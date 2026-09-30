package httpapi

import (
	"errors"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// BindingGuardResponse is the 409 body of every write refused by the FR-008a
// binding guard (`binding_bypassable_without_auth`): a superset of the
// contract `{error, code, bindings, fixes}` carrying the same envelope keys
// as the other typed 409 (ConnectConflictResponse), so a client reads
// `error`/`code` the same way everywhere.
type BindingGuardResponse struct {
	Success   bool                         `json:"success"` // Always false
	Error     string                       `json:"error"`   // Human-readable, byte-stable refusal text
	Code      string                       `json:"code"`    // binding_bypassable_without_auth
	Bindings  []internalRuntime.BindingRef `json:"bindings"`
	Fixes     []internalRuntime.GuardFix   `json:"fixes"`
	RequestID string                       `json:"request_id,omitempty"`
}

// writeIfBindingGuardRefusal answers a *runtime.BindingGuardError with the
// 409 body above and reports whether it handled err.
func (s *Server) writeIfBindingGuardRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	var refusal *internalRuntime.BindingGuardError
	if !errors.As(err, &refusal) {
		return false
	}
	bindings, fixes := refusal.Bindings, refusal.Fixes
	if bindings == nil {
		bindings = []internalRuntime.BindingRef{}
	}
	if fixes == nil {
		fixes = []internalRuntime.GuardFix{}
	}
	s.writeJSON(w, http.StatusConflict, BindingGuardResponse{
		Success:   false,
		Error:     refusal.Error(),
		Code:      profile.ErrorCodeBindingBypassable,
		Bindings:  bindings,
		Fixes:     fixes,
		RequestID: reqcontext.GetRequestID(r.Context()),
	})
	return true
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
