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

// NetworkRecoveryEligible reports whether a restored network may reconnect
// this client: it is enabled and not quarantined, and its current failure is a
// plain network failure (SetNetworkError) — not an auth failure, a pending
// sign-in, a proven-permanent fault or a user logout — and the upstream's own
// Retry-After window, if any, has passed. Give-up is NOT a bar: a server that
// gave up during a long outage is exactly the one waiting 30 minutes for no
// reason. A quarantined server is refused even when a review had it connect
// (an inspection exemption): the watcher cannot see whether that is still
// current, and the supervisor reconnects it while it is.
func (mc *Client) NetworkRecoveryEligible(now time.Time) bool {
	if mc == nil || mc.retired.Load() || mc.unregistered.Load() || mc.IsUserLoggedOut() {
		return false
	}
	cfg := mc.GetConfig()
	if cfg == nil || !cfg.Enabled || cfg.Quarantined {
		return false
	}
	info := mc.StateManager.GetConnectionInfo()
	if info.State != types.StateError || !info.NetworkFailure || info.IsOAuthError || info.Terminal {
		return false
	}
	if !info.RetryAfter.IsZero() && now.Before(info.RetryAfter) {
		return false
	}
	return true
}

// BeginNetworkRecovery admits one reconnect for a client whose network failure
// is over and reports whether it did; the caller then runs
// ReconnectAfterNetworkRecovery. resetBackoff restarts the retry ladder — the
// watcher passes it only when it observed the outage end — atomically with the
// eligibility check (StateManager.ClearNetworkBackoff), so a connect that
// started or finished meanwhile is never overwritten. Without it the attempt
// is made on the current ladder and a failure simply advances it.
//
// Either way the OAuth refresh cooldown of the server's credential is lifted:
// refreshes that failed while the machine was offline would otherwise make the
// reconnect wait up to five minutes for a refresh that now succeeds. A revoked
// grant stays latched.
func (mc *Client) BeginNetworkRecovery(resetBackoff bool, reason string) bool {
	now := time.Now()
	if !mc.NetworkRecoveryEligible(now) || mc.IsConnecting() {
		return false
	}
	if resetBackoff && !mc.StateManager.ClearNetworkBackoff(now) {
		return false
	}
	cfg := mc.GetConfig()
	if cfg.URL != "" {
		oauth.DefaultRefreshCoordinator().ClearTransientCooldown(oauth.GenerateServerKey(cfg.Name, cfg.URL), mc.CredentialBinding())
	}
	info := mc.StateManager.GetConnectionInfo()
	mc.logger.Info("Network connectivity to upstream is back; reconnecting now",
		zap.String("server", cfg.Name),
		zap.String("reason", reason),
		zap.Bool("reset_backoff", resetBackoff),
		zap.Int("retry_count", info.RetryCount),
		zap.Bool("gave_up", info.GaveUp))
	return true
}

// ReconnectAfterNetworkRecovery runs the reconnect BeginNetworkRecovery
// admitted. It blocks until the attempt ends. ctx bounds it (the manager's
// shutdown context), and it honours the health loop's stop channel, so a
// Disconnect — the server disabled, removed or shut down — that lands first
// is not undone. It does nothing if the client is no longer eligible.
func (mc *Client) ReconnectAfterNetworkRecovery(ctx context.Context) {
	if ctx.Err() != nil || !mc.NetworkRecoveryEligible(time.Now()) {
		return
	}
	mc.mu.Lock()
	stop := mc.stopMonitoring
	mc.mu.Unlock()
	mc.tryReconnectWithin(ctx, stop)
}
