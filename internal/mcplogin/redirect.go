package mcplogin

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// MaxRedirectURIs is the most redirect URIs one registration may carry.
const MaxRedirectURIs = 5

// ErrRedirectCount is returned when there are no redirect URIs or too many.
var ErrRedirectCount = errors.New("mcplogin: redirect_uris must hold 1 to 5 entries")

func isLoopbackHost(h string) bool {
	return h == "127.0.0.1" || h == "[::1]" || h == "localhost"
}

// parseRedirect parses one redirect URI and enforces the shape rules shared
// by registration and matching.
func parseRedirect(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("redirect uri is not a valid URL")
	}
	if !u.IsAbs() || u.Opaque != "" {
		return nil, fmt.Errorf("redirect uri must be an absolute URL")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, fmt.Errorf("redirect uri must not have a fragment")
	}
	if u.User != nil {
		return nil, fmt.Errorf("redirect uri must not carry credentials")
	}
	switch u.Scheme {
	case "http":
		if !isLoopbackHost(u.Host[:hostEnd(u.Host)]) {
			return nil, fmt.Errorf("http redirect uri must use 127.0.0.1, [::1] or localhost")
		}
	case "https":
		if u.Hostname() == "" {
			return nil, fmt.Errorf("https redirect uri needs a host")
		}
	default:
		return nil, fmt.Errorf("redirect uri scheme %q is not allowed", u.Scheme)
	}
	return u, nil
}

// hostEnd returns the index where the port starts in a host[:port] string
// (len(h) when there is none), understanding bracketed IPv6.
func hostEnd(h string) int {
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i >= 0 {
			return i + 1
		}
		return len(h)
	}
	if i := strings.LastIndex(h, ":"); i >= 0 {
		return i
	}
	return len(h)
}

func isLoopbackURL(u *url.URL) bool {
	return u.Scheme == "http" && isLoopbackHost(u.Host[:hostEnd(u.Host)])
}

// stripPort removes the port from the authority of an already validated URI
// string and leaves every other byte alone, so two loopback URIs compare equal
// only when they differ by port at most.
func stripPort(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	start := i + 3
	end := len(raw)
	if j := strings.IndexAny(raw[start:], "/?#"); j >= 0 {
		end = start + j
	}
	auth := raw[start:end]
	return raw[:start] + auth[:hostEnd(auth)] + raw[end:]
}

// ValidateRegisteredRedirects checks the redirect URIs of a registration: 1 to
// 5 entries, each an absolute URL without fragment or user info, either http
// on 127.0.0.1, [::1] or localhost, or https with a host. Custom schemes are
// refused.
func ValidateRegisteredRedirects(uris []string) error {
	if len(uris) < 1 || len(uris) > MaxRedirectURIs {
		return ErrRedirectCount
	}
	for _, raw := range uris {
		if _, err := parseRedirect(raw); err != nil {
			return err
		}
	}
	return nil
}

// MatchRedirect finds the registered URI that the presented redirect URI
// stands for. https must match exactly; loopback http matches on scheme, host,
// path and query with the port ignored (RFC 8252 section 7.3). For loopback the
// presented URI is returned, since it names the port the client listens on.
func MatchRedirect(registered []string, presented string) (matched string, ok bool) {
	p, err := parseRedirect(presented)
	if err != nil {
		return "", false
	}
	for _, reg := range registered {
		if p.Scheme == "https" {
			if reg == presented {
				return presented, true
			}
			continue
		}
		r, err := parseRedirect(reg)
		if err != nil || !isLoopbackURL(r) {
			continue
		}
		if isLoopbackURL(p) && stripPort(reg) == stripPort(presented) {
			return presented, true
		}
	}
	return "", false
}

// BuildRedirect parses base, keeps its existing query and appends params
// percent-encoded. The order is deterministic: code, error, error_description
// and iss first, then state, then any other key alphabetically. Which keys are
// present is decided entirely by the map.
func BuildRedirect(base string, params map[string]string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || !u.IsAbs() {
		return "", errors.New("mcplogin: invalid redirect base")
	}
	order := []string{"code", "error", "error_description", "iss"}
	var keys []string
	seen := map[string]bool{}
	for _, k := range order {
		if _, ok := params[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range params {
		if !seen[k] && k != "state" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	if _, ok := params["state"]; ok {
		keys = append(keys, "state")
	}
	var b strings.Builder
	b.WriteString(u.RawQuery)
	for _, k := range keys {
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(k))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(params[k]))
	}
	u.RawQuery = b.String()
	return u.String(), nil
}
