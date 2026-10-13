package server

import (
	"net/http"
	"strings"
)

// uiRoutePrefixes are the first path segments the Web UI owns. A request for
// one of them without the `/ui` prefix is someone deep-linking (or pasting a
// link from a doc, an issue, or another machine's address bar) — not a stray
// API call — so it is redirected into the SPA instead of being answered with
// Go's plain-text `404 page not found` (audit F33).
//
// This mirrors the top-level routes in frontend/src/router/index.ts. It is an
// allowlist on purpose: a blanket "redirect everything unknown" would turn a
// mistyped API path into a 302 towards an HTML page, which is a far more
// confusing failure for a client than a 404.
var uiRoutePrefixes = map[string]struct{}{
	"servers":      {},
	"tools":        {},
	"activity":     {},
	"security":     {},
	"sessions":     {},
	"secrets":      {},
	"repositories": {},
	"settings":     {},
	"tokens":       {},
	"feedback":     {},
	"search":       {},
	"login":        {},
	"my":           {},
	"admin":        {},
}

// uiRedirectTarget reports where a prefix-less deep link should go, or false
// when the request is not one.
//
// The path is rebuilt from the ESCAPED form: a server name with an encoded
// slash (`/servers/my%2Fserver`) must survive the round trip, and decoding it
// here would hand the SPA a different route than the one that was asked for.
// The query string is carried across verbatim so `?apikey=…` — the only way a
// browser authenticates to the UI on a fresh profile — is not dropped by the
// redirect.
func uiRedirectTarget(r *http.Request) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", false
	}

	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, "/") || escaped == "/" {
		return "", false
	}

	// A protocol-relative path ("//evil.example") would otherwise be forwarded
	// as-is; "/ui" + that is still a local path, but the intent is clearly not a
	// UI deep link, so it is refused outright.
	if strings.HasPrefix(escaped, "//") {
		return "", false
	}

	segment := strings.SplitN(strings.TrimPrefix(escaped, "/"), "/", 2)[0]
	if _, ok := uiRoutePrefixes[segment]; !ok {
		return "", false
	}

	target := "/ui" + escaped
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target, true
}
