package httpapi

import (
	"errors"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// requestError is a refusal of a shared admin operation that carries its own
// HTTP status (a malformed request the service never sees). REST writes it with
// that status and the MCP `profiles` tool reports only its message.
type requestError struct {
	status int
	msg    string
}

func (e *requestError) Error() string { return e.msg }

// ProfilesErrorBody maps an error of the profiles service, the clients service
// or an admin view to the REST error status and the error body WITHOUT the REST
// envelope (`success` and `request_id`). It is the single mapping behind every
// profile and client-binding refusal: the REST writers add the envelope, and the
// MCP `profiles` tool (Spec 108-h) returns the body as its error text, so the two
// surfaces cannot drift (FR-037).
//
// The keys are {error, code?, field?, used_by?, bindings?, fixes?}. An error the
// mapping does not know is a 500 {error: "profiles operation failed"}.
func ProfilesErrorBody(err error) (int, map[string]any) {
	status, body, known := profilesErrorBody(err)
	if !known {
		return http.StatusInternalServerError, map[string]any{"error": "profiles operation failed"}
	}
	return status, body
}

func errorBody(msg, code, field string) map[string]any {
	body := map[string]any{"error": msg}
	if code != "" {
		body["code"] = code
	}
	if field != "" {
		body["field"] = field
	}
	return body
}

// profilesErrorBody is ProfilesErrorBody plus whether err was recognised, so a
// REST writer can log and answer its own generic 500 for the rest.
func profilesErrorBody(err error) (int, map[string]any, bool) {
	var guard *internalRuntime.BindingGuardError
	var req *requestError
	var notFound *internalRuntime.ProfileNotFoundError
	var exists *internalRuntime.ProfileExistsError
	var mismatch *internalRuntime.NameMismatchError
	var inUse *internalRuntime.ProfileInUseError
	var anon *internalRuntime.ProfileIsAnonymousError
	var val *internalRuntime.ValidationError
	var noCred *internalRuntime.NoClientCredentialError
	var pre *internalRuntime.PreconditionFailedError
	switch {
	case errors.As(err, &guard):
		bindings := make([]GuardBinding, 0, len(guard.Bindings))
		for _, b := range guard.Bindings {
			bindings = append(bindings, GuardBinding{ClientID: b.ClientID, TokenName: b.TokenName, Profile: b.Profile, Mode: b.Mode})
		}
		fixes := make([]GuardFixOption, 0, len(guard.Fixes))
		for _, f := range guard.Fixes {
			fixes = append(fixes, GuardFixOption{Kind: f.Kind, Target: f.Target})
		}
		body := errorBody(guard.Error(), profile.ErrorCodeBindingBypassable, "")
		body["bindings"] = bindings
		body["fixes"] = fixes
		return http.StatusConflict, body, true
	case errors.As(err, &req):
		return req.status, errorBody(req.msg, "", ""), true
	case errors.As(err, &notFound), errors.Is(err, profile.ErrUnknownProfile):
		return http.StatusNotFound, errorBody(errProfileNotFound, "", ""), true
	case errors.As(err, &exists):
		return http.StatusConflict, errorBody(exists.Error(), exists.Code(), ""), true
	case errors.As(err, &mismatch):
		return http.StatusConflict, errorBody(mismatch.Error(), mismatch.Code(), ""), true
	case errors.As(err, &inUse):
		body := errorBody(inUse.Error(), inUse.Code(), "")
		body["used_by"] = inUse.UsedBy
		return http.StatusConflict, body, true
	case errors.As(err, &anon):
		body := errorBody(anon.Error(), anon.Code(), "")
		body["used_by"] = anon.UsedBy
		return http.StatusConflict, body, true
	case errors.As(err, &val):
		return http.StatusBadRequest, errorBody(val.Message, "", val.Field), true
	case errors.As(err, &noCred):
		return http.StatusConflict, errorBody(noCred.Error(), noCred.Code(), ""), true
	case errors.As(err, &pre):
		return http.StatusConflict, errorBody(pre.Error(), pre.Code(), ""), true
	case errors.Is(err, profile.ErrUnknownClient):
		return http.StatusNotFound, errorBody(errClientNotFound, "", ""), true
	case errors.Is(err, profile.ErrUnknownToken):
		return http.StatusNotFound, errorBody("token not found", "", ""), true
	case errors.Is(err, profile.ErrClientCredentialToken), errors.Is(err, profile.ErrExplainBuiltinTool):
		return http.StatusBadRequest, errorBody(err.Error(), "", ""), true
	case errors.Is(err, connect.ErrNoCredentialMinter):
		return http.StatusServiceUnavailable, errorBody(err.Error(), "", ""), true
	case errors.Is(err, internalRuntime.ErrEvaluatorUnavailable), errors.Is(err, errProfilesUnavailable):
		return http.StatusServiceUnavailable, errorBody("profiles service unavailable", "", ""), true
	case errors.Is(err, internalRuntime.ErrConfigUnavailable):
		return http.StatusInternalServerError, errorBody("Configuration unavailable", "", ""), true
	}
	return 0, nil, false
}

// writeProfilesError answers err with its ProfilesErrorBody plus the REST
// envelope and reports whether it recognised err.
func (s *Server) writeProfilesError(w http.ResponseWriter, r *http.Request, err error) bool {
	status, body, known := profilesErrorBody(err)
	if !known {
		return false
	}
	body["success"] = false
	if id := reqcontext.GetRequestID(r.Context()); id != "" {
		body["request_id"] = id
	}
	s.writeJSON(w, status, body)
	return true
}
