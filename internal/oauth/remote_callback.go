package oauth

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// configureRemoteCallback retains the loopback listener for local callers, but
// makes the same single-use state dispatcher reachable from the main HTTP mux.
// A config reload must not move a callback while a sign-in is waiting for it.
func (c *CallbackServer) configureRemoteCallback(callbackURL string) error {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()
	if c.remoteCallbackURL != callbackURL && len(c.waiters) > 0 {
		return fmt.Errorf("cannot change oauth.callback_base_url while a sign-in is waiting; finish or cancel that sign-in first")
	}
	c.remoteCallbackURL = callbackURL
	return nil
}

// EffectiveRedirectURI is the exact URI registered with the provider, which
// can differ from the private loopback listener behind a reverse proxy.
func (c *CallbackServer) EffectiveRedirectURI() string {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()
	if c.remoteCallbackURL != "" {
		return c.remoteCallbackURL
	}
	return c.RedirectURI
}

// RemoteCallbackHandler is mounted outside API-key middleware: an OAuth
// provider has no API key. The unpredictable, single-use state authenticates
// the callback, and only explicitly configured remote flows can receive it.
func (m *CallbackServerManager) RemoteCallbackHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	encodedName := strings.TrimPrefix(r.URL.EscapedPath(), config.OAuthRemoteCallbackPrefix)
	serverName, err := url.PathUnescape(encodedName)
	if err != nil || encodedName == "" || strings.Contains(encodedName, "/") {
		http.NotFound(w, r)
		return
	}
	c, exists := m.GetCallbackServer(serverName)
	if !exists {
		http.NotFound(w, r)
		return
	}
	c.waitersMu.Lock()
	callbackURL := c.remoteCallbackURL
	c.waitersMu.Unlock()
	expected, err := url.Parse(callbackURL)
	if err != nil || callbackURL == "" || expected.EscapedPath() != r.URL.EscapedPath() {
		http.NotFound(w, r)
		return
	}
	// Reject missing/duplicate parameters before they can consume a waiter.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["state"]) != 1 || query.Get("state") == "" || len(query["code"]) > 1 || len(query["error"]) > 1 || (query.Get("code") == "" && query.Get("error") == "") || (query.Get("code") != "" && query.Get("error") != "") {
		http.Error(w, "invalid OAuth callback", http.StatusBadRequest)
		return
	}
	// Invalid public requests must not mark an otherwise healthy upstream as
	// failed. Delivery still atomically consumes the matching waiter once.
	c.handleCallbackWithFailureNotification(w, r, false)
}
