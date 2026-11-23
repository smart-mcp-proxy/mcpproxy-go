package connect

import (
	"errors"
	"fmt"
	"strings"
)

// Browser login in Connect (Spec 132, PR-3). A client that supports it can be
// given only an address, `<base>/oauth/<client-id>` (so `…/mcp/oauth/claude-code`),
// and sign in through the browser. Nothing secret is written: no header, no
// bridge argument, no header table, and the credential minter is not called.

// Values of CredentialIntent.Auth / ConnectOptions.Auth. The empty value is
// AuthKey, the way Connect has always worked.
const (
	AuthKey   = "key"
	AuthOAuth = "oauth"
)

// oauthPathSegment is appended to the MCP endpoint to form a client's browser
// login address: `/mcp` + `/oauth/` + client id.
const oauthPathSegment = "/oauth"

// authNoteAPIKeyKept is said on every successful oauth write and on its
// preview: Connect never revokes an API key, so the user is told what stays.
const authNoteAPIKeyKept = "Connect does not revoke this client's existing API key: it keeps working until you revoke it " +
	"(\"Revoke API key\" on the Clients page). The config entry now holds only the sign-in address."

// Refusals of an oauth connect. All of them are raised before anything is
// read or written.
var (
	ErrBrowserLoginEdition  = errors.New("browser login is not available in the server edition")
	ErrBrowserLoginDisabled = errors.New("browser login is off: set mcp_oauth_enabled to true (or MCPPROXY_MCP_OAUTH_ENABLED=true) first")
	ErrBrowserLoginKeyless  = errors.New("auth oauth cannot be combined with keyless: a browser login has no config credential to leave out")
	ErrBrowserLoginProfile  = errors.New("auth oauth cannot carry a profile or mode: the access is chosen on the consent page")
)

// WithMCPOAuthEnabled installs the live accessor for mcp_oauth_enabled, so the
// service refuses an oauth connect while the feature is off. Without one the
// feature counts as off. Returns the receiver for chaining.
func (s *Service) WithMCPOAuthEnabled(fn func() bool) *Service {
	s.mcpOAuthEnabled = fn
	return s
}

// browserLoginOn reports whether this build and configuration offer browser
// login at all.
func (s *Service) browserLoginOn() bool {
	return browserLoginEditionSupported && s.mcpOAuthEnabled != nil && s.mcpOAuthEnabled()
}

// browserLoginAvailable is true when Connect may write an oauth entry for the
// client: the feature is on, this is the personal edition and the client passed
// a real-client check.
func (s *Service) browserLoginAvailable(c *ClientDef) bool {
	return c != nil && c.Supported && c.SupportsBrowserLogin && s.browserLoginOn()
}

// planAuth validates the auth choice of an intent for a client and reports
// whether the write is an oauth one. It is pure input validation, run before
// any read, write or mint.
func (s *Service) planAuth(clientID string, intent CredentialIntent) (oauth bool, err error) {
	oauth, err = s.planAuthChoice(clientID, intent)
	if err != nil {
		return false, &AuthChoiceError{Err: err}
	}
	return oauth, nil
}

// AuthChoiceError is a refused auth choice (feature off, unsupported client,
// server edition, conflicting flags, unknown value). It is the caller's input
// that is wrong, never a failure of the write, so surfaces map it to a usage
// refusal (HTTP 400, CLI exit 1).
type AuthChoiceError struct{ Err error }

func (e *AuthChoiceError) Error() string { return e.Err.Error() }
func (e *AuthChoiceError) Unwrap() error { return e.Err }

func (s *Service) planAuthChoice(clientID string, intent CredentialIntent) (oauth bool, err error) {
	switch intent.Auth {
	case "", AuthKey:
		return false, nil
	case AuthOAuth:
	default:
		return false, fmt.Errorf("unknown auth %q (want %s or %s)", intent.Auth, AuthKey, AuthOAuth)
	}
	if !browserLoginEditionSupported {
		return false, ErrBrowserLoginEdition
	}
	if !s.browserLoginOn() {
		return false, ErrBrowserLoginDisabled
	}
	client := FindClient(clientID)
	if client == nil || !client.SupportsBrowserLogin {
		name := clientID
		if client != nil {
			name = client.Name
		}
		return false, fmt.Errorf("%s does not support browser login in MCPProxy yet (only clients with a recorded real-client check do); use the API key", name)
	}
	if intent.Keyless {
		return false, ErrBrowserLoginKeyless
	}
	if (intent.Profile != nil && *intent.Profile != "") || intent.Mode != nil {
		return false, ErrBrowserLoginProfile
	}
	return true, nil
}

// oauthURL is the per-client browser login address of this instance.
func (s *Service) oauthURL(clientID string) string {
	return s.baseURL() + oauthPathSegment + "/" + clientID
}

// entryParamsFor builds the entry parameters for a client: with oauth set the
// address is the per-client login address and no credential is carried, so no
// header, bridge argument or header table can be built from it.
func (s *Service) entryParamsFor(clientID string, oauth bool, credential string) serverEntryParams {
	if oauth {
		return serverEntryParams{baseURL: s.oauthURL(clientID)}
	}
	return s.entryParams(credential)
}

// urlPointsHere reports whether a URL found in a client config addresses this
// instance: the MCP endpoint itself (with or without a legacy ?apikey= query)
// or one of its browser login addresses (`<base>/oauth`, `<base>/oauth/<id>`).
func urlPointsHere(u, baseURL string) bool {
	if u == baseURL || strings.HasPrefix(u, baseURL+"?") {
		return true
	}
	return isOAuthAddress(u, baseURL)
}

// isOAuthAddress reports whether u is a browser login address of the instance
// at baseURL.
func isOAuthAddress(u, baseURL string) bool {
	prefix := baseURL + oauthPathSegment
	if u == prefix {
		return true
	}
	return strings.HasPrefix(u, prefix+"/") || strings.HasPrefix(u, prefix+"?")
}

// entryIsOAuth reports whether a parsed client entry carries a browser login
// address of this instance in one of the URL fields clients use.
func (s *Service) entryIsOAuth(entry map[string]interface{}) bool {
	base := s.baseURL()
	for _, field := range []string{"url", "serverUrl", "httpUrl"} {
		if u, ok := entry[field].(string); ok && isOAuthAddress(u, base) {
			return true
		}
	}
	return false
}

// authNote is the sentence shown beside an oauth write or preview.
func authNote(oauth bool) string {
	if oauth {
		return authNoteAPIKeyKept
	}
	return ""
}

// displayClientName is a client's display name, or its id when unknown.
func displayClientName(id string) string {
	if c := FindClient(id); c != nil {
		return c.Name
	}
	return id
}
