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
// @Description One list, one count, every surface (Web UI, macOS tray/Home, CLI) reads from it. Filtered per caller: an administrator sees every item; a scoped caller (agent token, or a non-admin server-edition OAuth user session) sees only server items whose server it may enumerate; every other subject type (client, setting) is administrator-only.
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
// OAuth user session) sees only server items whose server it may
// enumerate; every other subject type is withheld (allow-list).
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
// client or setting id, with count recomputed from the narrowed list. Per-connection
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
		if !scopedCallerMaySeeAttention(ctx, it.SubjectType, it.SubjectID) {
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
		if scopedCallerMaySeeAttention(ctx, it.Subject.Type, it.Subject.ID) {
			out = append(out, it)
		}
	}
	return out
}

// scopedCallerMaySeeAttention is the ALLOW-list for a scoped caller (FR-007,
// Spec 109-l P4): only a server item whose server the caller may enumerate.
// Every other subject type (client, setting, and any type a later spec adds)
// is administrator-only and fails closed, so a new kind can never leak to an
// agent token or a tenant by being forgotten here.
func scopedCallerMaySeeAttention(ctx context.Context, subjectType, subjectID string) bool {
	return subjectType == "server" && canSeeServer(ctx, subjectID)
}
