package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// ARC-04: the management service must reach the REST handlers through a
// COMPILE-TIME contract, not as an interface{} each handler re-derives with an
// ad-hoc anonymous type assertion. Twelve such assertions existed; five were
// unchecked and turned any method-set drift into a recovered panic surfacing as
// an opaque 500 with no log line naming the cause.
//
// This is the defect fix itself: it cannot compile while
// ServerController.GetManagementService() returns interface{}.
var _ = func(c ServerController) management.Service { return c.GetManagementService() }

// fullMgmtService embeds management.Service so it satisfies the whole interface
// while implementing only the methods these routes drive. The embedded
// interface is nil, so an unexpected call panics loudly — the correct failure
// for a test reaching a method it never meant to exercise.
type fullMgmtService struct {
	management.Service
	calls map[string]int
}

func (m *fullMgmtService) record(name string) {
	if m.calls == nil {
		m.calls = map[string]int{}
	}
	m.calls[name]++
}

func (m *fullMgmtService) RestartAll(context.Context) (*management.BulkOperationResult, error) {
	m.record("RestartAll")
	return &management.BulkOperationResult{Total: 2, Successful: 2}, nil
}

func (m *fullMgmtService) EnableAll(context.Context) (*management.BulkOperationResult, error) {
	m.record("EnableAll")
	return &management.BulkOperationResult{Total: 2, Successful: 2}, nil
}

func (m *fullMgmtService) DisableAll(context.Context) (*management.BulkOperationResult, error) {
	m.record("DisableAll")
	return &management.BulkOperationResult{Total: 2, Successful: 2}, nil
}

func (m *fullMgmtService) TriggerOAuthLoginQuick(_ context.Context, name string) (*core.OAuthStartResult, error) {
	m.record("TriggerOAuthLoginQuick")
	return &core.OAuthStartResult{AuthURL: "https://example.test/authorize?server=" + name, BrowserOpened: true}, nil
}

func (m *fullMgmtService) TriggerOAuthLogout(context.Context, string) error {
	m.record("TriggerOAuthLogout")
	return nil
}

func (m *fullMgmtService) GetServerTools(context.Context, string) ([]map[string]interface{}, error) {
	m.record("GetServerTools")
	return []map[string]interface{}{{"name": "alpha", "description": "a tool"}}, nil
}

const mgmtContractAPIKey = "mgmt-contract-admin-key"

// typedMgmtController hands the server a real management.Service through the
// controller seam.
type typedMgmtController struct {
	baseController
	svc *fullMgmtService
}

func (c *typedMgmtController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: mgmtContractAPIKey}
}

func (c *typedMgmtController) GetManagementService() management.Service { return c.svc }

// TestManagementHandlers_RouteThroughTypedService drives every management-only
// route and asserts each reaches the service and returns its success code —
// never the 500 "Management service not available" the ad-hoc assertions
// produced when a method was missing from the anonymous interface.
func TestManagementHandlers_RouteThroughTypedService(t *testing.T) {
	svc := &fullMgmtService{}
	srv := NewServer(&typedMgmtController{svc: svc}, zap.NewNop().Sugar(), nil)

	cases := []struct {
		name   string
		method string
		path   string
		expect string // management.Service method the route must reach
	}{
		{"restart_all", http.MethodPost, "/api/v1/servers/restart_all", "RestartAll"},
		{"enable_all", http.MethodPost, "/api/v1/servers/enable_all", "EnableAll"},
		{"disable_all", http.MethodPost, "/api/v1/servers/disable_all", "DisableAll"},
		{"login", http.MethodPost, "/api/v1/servers/github/login", "TriggerOAuthLoginQuick"},
		{"logout", http.MethodPost, "/api/v1/servers/github/logout", "TriggerOAuthLogout"},
		{"tools", http.MethodGet, "/api/v1/servers/github/tools", "GetServerTools"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("X-API-Key", mgmtContractAPIKey)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code,
				"%s %s should reach the management service; body=%s", tc.method, tc.path, w.Body.String())
			assert.NotContains(t, w.Body.String(), "Management service not available",
				"the typed seam must not report the service missing")
			assert.Equal(t, 1, svc.calls[tc.expect],
				fmt.Sprintf("route %s must call management.Service.%s exactly once", tc.path, tc.expect))
		})
	}
}
