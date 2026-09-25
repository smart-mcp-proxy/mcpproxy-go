package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// usableServerTestController controls the server inventory and tool-approval
// fixtures computeUsableServers reads (Spec 109-b, T029/FR-041).
type usableServerTestController struct {
	mockRoutingController

	servers  []map[string]interface{}
	approved map[string][]*storage.ToolApprovalRecord // serverName -> records
}

func (m *usableServerTestController) GetAllServers() ([]map[string]interface{}, error) {
	return m.servers, nil
}

// ListToolApprovals mirrors storage.BoltDB.ListToolApprovals's documented
// contract: an empty serverName returns every record across every server,
// not just the entry keyed by "" — computeUsableServers relies on the
// aggregate form (one call, grouped in memory) rather than one call per
// candidate server.
func (m *usableServerTestController) ListToolApprovals(serverName string) ([]*storage.ToolApprovalRecord, error) {
	if serverName != "" {
		return m.approved[serverName], nil
	}
	var all []*storage.ToolApprovalRecord
	for _, records := range m.approved {
		all = append(all, records...)
	}
	return all, nil
}

func newUsableServerTestServer(t *testing.T, ctrl *usableServerTestController) *Server {
	t.Helper()
	ctrl.apiKey = "test-key"
	ctrl.routingMode = "retrieve_tools"
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func getOnboardingState(t *testing.T, srv *Server) OnboardingStateResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding/state", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var envelope struct {
		Data OnboardingStateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	return envelope.Data
}

func server(name string, enabled, quarantined, connected bool) map[string]interface{} {
	return map[string]interface{}{
		"name":        name,
		"enabled":     enabled,
		"quarantined": quarantined,
		"connected":   connected,
	}
}

func approvalRecord(server, tool, status string, disabled bool) *storage.ToolApprovalRecord {
	return &storage.ToolApprovalRecord{
		ServerName: server,
		ToolName:   tool,
		Status:     status,
		Disabled:   disabled,
	}
}

// TestHasUsableServer_FalseWhileAllServersUnusable is T029: has_usable_server
// stays false while every server is quarantined, disabled, disconnected, or
// has no approved tool.
func TestHasUsableServer_FalseWhileAllServersUnusable(t *testing.T) {
	cases := []struct {
		name     string
		servers  []map[string]interface{}
		approved map[string][]*storage.ToolApprovalRecord
	}{
		{
			name:    "quarantined",
			servers: []map[string]interface{}{server("github", true, true, true)},
			approved: map[string][]*storage.ToolApprovalRecord{
				"github": {approvalRecord("github", "create_issue", storage.ToolApprovalStatusApproved, false)},
			},
		},
		{
			name:    "disabled",
			servers: []map[string]interface{}{server("github", false, false, true)},
			approved: map[string][]*storage.ToolApprovalRecord{
				"github": {approvalRecord("github", "create_issue", storage.ToolApprovalStatusApproved, false)},
			},
		},
		{
			name:    "not connected",
			servers: []map[string]interface{}{server("github", true, false, false)},
			approved: map[string][]*storage.ToolApprovalRecord{
				"github": {approvalRecord("github", "create_issue", storage.ToolApprovalStatusApproved, false)},
			},
		},
		{
			name:     "no tool approvals at all",
			servers:  []map[string]interface{}{server("github", true, false, true)},
			approved: map[string][]*storage.ToolApprovalRecord{},
		},
		{
			name:    "only pending tools",
			servers: []map[string]interface{}{server("github", true, false, true)},
			approved: map[string][]*storage.ToolApprovalRecord{
				"github": {approvalRecord("github", "create_issue", storage.ToolApprovalStatusPending, false)},
			},
		},
		{
			name:    "approved but disabled tool",
			servers: []map[string]interface{}{server("github", true, false, true)},
			approved: map[string][]*storage.ToolApprovalRecord{
				"github": {approvalRecord("github", "create_issue", storage.ToolApprovalStatusApproved, true)},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &usableServerTestController{servers: tc.servers, approved: tc.approved}
			srv := newUsableServerTestServer(t, ctrl)

			resp := getOnboardingState(t, srv)
			assert.False(t, resp.HasUsableServer, "has_usable_server should be false")
			assert.Empty(t, resp.UsableServers)
		})
	}
}

// TestHasUsableServer_TrueAfterApprove asserts has_usable_server flips true
// once an enabled, non-quarantined, connected server has at least one
// approved, non-disabled tool, and usable_servers names it.
func TestHasUsableServer_TrueAfterApprove(t *testing.T) {
	ctrl := &usableServerTestController{
		servers: []map[string]interface{}{
			server("github", true, false, true),
			server("quarantined-one", true, true, true), // must not appear
		},
		approved: map[string][]*storage.ToolApprovalRecord{
			"github": {
				approvalRecord("github", "create_issue", storage.ToolApprovalStatusPending, false),
				approvalRecord("github", "list_issues", storage.ToolApprovalStatusApproved, false),
			},
			"quarantined-one": {
				approvalRecord("quarantined-one", "x", storage.ToolApprovalStatusApproved, false),
			},
		},
	}
	srv := newUsableServerTestServer(t, ctrl)

	resp := getOnboardingState(t, srv)
	assert.True(t, resp.HasUsableServer)
	assert.Equal(t, []string{"github"}, resp.UsableServers)
}

// TestIncompleteTabCount_UsesHasUsableServer asserts FR-041: the Servers
// step / Setup badge counts on has_usable_server, not has_configured_server —
// a quarantined-only inventory (servers exist, none usable) still counts as
// an incomplete tab.
func TestIncompleteTabCount_UsesHasUsableServer(t *testing.T) {
	ctrl := &usableServerTestController{
		servers: []map[string]interface{}{server("github", true, true, true)},
		approved: map[string][]*storage.ToolApprovalRecord{
			"github": {approvalRecord("github", "create_issue", storage.ToolApprovalStatusApproved, false)},
		},
	}
	srv := newUsableServerTestServer(t, ctrl)

	resp := getOnboardingState(t, srv)
	assert.True(t, resp.HasConfiguredServer, "precondition: a server entry exists")
	assert.False(t, resp.HasUsableServer)
	// +1 for no connected client, +1 for no usable server, +1 for no first-ever
	// MCP client — none of the three predicates are satisfied by this fixture.
	assert.Equal(t, 3, resp.IncompleteTabCount)
}

// TestRecordClientConnected_SetsClientConnectedAt is the connect-success half
// of T029: a successful POST /connect/{client} records the timestamp under
// state.client_connected_at through UpdateOnboardingState.
func TestRecordClientConnected_SetsClientConnectedAt(t *testing.T) {
	ctrl := &onboardingTestController{}
	srv := newOnboardingTestServer(t, ctrl)

	srv.recordClientConnected("cursor")

	require.NotNil(t, ctrl.state)
	require.Contains(t, ctrl.state.ClientConnectedAt, "cursor")
	assert.False(t, ctrl.state.ClientConnectedAt["cursor"].IsZero())
}
