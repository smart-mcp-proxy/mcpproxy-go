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
