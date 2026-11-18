package managed

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Network recovery (GH #1929).
//
// A remote upstream that failed because the network was gone — a laptop
// asleep overnight, Wi-Fi switching, a VPN dropping — keeps failing while it is
// gone, and every failure advances the reconnect backoff until the client gives
// up and only probes every 30 minutes. The upstream manager's connectivity
// watcher (upstream.networkRecovery) probes such servers cheaply and, once the
// network is demonstrably back, admits a reconnect with BeginNetworkRecovery
// and runs it with ReconnectAfterNetworkRecovery.

// NetworkProbe says how the connectivity watcher checks that a server is
// reachable: a TCP dial to Addr and, when ConnectTo is set, an HTTP CONNECT to
// ConnectTo through the proxy at Addr, which proves the path beyond the proxy.
// ProxyAuth is a ready Proxy-Authorization value (never logged).
type NetworkProbe struct {
	Addr      string
	ConnectTo string
	ProxyAuth string
}

// Key identifies the probe: servers with equal keys share one probe per tick.
func (p NetworkProbe) Key() string { return p.Addr + "|" + p.ConnectTo }

// NetworkRecoveryProbe returns how to probe this server's reachability. ok is
// false for a server with no remote address (stdio).
func (mc *Client) NetworkRecoveryProbe() (NetworkProbe, bool) {
	cfg := mc.GetConfig()
	if cfg == nil || cfg.URL == "" || cfg.Command != "" || strings.EqualFold(cfg.Protocol, "stdio") {
		return NetworkProbe{}, false
	}
	return probeFor(cfg.URL, http.ProxyFromEnvironment)
}

// probeFor resolves the probe for rawURL. Through an http:// proxy, an https
// upstream is probed with CONNECT to the upstream, so a proxy that stays
// reachable while the network behind it is down does not hide the outage. A
// plain-http upstream, or an https:// or SOCKS proxy, is probed at the proxy
// only.
func probeFor(rawURL string, proxy func(*http.Request) (*url.URL, error)) (NetworkProbe, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return NetworkProbe{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return NetworkProbe{}, false
	}
	target, ok := hostPort(u)
	if !ok {
		return NetworkProbe{}, false
	}
	if proxy != nil {
		if p, perr := proxy(&http.Request{URL: u}); perr == nil && p != nil && p.Host != "" {
			paddr, pok := hostPort(p)
			if !pok {
				return NetworkProbe{}, false
			}
			probe := NetworkProbe{Addr: paddr}
			if scheme == "https" && strings.EqualFold(p.Scheme, "http") {
				probe.ConnectTo = target
				if p.User != nil {
					pw, _ := p.User.Password()
					probe.ProxyAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+pw))
				}
			}
			return probe, true
		}
	}
	return NetworkProbe{Addr: target}, true
}

func hostPort(u *url.URL) (string, bool) {
	host := u.Hostname()
	if host == "" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(host, port), true
}

// networkRecoveryMode is what a restored network can do for a client.
type networkRecoveryMode int

const (
	recoveryNone networkRecoveryMode = iota
	// recoveryReconnect: the client is in Error on a network failure.
	recoveryReconnect
	// recoveryRefresh: the client is connected, but its OAuth token refresh
	// failed transiently (offline, provider 5xx) and the expired token keeps
	// tool calls failing until the next refresh (QA run2 cooldown-503).
	recoveryRefresh
)

// networkRecoveryMode decides which recovery, if any, applies now. The common
// gates come first: the instance is live, enabled, not quarantined and not
// logged out. A quarantined server is refused even when a review had it
// connect (an inspection exemption): the watcher cannot see whether that is
// still current, and the supervisor reconnects it while it is.
func (mc *Client) networkRecoveryMode(now time.Time) networkRecoveryMode {
	if mc == nil || mc.retired.Load() || mc.unregistered.Load() || mc.IsUserLoggedOut() {
		return recoveryNone
	}
	cfg := mc.GetConfig()
	if cfg == nil || !cfg.Enabled || cfg.Quarantined {
		return recoveryNone
	}
	info := mc.StateManager.GetConnectionInfo()
	switch info.State {
	case types.StateError:
		// A plain network failure only — not an auth failure, a pending
		// sign-in or a proven-permanent fault — and past any Retry-After
		// window. Give-up is NOT a bar: a server that gave up during a long
		// outage is exactly the one waiting 30 minutes for no reason.
		if !info.NetworkFailure || info.IsOAuthError || info.Terminal {
			return recoveryNone
		}
		if !info.RetryAfter.IsZero() && now.Before(info.RetryAfter) {
			return recoveryNone
		}
		return recoveryReconnect
	case types.StateReady:
		if mc.oauthRefreshStalled() {
			return recoveryRefresh
		}
	}
	return recoveryNone
}

