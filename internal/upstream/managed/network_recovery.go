package managed

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Network recovery (GH #1929).
//
// A remote upstream that failed because the network was gone — a laptop
// asleep overnight, Wi-Fi switching, a VPN dropping — keeps failing while it is
// gone, and every failure advances the reconnect backoff until the client gives
// up and only probes every 30 minutes. When the network returns nothing used to
// shorten that wait. The upstream manager's connectivity watcher
// (upstream.networkRecovery) now probes such servers cheaply and calls
// RecoverFromNetworkOutage once their address is reachable again.

// NetworkRecoveryProbeAddress returns the host:port a connectivity probe for
// this server dials: the URL's host (default port by scheme), or the HTTP proxy
// that requests to it go through. ok is false for a server with no remote
// address (stdio).
func (mc *Client) NetworkRecoveryProbeAddress() (string, bool) {
	cfg := mc.GetConfig()
	if cfg == nil || cfg.URL == "" || cfg.Command != "" || strings.EqualFold(cfg.Protocol, "stdio") {
		return "", false
	}
	return probeAddressFor(cfg.URL, http.ProxyFromEnvironment)
}

// probeAddressFor resolves the address to probe for rawURL. A configured
// proxy is probed instead of the upstream: when one is in use, reaching it is
// what the upstream's own connection depends on.
func probeAddressFor(rawURL string, proxy func(*http.Request) (*url.URL, error)) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	target := u
	if proxy != nil {
		if p, perr := proxy(&http.Request{URL: u}); perr == nil && p != nil && p.Host != "" {
			target = p
		}
	}
	host := target.Hostname()
	if host == "" {
		return "", false
	}
	port := target.Port()
	if port == "" {
		switch strings.ToLower(target.Scheme) {
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
// this client: its current failure is a plain network failure (SetNetworkError)
// — not an auth failure, a pending sign-in, a proven-permanent fault or a user
// logout — and the upstream's own Retry-After window, if any, has passed.
// Give-up is NOT a bar: a server that gave up during a long outage is exactly
// the one waiting 30 minutes for no reason.
func (mc *Client) NetworkRecoveryEligible(now time.Time) bool {
	if mc == nil || mc.retired.Load() || mc.unregistered.Load() || mc.IsUserLoggedOut() {
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

// RecoverFromNetworkOutage starts one reconnect attempt for a client whose
// network failure is over, and reports whether it did. resetBackoff clears the
// retry ladder first — the watcher passes it only when it observed the outage
// end (probes failed, then succeeded), so the next failure, if any, starts the
// ladder over at 1s instead of waiting out a backoff built while the network
// was down. Without it the attempt is made on the current ladder, and a failure
// simply advances it: one attempt per wake or network change, then the normal
// backoff resumes (#1013).
func (mc *Client) RecoverFromNetworkOutage(resetBackoff bool, reason string) bool {
	if !mc.NetworkRecoveryEligible(time.Now()) || mc.IsConnecting() {
		return false
	}
	mc.reconnectMu.Lock()
	busy := mc.reconnectInProgress
	mc.reconnectMu.Unlock()
	if busy {
		return false
	}

	info := mc.StateManager.GetConnectionInfo()
	mc.logger.Info("Network connectivity to upstream is back; reconnecting now",
		zap.String("server", mc.GetConfig().Name),
		zap.String("reason", reason),
		zap.Bool("reset_backoff", resetBackoff),
		zap.Int("retry_count", info.RetryCount),
		zap.Bool("gave_up", info.GaveUp))

	if resetBackoff {
		mc.StateManager.Reset()
	}
	go mc.tryReconnect()
	return true
}
