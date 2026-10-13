package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

type reviewControllerMock struct {
	mockActivityController
	queue   *internalRuntime.ReviewQueue
	servers []map[string]interface{}
}

func (m *reviewControllerMock) GetReviewQueue(context.Context) (*internalRuntime.ReviewQueue, error) {
	return m.queue, nil
}

func (m *reviewControllerMock) GetServerReview(_ context.Context, name string) (*internalRuntime.ServerReview, error) {
	return &internalRuntime.ServerReview{Server: internalRuntime.ReviewServer{Name: name}, Tools: []internalRuntime.ReviewTool{}}, nil
}

func (m *reviewControllerMock) GetAllServers() ([]map[string]interface{}, error) {
	return m.servers, nil
}

func TestReviewQueueFiltersAgentAndUserScopesAndRecounts(t *testing.T) {
	controller := &reviewControllerMock{queue: &internalRuntime.ReviewQueue{
		Count:   2,
		Servers: []internalRuntime.ReviewQueueRow{{Server: "alpha", Kind: "tool_review"}, {Server: "beta", Kind: "server_review"}},
	}}
	srv := NewServer(controller, zap.NewNop().Sugar(), nil)

	tests := []struct {
		name       string
		caller     *auth.AuthContext
		wantCount  int
		wantServer string
	}{
		{name: "admin", caller: auth.AdminContext(), wantCount: 2, wantServer: "alpha"},
		{name: "agent scope", caller: &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}}, wantCount: 1, wantServer: "alpha"},
		{name: "user scope", caller: &auth.AuthContext{Type: auth.AuthTypeUser, AllowedServers: []string{"alpha"}}, wantCount: 1, wantServer: "alpha"},
		{name: "empty user scope", caller: &auth.AuthContext{Type: auth.AuthTypeUser}, wantCount: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/review", nil)
			req = req.WithContext(auth.WithAuthContext(req.Context(), tt.caller))
			rec := httptest.NewRecorder()
			srv.handleGetReviewQueue(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			var response struct {
				Data internalRuntime.ReviewQueue `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, tt.wantCount, response.Data.Count)
			require.Len(t, response.Data.Servers, tt.wantCount)
			if tt.wantCount > 0 {
				require.Equal(t, tt.wantServer, response.Data.Servers[0].Server)
			}
		})
	}
}

func TestServerReviewUsesScopedSubtreeGuard(t *testing.T) {
	controller := &reviewControllerMock{servers: []map[string]interface{}{{"name": "alpha"}, {"name": "beta"}}}
	srv := NewServer(controller, zap.NewNop().Sugar(), nil)
	mux := chi.NewRouter()
	mux.Route("/servers/{id}", func(r chi.Router) {
		r.Use(decodeServerIDParam)
		r.Use(srv.scopedServerSubtree)
		r.Get("/review", srv.handleGetServerReview)
	})

	req := httptest.NewRequest(http.MethodGet, "/servers/beta/review", nil)
	req = req.WithContext(auth.WithAuthContext(req.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "Server not found: beta")
	outOfScopeBody := rec.Body.String()

	req = httptest.NewRequest(http.MethodGet, "/servers/alpha/review", nil)
	req = req.WithContext(auth.WithAuthContext(req.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"name":"alpha"`)

	controller.servers = []map[string]interface{}{{"name": "alpha"}}
	req = httptest.NewRequest(http.MethodGet, "/servers/beta/review", nil)
	req = req.WithContext(auth.WithAuthContext(req.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha", "beta"}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, outOfScopeBody, rec.Body.String(), "out-of-scope and absent servers must have the same response")
}
