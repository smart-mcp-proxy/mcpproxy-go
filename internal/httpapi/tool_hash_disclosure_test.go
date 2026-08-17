package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// T020 — hash-pin authoring surface (Spec 098 FR-011).
//
// The per-tool REST payload carries the tool's current hash rendered as the
// preflight pin format "sha256/v{N}:{hex}" so an operator can author
// `--pin id=<hash>` without a second endpoint. Disclosure is operator-tier
// only: the agent-token tier must never see a hash, on any tool endpoint —
// otherwise an agent can fingerprint tools it is not scoped to and detect
// upstream drift the operator has not published to it.

const toolHashTestAPIKey = "tool-hash-test-api-key"

// toolHashMgmtService is the management service the per-server tools endpoint
// prefers; it serves a fixed tool set for one server.
type toolHashMgmtService struct {
	tools map[string][]map[string]interface{}
}

func (m *toolHashMgmtService) GetServerTools(_ context.Context, name string) ([]map[string]interface{}, error) {
	return m.tools[name], nil
}

// toolHashController drives both tool listing endpoints with a real API key
// configured so the auth middleware runs (agent tokens need it).
type toolHashController struct {
	globalToolsController
	mgmt *toolHashMgmtService
}

func (c *toolHashController) GetCurrentConfig() interface{} {
	return &config.Config{APIKey: toolHashTestAPIKey}
}

func (c *toolHashController) GetManagementService() interface{} {
	if c.mgmt == nil {
		return nil
	}
	return c.mgmt
}

func newToolHashController() *toolHashController {
	tools := map[string][]map[string]interface{}{
		"github": {
			{"name": "create_issue", "description": "Create issue"},
			{"name": "no_record", "description": "Never approved"},
		},
	}
	ctrl := &toolHashController{
		globalToolsController: globalToolsController{
			allServers:  []map[string]interface{}{{"name": "github"}},
			serverTools: tools,
			approvals: map[string]*storage.ToolApprovalRecord{
				"github\x00create_issue": {
					Status:            storage.ToolApprovalStatusApproved,
					CurrentHash:       "abc123",
					HashSchemaVersion: 3,
				},
			},
		},
		mgmt: &toolHashMgmtService{tools: tools},
	}
	return ctrl
}

// newToolHashAgentToken registers a valid agent token on srv and returns it.
func newToolHashAgentToken(t *testing.T, srv *Server) string {
	t.Helper()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)
	srv.SetTokenStore(&testTokenStore{
		validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
			if token != rawToken {
				return nil, fmt.Errorf("token not found")
			}
			return &auth.AgentToken{
				Name:           "cron",
				TokenPrefix:    auth.TokenPrefix(rawToken),
				AllowedServers: []string{"*"},
				Permissions:    []string{auth.PermRead},
				ExpiresAt:      time.Now().Add(time.Hour),
			}, nil
		},
	}, tmpDir)
	return rawToken
}

// fetchTools calls a tool-listing endpoint with the given credential and
// returns the tools keyed by name.
func fetchTools(t *testing.T, srv *Server, path, token string) map[string]map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-API-Key", token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Data struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	byName := map[string]map[string]interface{}{}
	for _, tool := range resp.Data.Tools {
		name, _ := tool["name"].(string)
		byName[name] = tool
	}
	return byName
}

func TestToolHash_OperatorTierSeesPin(t *testing.T) {
	for _, path := range []string{"/api/v1/tools", "/api/v1/servers/github/tools"} {
		t.Run(path, func(t *testing.T) {
			srv := NewServer(newToolHashController(), zaptest.NewLogger(t).Sugar(), nil)
			tools := fetchTools(t, srv, path, toolHashTestAPIKey)

			require.Contains(t, tools, "create_issue")
			assert.Equal(t, "sha256/v3:abc123", tools["create_issue"]["hash"],
				"operator tier gets the current hash in pin format")

			// A tool with no approval record has no hash to publish; the field
			// must be absent rather than an empty/garbage pin.
			require.Contains(t, tools, "no_record")
			assert.NotContains(t, tools["no_record"], "hash")
		})
	}
}

func TestToolHash_NeverDisclosedToAgentToken(t *testing.T) {
	for _, path := range []string{"/api/v1/tools", "/api/v1/servers/github/tools"} {
		t.Run(path, func(t *testing.T) {
			srv := NewServer(newToolHashController(), zaptest.NewLogger(t).Sugar(), nil)
			token := newToolHashAgentToken(t, srv)

			tools := fetchTools(t, srv, path, token)

			require.Contains(t, tools, "create_issue", "the agent still sees the tool itself")
			assert.NotContains(t, tools["create_issue"], "hash",
				"agent-token tier must never receive a hash pin")
			// The rest of the enrichment is unchanged for agents.
			assert.Equal(t, storage.ToolApprovalStatusApproved, tools["create_issue"]["approval_status"])
		})
	}
}

// Spec 099 FR-018a: a request that reached the handler with NO auth context —
// the middleware's no-config passthrough is the only way in — is not evidence of
// an admin, so it gets the agent-token tier and no pin. This is the second
// consumer of disclosureTier, and the one where a residual grant would publish
// hashes rather than merely widen a diagnosis.
func TestToolHash_NoAuthContextGetsNoPin(t *testing.T) {
	ctrl := &unconfiguredToolHashController{toolHashController: newToolHashController()}
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

	tools := fetchTools(t, srv, "/api/v1/tools", "")
	require.Contains(t, tools, "create_issue")
	assert.NotContains(t, tools["create_issue"], "hash",
		"no auth context is the disclosure floor, not the ceiling")
}

// unconfiguredToolHashController reproduces the ONE middleware path that reaches
// a handler without installing an auth context: no readable config.
type unconfiguredToolHashController struct {
	*toolHashController
}

func (c *unconfiguredToolHashController) GetCurrentConfig() interface{} { return nil }

// The hash is proxy state, never upstream-supplied: a server declaring a tool
// field literally named "hash" must not be able to publish a pin through the
// listing (it would let a malicious upstream pin itself to a value the operator
// never approved, and would leak a pin to the agent tier).
func TestToolHash_UpstreamSuppliedHashIsNotReflected(t *testing.T) {
	ctrl := newToolHashController()
	ctrl.serverTools["github"] = []map[string]interface{}{
		{"name": "no_record", "description": "Never approved", "hash": "sha256/v3:spoofed"},
	}
	ctrl.mgmt.tools = ctrl.serverTools
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

	tools := fetchTools(t, srv, "/api/v1/tools", toolHashTestAPIKey)
	require.Contains(t, tools, "no_record")
	assert.NotContains(t, tools["no_record"], "hash")
}

// A record whose hash is empty (pre-hash record) must not render a "sha256/v0:"
// placeholder pin.
func TestToolHash_EmptyStoredHashIsOmitted(t *testing.T) {
	ctrl := newToolHashController()
	ctrl.approvals["github\x00create_issue"] = &storage.ToolApprovalRecord{
		Status: storage.ToolApprovalStatusApproved,
	}
	srv := NewServer(ctrl, zaptest.NewLogger(t).Sugar(), nil)

	tools := fetchTools(t, srv, "/api/v1/tools", toolHashTestAPIKey)
	require.Contains(t, tools, "create_issue")
	assert.NotContains(t, tools["create_issue"], "hash")
}
