package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
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

// reviewedIncarnationController is the optional capability that carries both
// bindings on a review-bound tool approval (Spec 116 + Spec 117 PR-2).
type reviewedIncarnationController interface {
	ApproveToolsReviewedAt(serverName string, toolNames []string, expected map[string]string, approvedBy string, expectedIncarnation int64) (int, error)
	ApproveAllToolsReviewedAt(serverName string, expected map[string]string, approvedBy string, expectedIncarnation int64) (int, error)
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
	case errors.Is(err, toolreview.ErrReviewNotRecorded):
		// Spec 117 FR-018: the server's latest capture is incomplete; the
		// request can succeed once a complete capture is applied.
		s.writeError(w, r, http.StatusServiceUnavailable, err.Error())
	default:
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("%s: %v", prefix, err))
	}
}

// serverIncarnationReader is the optional controller capability that reports
// a server's committed incarnation. *server.Server implements it.
type serverIncarnationReader interface {
	ServerIncarnation(serverName string) (int64, bool)
}

// serverIncarnationOf returns name's committed incarnation, or 0 when the
// controller cannot tell.
func (s *Server) serverIncarnationOf(name string) int64 {
	if rd, ok := s.controller.(serverIncarnationReader); ok {
		if inc, found := rd.ServerIncarnation(name); found {
			return inc
		}
	}
	return 0
}

// incarnationField reads the "incarnation" of a GetAllServers row (int64 from
// the runtime, float64 after a JSON round trip); 0 when absent.
func incarnationField(row map[string]interface{}) int64 {
	switch v := row["incarnation"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// incarnationReadAttempts bounds how often a listing re-reads rows whose
// server identity moved while they were read.
const incarnationReadAttempts = 3

// errIncarnationUnsettled is returned when a server kept being removed or
// replaced while its rows were read; the handler answers 409.
var errIncarnationUnsettled = errors.New("server_replaced: the server was removed or replaced while it was read; retry")

// readAtSettledIncarnation runs read between two reads of name's committed
// incarnation and returns the incarnation the rows belong to (Spec 116 PR2
// review round 7, FR-017). The first read alone (round 1) stamps rows with
// an incarnation no newer than their server, but when it saw the name ABSENT
// a same-name replacement landing before read returns the replacement's rows
// with no stamp at all, and the Web UI then sends no expected_incarnation, so
// a decision binds to whichever server exists when the operator clicks. The
// rows are therefore returned only when the identity did not move across the
// read; otherwise they are read again, and errIncarnationUnsettled is
// returned when it never settles.
func (s *Server) readAtSettledIncarnation(name string, read func() error) (int64, error) {
	for i := 0; i < incarnationReadAttempts; i++ {
		before := s.serverIncarnationOf(name)
		if err := read(); err != nil {
			return before, err
		}
		if s.serverIncarnationOf(name) == before {
			return before, nil
		}
	}
	return 0, errIncarnationUnsettled
}

// serverRowAt returns name's GetAllServers row when it carries incarnation
// inc. found is false when name is absent or its row names another
// incarnation (the identity moved again). Spec 116 PR2 review round 8: the
// global tools listing re-reads the row whose quarantine exclusion applies
// once its tool read settled on a replacement incarnation.
func (s *Server) serverRowAt(name string, inc int64) (row map[string]interface{}, found bool, err error) {
	rows, err := s.controller.GetAllServers()
	if err != nil {
		return nil, false, err
	}
	for _, r := range rows {
		if n, _ := r["name"].(string); n == name {
			if incarnationField(r) != inc {
				return nil, false, nil
			}
			return r, true, nil
		}
	}
	return nil, false, nil
}