// oauthRefreshStalled reports whether this server's OAuth token refresh failed
// transiently and has not succeeded since. A revoked grant (invalid_grant) is
// latched by the coordinator and never reads as stalled.
func (mc *Client) oauthRefreshStalled() bool {
	cfg := mc.GetConfig()
	if cfg == nil || cfg.URL == "" {
		return false
	}
	return oauth.DefaultRefreshCoordinator().TransientlyFailing(oauth.GenerateServerKey(cfg.Name, cfg.URL), mc.CredentialBinding())
}

// NetworkRecoveryEligible reports whether a restored network may act on this
// client: reconnect it (Error on a network failure) or refresh its OAuth token
// (connected, refresh failing transiently).
func (mc *Client) NetworkRecoveryEligible(now time.Time) bool {
	return mc.networkRecoveryMode(now) != recoveryNone
}

// NetworkRecoveryHealthy reports whether the client is fully working —
// connected, with no OAuth refresh failing. The watcher restores a server's
// recovery budget when it sees this.
func (mc *Client) NetworkRecoveryHealthy() bool {
	return mc.IsConnected() && !mc.oauthRefreshStalled()
}

// BeginNetworkRecovery admits one recovery for a client whose network problem
// is over and reports whether it did; the caller then runs
// ReconnectAfterNetworkRecovery.
//
// For a client in Error, resetBackoff restarts the retry ladder — the watcher
// passes it only when it observed the outage end — atomically with the
// eligibility check (StateManager.ClearNetworkBackoff), so a connect that
// started or finished meanwhile is never overwritten. Without it the attempt is
// made on the current ladder and a failure simply advances it.
//
// Either way the OAuth refresh cooldown of the server's credential is lifted:
// refreshes that failed while the machine was offline would otherwise make the
// recovery wait up to five minutes for a refresh that now succeeds. A revoked
// grant stays latched.
func (mc *Client) BeginNetworkRecovery(resetBackoff bool, reason string) bool {
	now := time.Now()
	mode := mc.networkRecoveryMode(now)
	switch mode {
	case recoveryNone:
		return false
	case recoveryReconnect:
		if mc.IsConnecting() {
			return false
		}
		if resetBackoff && !mc.StateManager.ClearNetworkBackoff(now) {
			return false
		}
	}
	cfg := mc.GetConfig()
	if cfg.URL != "" {
		oauth.DefaultRefreshCoordinator().ClearTransientCooldown(oauth.GenerateServerKey(cfg.Name, cfg.URL), mc.CredentialBinding())
	}
	info := mc.StateManager.GetConnectionInfo()
	if mode == recoveryRefresh {
		mc.logger.Info("Network connectivity to upstream is back; retrying the OAuth token refresh now",
			zap.String("server", cfg.Name),
			zap.String("reason", reason))
		return true
	}
	mc.logger.Info("Network connectivity to upstream is back; reconnecting now",
		zap.String("server", cfg.Name),
		zap.String("reason", reason),
		zap.Bool("reset_backoff", resetBackoff),
		zap.Int("retry_count", info.RetryCount),
		zap.Bool("gave_up", info.GaveUp))
	return true
}

// ConnectSuccessCount reports how many connects of this client reached Ready.
func (mc *Client) ConnectSuccessCount() uint64 { return mc.connectSuccesses.Load() }

// networkRecoveryRefreshTimeout bounds the token refresh a recovery runs.
const networkRecoveryRefreshTimeout = 30 * time.Second

// ReconnectAfterNetworkRecovery runs the recovery BeginNetworkRecovery
// admitted and blocks until it ends. ctx bounds it (the manager's shutdown
// context). A client in Error is reconnected, honouring the health loop's stop
// channel so a Disconnect that lands first is not undone. A connected client
// with a stalled refresh gets one proactive refresh through the coordinator
// (single flight; a revoked grant is answered by the latch without a request);
// its connection is left alone. It does nothing if the client no longer
// qualifies.
func (mc *Client) ReconnectAfterNetworkRecovery(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	// The mode re-check and the stop-channel read happen together under
	// mc.mu, which Disconnect also takes: either the Disconnect already ran
	// (the state is no longer Error, so nothing is done) or the channel read
	// here is the one it will close, which stops the reconnect.
	mc.mu.Lock()
	mode := mc.networkRecoveryMode(time.Now())
	stop := mc.stopMonitoring
	mc.mu.Unlock()
	switch mode {
	case recoveryReconnect:
		mc.tryReconnectWithin(ctx, stop)
	case recoveryRefresh:
		rctx, cancel := context.WithTimeout(ctx, networkRecoveryRefreshTimeout)
		defer cancel()
		if err := mc.RefreshOAuthTokenDirect(rctx); err != nil {
			mc.logger.Warn("OAuth token refresh after the network came back failed",
				zap.String("server", mc.GetConfig().Name),
				zap.Error(err))
		}
	}
}
