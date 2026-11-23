package mcplogin

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// resourceBase is the path of the generic OAuth-protected MCP endpoint.
const resourceBase = "/mcp/oauth"

// Origin returns "scheme://host" for the request: https when the connection is
// TLS, else http; the host is r.Host lowercased and otherwise verbatim. It is
// used as the issuer and as the resource prefix, so "localhost" and
// "127.0.0.1" are different origins by design.
func Origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(r.Host)
}

// ResourcePath returns "/mcp/oauth" for an empty client id, else
// "/mcp/oauth/<id>".
func ResourcePath(clientID string) string {
	if clientID == "" {
		return resourceBase
	}
	return resourceBase + "/" + clientID
}

// ParseResourcePath accepts exactly /mcp/oauth, /mcp/oauth/, /mcp/oauth/<id>
// and /mcp/oauth/<id>/ where id passes auth.ValidClientID. Nothing deeper.
func ParseResourcePath(path string) (clientID string, generic, ok bool) {
	if path == resourceBase || path == resourceBase+"/" {
		return "", true, true
	}
	rest, found := strings.CutPrefix(path, resourceBase+"/")
	if !found {
		return "", false, false
	}
	rest = strings.TrimSuffix(rest, "/")
	if !auth.ValidClientID(rest) {
		return "", false, false
	}
	return rest, false, true
}

// ParseResource checks that resource is a URL on exactly the given origin with
// an OAuth resource path, no query and no fragment.
func ParseResource(origin, resource string) (clientID string, generic, ok bool) {
	rest, found := strings.CutPrefix(resource, origin)
	if !found || !strings.HasPrefix(rest, "/") {
		return "", false, false
	}
	u, err := url.Parse(resource)
	if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsAny(resource, "?#") || u.User != nil {
		return "", false, false
	}
	return ParseResourcePath(rest)
}
