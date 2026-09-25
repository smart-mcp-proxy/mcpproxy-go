package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
)

// internal/server/upstream_servers_health_status_test.go already proves the
// MCP `upstream_servers list` JSON path carries health.status/usable/actions
// (Spec 109 FR-010-012). GET /api/v1/servers shares the same contracts.Server
// struct but reaches the wire through a different handler
// (handleGetServers) with its own enrichment chain (quarantine stats,
// security-scan, secret redaction, then scope filtering) — none of which is
// exercised by the MCP-side test. This closes that gap: it proves the REST
// handler passes health.CalculateHealth's output through unmodified rather
// than dropping or transforming status/usable/actions along the way.

const restHealthVocabAPIKey = "rest-health-vocab-key"

type restHealthVocabController struct {
	baseController
	svc *restHealthVocabService
}

func (c *restHealthVocabController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: restHealthVocabAPIKey}
}

func (c *restHealthVocabController) GetManagementService() management.Service { return c.svc }

// restHealthVocabService embeds management.Service (nil) so it satisfies the
// interface while implementing only ListServers — the one method
// handleGetServers' management-service path calls.
type restHealthVocabService struct {
	management.Service
	server *contracts.Server
}

func (s *restHealthVocabService) ListServers(context.Context) ([]*contracts.Server, *contracts.ServerStats, error) {
	return []*contracts.Server{s.server}, &contracts.ServerStats{TotalServers: 1}, nil
}

func TestHandleGetServers_HealthCarriesStatusVocabulary(t *testing.T) {
	// A real calculator output (missing-secret branch), not a hand-rolled
	// JSON fixture — ties this test to the same source of truth the MCP-side
	// test exercises through the tool surface.
	computed := health.CalculateHealth(health.HealthCalculatorInput{
		Enabled:       true,
		MissingSecret: "GITHUB_TOKEN",
	}, nil)

	svc := &restHealthVocabService{server: &contracts.Server{
		ID:       "needs-secret",
		Name:     "needs-secret",
		Protocol: "http",
		Enabled:  true,
		Health:   computed,
	}}
	srv := NewServer(&restHealthVocabController{svc: svc}, zap.NewNop().Sugar(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/servers", http.NoBody)
	req.Header.Set("X-API-Key", restHealthVocabAPIKey)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Servers []struct {
				Name   string                 `json:"name"`
				Health contracts.HealthStatus `json:"health"`
			} `json:"servers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success, "body: %s", w.Body.String())
	require.Len(t, resp.Data.Servers, 1)

	got := resp.Data.Servers[0].Health
	assert.Equal(t, health.StatusNeedsSecret, got.Status)
	assert.False(t, got.Usable)
	assert.Equal(t, health.ActionSetSecret, got.Action)
	require.Len(t, got.Actions, 1)
	assert.Equal(t, got.Actions[0], got.Action, "action must equal actions[0]")
	assert.Equal(t, health.LevelUnhealthy, got.Level)

	// Legacy fields untouched by the REST enrichment chain.
	assert.Equal(t, "enabled", got.AdminState)
	assert.Equal(t, "Missing secret", got.Summary)
}
