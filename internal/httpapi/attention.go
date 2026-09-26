package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// attentionResponse is the GET /api/v1/attention success `data` payload
// (contracts/rest-api.md#attention).
type attentionResponse struct {
	Count       int                       `json:"count"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Items       []contracts.AttentionItem `json:"items"`
}

// handleGetAttention godoc
// @Summary Get the needs-attention list
// @Description One list, one count, every surface (Web UI, macOS tray/Home, CLI) reads from it. Filtered per caller: an administrator sees every item; a scoped caller (agent token, or a non-admin server-edition OAuth user session) sees only items whose server it may enumerate, and never a client item.
// @Tags attention
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} attentionResponse "The needs-attention list"
// @Router /api/v1/attention [get]
// handleGetAttention serves the one needs-attention list (Spec 109 FR-001).
// Both editions register this route. Rule: filtered (contracts/rest-api.md#attention,
// FR-007) — an administrator (or a request with no AuthContext at all) sees
// every item; a scoped caller (agent token, or a non-admin server-edition
// OAuth user session) sees only items whose subject is a server it may
// enumerate, and no client item at all.
func (s *Server) handleGetAttention(w http.ResponseWriter, r *http.Request) {
	items := s.controller.Attention()
	items = filterAttentionItems(r.Context(), items)
	if items == nil {
		items = []contracts.AttentionItem{}
	}
	s.writeSuccess(w, attentionResponse{
		Count:       len(items),
		GeneratedAt: time.Now().UTC(),
		Items:       items,
	})
}

// renderAttentionChangedForCaller narrows the runtime attention.changed
// event's structured item list to `{count, ids}` for one SSE subscriber
// (contracts/rest-api.md#attention, FR-006): an administrator gets every id;
// a scoped caller gets only ids whose subject is a server it can see, never a
// client id, with count recomputed from the narrowed list. Per-connection
// "unchanged since last frame" suppression is handled by the caller
// (handleSSEEvents), which is where per-connection state lives.
func renderAttentionChangedForCaller(ctx context.Context, payload map[string]interface{}) map[string]interface{} {
	items, _ := payload["items"].([]internalRuntime.AttentionEventItem)
	if !auth.IsScopedCaller(ctx) {
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		return map[string]interface{}{"count": len(items), "ids": ids}
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if it.SubjectType == "client" {
			continue
		}
		if it.SubjectType == "server" && !canSeeServer(ctx, it.SubjectID) {
			continue
		}
		ids = append(ids, it.ID)
	}
	return map[string]interface{}{"count": len(ids), "ids": ids}
}

// attentionIDSetsEqual reports whether two rendered attention.changed id sets
// are identical, used by handleSSEEvents to suppress a frame this subscriber
// has already effectively seen (FR-006).
func attentionIDSetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// filterAttentionItems narrows an attention list to what ctx's caller may
// see (FR-007): `auth.IsScopedCaller`, never `Type == AuthTypeAgent`. An
// admin, or a request carrying no AuthContext at all, gets every item.
func filterAttentionItems(ctx context.Context, items []contracts.AttentionItem) []contracts.AttentionItem {
	if !auth.IsScopedCaller(ctx) {
		return items
	}
	out := make([]contracts.AttentionItem, 0, len(items))
	for _, it := range items {
		switch it.Subject.Type {
		case "client":
			// Scoped callers never see client items (data-model.md §4, FR-007).
			continue
		case "server":
			if !canSeeServer(ctx, it.Subject.ID) {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}
