package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 116 PR2 (FR-017): approval endpoints accept an optional
// "expected_incarnation", the incarnation of the server the client reviewed
// (GET /api/v1/servers[/{id}] and the review payload carry "incarnation").
// When the server was removed and re-added since, the write is refused with
// 409 "server_replaced: …" and nothing is written. When the field is absent,
// the runtime binds the approval to the incarnation current when the request
// started.

// incarnationApprovalController is the optional controller capability that
// binds approval writes to an expected incarnation. *server.Server implements
// it; a controller without it gets the unbound calls.
type incarnationApprovalController interface {
	ApproveToolsAt(serverName string, toolNames []string, approvedBy string, expectedIncarnation int64) error
	ApproveAllToolsAt(serverName string, approvedBy string, expectedIncarnation int64) (int, error)
	BlockToolsAt(serverName string, toolNames []string, blockedBy string, expectedIncarnation int64) (int, error)
	BlockAllToolsAt(serverName string, blockedBy string, expectedIncarnation int64) (int, error)
	SetToolEnabledAt(serverName, toolName string, enabled bool, updatedBy string, expectedIncarnation int64) error
	SetAllToolsEnabledAt(serverName string, enabled bool, updatedBy string, expectedIncarnation int64) (int, error)
	QuarantineServerAt(serverName string, quarantined bool, expectedIncarnation int64) error
}

// incarnationController returns the controller's incarnation capability, or
// nil when it has none.
func (s *Server) incarnationController() incarnationApprovalController {
	c, _ := s.controller.(incarnationApprovalController)
	return c
}

// expectedIncarnationBody is the optional body of the endpoints that took
// none before (unquarantine, enable_all / disable_all).
type expectedIncarnationBody struct {
	ExpectedIncarnation int64 `json:"expected_incarnation,omitempty"`
}

// readOptionalExpectedIncarnation decodes {"expected_incarnation": n} from an
// optional request body. An empty body is 0 (unbound).
func readOptionalExpectedIncarnation(r *http.Request) (int64, error) {
	if r.Body == nil {
		return 0, nil
	}
	var body expectedIncarnationBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if body.ExpectedIncarnation < 0 {
		return 0, fmt.Errorf("expected_incarnation must not be negative")
	}
	return body.ExpectedIncarnation, nil
}

// writeApprovalWriteError maps an approval writer's error: 409
// server_replaced when the server was removed or replaced since the reviewed
// incarnation, 404 when it is not configured, 500 otherwise (with prefix).
func (s *Server) writeApprovalWriteError(w http.ResponseWriter, r *http.Request, err error, prefix string) {
	var replaced *commitview.ServerReplacedError
	switch {
	case errors.As(err, &replaced):
		s.writeError(w, r, http.StatusConflict, replaced.Error())
	case errors.Is(err, commitview.ErrServerReplaced):
		s.writeError(w, r, http.StatusConflict, "server_replaced: the server was removed or replaced since it was reviewed")
	case errors.Is(err, internalRuntime.ErrServerNotFound):
		s.writeError(w, r, http.StatusNotFound, err.Error())
	default:
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("%s: %v", prefix, err))
	}
}
