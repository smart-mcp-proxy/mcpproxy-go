package httpapi

import (
	"errors"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// BindingGuardResponse is the 409 body of every write refused by the FR-008a
// binding guard (`binding_bypassable_without_auth`): a superset of the
// contract `{error, code, bindings, fixes}` carrying the same envelope keys
// as the other typed 409 (ConnectConflictResponse), so a client reads
// `error`/`code` the same way everywhere.
type BindingGuardResponse struct {
	Success   bool             `json:"success"` // Always false
	Error     string           `json:"error"`   // Human-readable, byte-stable refusal text
	Code      string           `json:"code"`    // binding_bypassable_without_auth
	Bindings  []GuardBinding   `json:"bindings"`
	Fixes     []GuardFixOption `json:"fixes"`
	RequestID string           `json:"request_id,omitempty"`
}

// GuardBinding names one client binding a guard refusal is about.
type GuardBinding struct {
	ClientID  string `json:"client_id"`
	TokenName string `json:"token_name"`
	Profile   string `json:"profile"`
	Mode      string `json:"mode"`
}

// GuardFixOption is one remediation of a guard refusal: kind is
// require_mcp_auth or set_anonymous_profile; target names the profile for the
// latter and is present only when that profile itself would not leave any
// binding bypassable.
type GuardFixOption struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
}

// writeIfBindingGuardRefusal answers a *runtime.BindingGuardError with the
// 409 body above (built by ProfilesErrorBody) and reports whether it handled err.
func (s *Server) writeIfBindingGuardRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	var refusal *internalRuntime.BindingGuardError
	if !errors.As(err, &refusal) {
		return false
	}
	return s.writeProfilesError(w, r, err)
}

// ClientBindingErrorResponse is the 409/400 body of a refused binding write:
// `code` is no_client_credential (409), `field` names the offending input
// (400).
type ClientBindingErrorResponse struct {
	Success   bool   `json:"success"` // Always false
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
	Field     string `json:"field,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func (s *Server) writeClientBindingError(w http.ResponseWriter, r *http.Request, status int, code, field, msg string) {
	s.writeJSON(w, status, ClientBindingErrorResponse{
		Success: false, Error: msg, Code: code, Field: field,
		RequestID: reqcontext.GetRequestID(r.Context()),
	})
}
