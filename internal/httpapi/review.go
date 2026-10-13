package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

type reviewReader interface {
	GetReviewQueue(ctx context.Context) (*internalRuntime.ReviewQueue, error)
	GetServerReview(ctx context.Context, serverName string) (*internalRuntime.ServerReview, error)
}

// handleGetReviewQueue godoc
// @Summary Get the server review queue
// @Description Returns one row per quarantined server or trusted server with pending or changed tools. Scoped callers see only servers they may enumerate.
// @Tags review
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} contracts.SuccessResponse "Review queue"
// @Failure 500 {object} contracts.ErrorResponse "Failed to load review queue"
// @Router /api/v1/review [get]
func (s *Server) handleGetReviewQueue(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.controller.(reviewReader)
	if !ok {
		s.writeError(w, r, http.StatusServiceUnavailable, "Review service unavailable")
		return
	}
	queue, err := reader.GetReviewQueue(r.Context())
	if err != nil {
		s.logger.Errorw("Failed to load review queue", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to load review queue")
		return
	}
	if queue == nil {
		queue = &internalRuntime.ReviewQueue{Servers: []internalRuntime.ReviewQueueRow{}}
	}
	if auth.IsScopedCaller(r.Context()) {
		visible := make([]internalRuntime.ReviewQueueRow, 0, len(queue.Servers))
		for _, row := range queue.Servers {
			if canSeeServer(r.Context(), row.Server) {
				visible = append(visible, row)
			}
		}
		queue = &internalRuntime.ReviewQueue{Count: len(visible), Servers: visible}
	}
	s.writeSuccess(w, queue)
}

// handleGetServerReview godoc
// @Summary Get review details for a server
// @Description Returns redacted server configuration, captured tool definitions, annotations, scan verdicts, and diffs. Scoped callers receive the same 404 for invisible and missing servers.
// @Tags review
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param id path string true "Server name"
// @Success 200 {object} contracts.SuccessResponse "Server review"
// @Failure 404 {object} contracts.ErrorResponse "Server not found"
// @Failure 500 {object} contracts.ErrorResponse "Failed to load server review"
// @Router /api/v1/servers/{id}/review [get]
func (s *Server) handleGetServerReview(w http.ResponseWriter, r *http.Request) {
	serverName := chi.URLParam(r, "id")
	if serverName == "" {
		s.writeError(w, r, http.StatusBadRequest, "Server ID required")
		return
	}
	reader, ok := s.controller.(reviewReader)
	if !ok {
		s.writeError(w, r, http.StatusServiceUnavailable, "Review service unavailable")
		return
	}
	review, err := reader.GetServerReview(r.Context(), serverName)
	if err != nil {
		if errors.Is(err, internalRuntime.ErrReviewServerNotFound) {
			s.writeError(w, r, http.StatusNotFound, "Server not found: "+serverName)
			return
		}
		s.logger.Errorw("Failed to load server review", "server", serverName, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to load server review")
		return
	}
	s.writeSuccess(w, review)
}
