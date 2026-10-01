package connect

import (
	"errors"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Client credentials in connect (Spec 108, FR-024). Connect never writes the
// instance admin API key: every write carries a per-client `mcp_cli_`
// credential minted by an injected CredentialMinter (the runtime clients
// service), or nothing at all (--keyless, only while require_mcp_auth is off).

// maskClientCredential is the placeholder shown for a client credential in a
// preview payload. It keeps the literal `mcp_cli_` prefix so a UI can tell a
// client credential from an admin key, and the secret itself never leaves the
// core in a preview.
const maskClientCredential = "mcp_cli_••••"

// Errors returned by the connect credential path. All of them are raised
// BEFORE anything is written to a client config.
var (
	// ErrNoCredentialMinter: require_mcp_auth is on but no minter is wired.
	// Connect never falls back to the admin API key (fail closed).
	ErrNoCredentialMinter = errors.New("connect needs the client-credential store to mint a credential")
	// ErrKeylessRequiresAuthOff: a credential-less entry cannot work while
	// /mcp demands authentication.
	ErrKeylessRequiresAuthOff = errors.New("keyless connect is only possible while require_mcp_auth is off")
	// ErrKeylessWithProfile: a keyless client is unidentified, so it cannot
	// carry a profile binding.
	ErrKeylessWithProfile = errors.New("keyless connect cannot carry a profile or mode: an unidentified client has no binding")
)

// CredentialIntent is what the caller asked connect to mint. A nil Profile
// or Mode means "not specified": a fresh credential defaults to the built-in
// "All servers" scope (empty profile, switchable) and a reconnect keeps the
// binding the client already has. Profile "" is an explicit All servers.
type CredentialIntent struct {
	Profile *string
	Mode    *string
	Keyless bool

	// Actor attribution for the profile_change record (FR-030).
	ActorKind string
	ActorName string
	Surface   string
}

// IssuedCredential is a credential a minter has produced for one connect
// write. Secret is the raw `mcp_cli_` value to embed; it is never logged,
// never put in a precondition token digest and never returned by an API.
type IssuedCredential struct {
	Secret    string
	TokenName string
	Profile   string
	Mode      string
	// Rotating is true when the credential is a staged rotation over an
	// active one (both secrets authenticate until Commit/Abort, FR-021a).
	Rotating bool
	// Pending carries opaque minter state for Commit (a reconnect that
	// also changes the binding applies it after the write succeeds).
	Pending interface{}
}

// CredentialMinter mints, commits and aborts client credentials for connect,
// and classifies secrets found in a client config. Implemented by
// runtime.ClientsService.
type CredentialMinter interface {
	// Issue validates the intent, runs the FR-008a guard over the candidate
	// state, and mints a fresh credential or stages a rotation.
	Issue(clientID string, intent CredentialIntent) (*IssuedCredential, error)
	// CheckIdle refuses (connect_in_progress) while another connect of
	// clientID holds the in-flight claim. A keyless connect mints nothing and
	// so never calls Issue, but it still writes the client's config and must
	// honour the same claim (FR-021a).
	CheckIdle(clientID string) error
	// Commit finalizes a successful write (fresh: records the mint; rotation:
	// finalizes it and applies any binding change).
	Commit(clientID string, intent CredentialIntent, issued *IssuedCredential) error
	// Abort undoes Issue after a failed write (fresh: forget; rotation:
	// rollback, the old secret keeps working).
	Abort(clientID string, intent CredentialIntent, issued *IssuedCredential) error
	// Release ends the in-flight claim Issue took without committing or
	// aborting: the write outcome is ambiguous, so the reconciler resolves the
	// staged rotation from what the config actually holds (FR-021a).
	Release(clientID string, issued *IssuedCredential)
	// PreviewBinding reports the profile and mode Issue would apply for this
	// intent (a reconnect with no profile keeps the recorded binding),
	// minting and staging nothing.
	PreviewBinding(clientID string, intent CredentialIntent) (profile, mode string, err error)
	// Classify reports the credential state of a `mcp_cli_` secret found in
	// clientID's config: client|revoked|expired, or none for anything that is
	// not a credential of this client.
	Classify(clientID, secret string) profile.CredentialState
	// HeldByRecord reports whether secret is the primary or pending secret of
	// clientID's credential record.
	HeldByRecord(clientID, secret string) bool
	// ForgetUnheld revokes clientID's active credential when the config now
	// holds a different secret (undo of a connect that minted). It returns the
	// revoked token name, or "" when nothing was revoked.
	ForgetUnheld(clientID, restoredSecret string, intent CredentialIntent) (string, error)
}

// WithCredentialMinter installs the minter connect uses to obtain client
// credentials. Returns the receiver for chaining.
func (s *Service) WithCredentialMinter(m CredentialMinter) *Service {
	s.minter = m
	return s
}

// ClientSecret reads clientID's config on demand and returns the credential
// secret its mcpproxy entry carries ("" when none). It is the reconciler's
// ClientConfigReader (FR-021a). found is false when there is no entry.
func (s *Service) ClientSecret(clientID string) (secret string, found bool, err error) {
	client := FindClient(clientID)
	if client == nil || !client.Supported {
		return "", false, errors.New("unknown or unsupported client")
	}
	cfgPath := s.configPath(clientID)
	loc, ok, outcome := s.entryAccess(*client, cfgPath)
	if outcome != accessAccessible {
		return "", false, errors.New("client config not readable: " + outcome)
	}
	if !ok {
		return "", false, nil
	}
	secret, _ = extractEntryCredential(clientID, loc.Entry)
	return secret, true, nil
}

// extractEntryCredential pulls the credential secret out of an existing
// client entry through the same carriers buildServerEntry writes: an
// X-API-Key header, an mcp-remote `--header X-API-Key:<v>` arg, or an
// ?apikey= query on url / serverUrl / httpUrl. Empty when there is none.
// The value is returned only to be classified; callers never echo it.
func extractEntryCredential(clientID string, entry interface{}) (string, bool) {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return "", false
	}
	if headers, ok := m["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if strings.EqualFold(k, "x-api-key") {
				if sv, ok := v.(string); ok && sv != "" {
					return sv, true
				}
			}
		}
	}
	if args := stringSlice(m["args"]); len(args) > 0 {
		for i, a := range args {
			if a == "--header" && i+1 < len(args) {
				if v, ok := cutHeaderValue(args[i+1]); ok {
					return v, true
				}
			}
			if v, ok := cutHeaderValue(a); ok && strings.HasPrefix(strings.ToLower(a), "x-api-key:") {
				return v, true
			}
		}
	}
	for _, key := range []string{"url", "serverUrl", "httpUrl"} {
		if raw, ok := m[key].(string); ok && raw != "" {
			if u, err := url.Parse(raw); err == nil {
				if v := u.Query().Get("apikey"); v != "" {
					return v, true
				}
			}
		}
	}
	return "", false
}

func cutHeaderValue(s string) (string, bool) {
	name, value, ok := strings.Cut(s, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(name), "x-api-key") {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func stringSlice(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// classifyEntrySecret maps a secret found in a client config to its
// credential state (plan D7). apiKey is the instance admin key. Every
// comparison is constant-time and the value is never echoed.
func (s *Service) classifyEntrySecret(clientID, secret, apiKey string) profile.CredentialState {
	if secret == "" {
		return profile.CredentialStateNone
	}
	if apiKey != "" && auth.ConstantTimeEqual(secret, apiKey) {
		return profile.CredentialStateAdminKey
	}
	if kind, ok := auth.ValidateAnyTokenFormat(secret); ok && kind == auth.KindClient && s.minter != nil {
		return s.minter.Classify(clientID, secret)
	}
	return profile.CredentialStateNone
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
