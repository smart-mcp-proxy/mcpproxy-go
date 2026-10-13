package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 109-l P11 / issue #1437 item 1: GET /servers?profile=<viewed> by a
// caller pinned to a narrower profile must agree with GET /tools?profile=<viewed>
// for the SAME caller: a server outside the caller's own pin disappears, and a
// row's tool_count counts only tools both profiles admit.

var callerPinTools = map[string][]string{
	"github":     {"read_issue", "write_issue"},
	"notion":     {"read_page", "read_db"},
	"filesystem": {"write_file"},
}

type allowAllEvaluator struct{}

func (allowAllEvaluator) Evaluate(_, _ string) profile.AccessVerdict {
	return profile.AccessVerdict{Visible: true, Callable: true}
}

type callerPinMgmt struct {
	*scopeMgmtService
}

func (callerPinMgmt) GetServerTools(_ context.Context, name string) ([]map[string]interface{}, error) {
	return callerPinToolRows(name), nil
}

func callerPinToolRows(name string) []map[string]interface{} {
	var rows []map[string]interface{}
	for _, tool := range callerPinTools[name] {
		rows = append(rows, map[string]interface{}{"name": tool, "server_name": name, "description": tool})
	}
	return rows
}

// callerPinController serves three servers and a caller pinned to
// work-readonly (github read_* tools only).
type callerPinController struct {
	scopeController
}

func (c *callerPinController) GetManagementService() management.Service {
	if !c.withManagement {
		return nil
	}
	return callerPinMgmt{&scopeMgmtService{servers: c.servers}}
}

func (c *callerPinController) GetServerTools(name string) ([]map[string]interface{}, error) {
	return callerPinToolRows(name), nil
}

func (c *callerPinController) ResolveViewAs(profile.AccessSubject) (ViewAsEvaluator, error) {
	return allowAllEvaluator{}, nil
}

func (c *callerPinController) ToolAllowedByProfile(ctx context.Context, serverName, toolName string) bool {
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || ac.IsAdmin() || ac.ProfilePin == "" {
		return true
	}
	return serverName == "github" && strings.HasPrefix(toolName, "read_")
}

func (c *callerPinController) SearchToolsForProfile(context.Context, string, int, func(string) bool) ([]map[string]interface{}, bool, error) {
	return nil, false, nil
}

func callerPinServer(t *testing.T, withManagement bool) (*Server, string) {
	t.Helper()
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
		APIKey: scopeAdminAPIKey,
		Servers: []*config.ServerConfig{
			{Name: "github", Enabled: true}, {Name: "notion", Enabled: true}, {Name: "filesystem", Enabled: true},
		},
		Profiles: []config.ProfileConfig{
			{Name: "work-readonly", Servers: []string{"github"}},
			{Name: "work-full", Servers: []string{"github", "notion", "filesystem"}},
		},
	}
	servers := []contracts.Server{
		{ID: "github", Name: "github", Enabled: true, Connected: true, ToolCount: 2},
		{ID: "notion", Name: "notion", Enabled: true, Connected: true, ToolCount: 2},
		{ID: "filesystem", Name: "filesystem", Enabled: true, Connected: true, ToolCount: 1},
	}
	ctrl := &callerPinController{scopeController: scopeController{cfg: cfg, servers: servers, withManagement: withManagement}}

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)
	tmpDir := t.TempDir()
	_, err = auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	agent := &auth.AgentToken{
		Name: "ro-bot", TokenPrefix: auth.TokenPrefix(rawToken), AllowedServers: []string{"*"},
		Permissions: []string{auth.PermRead}, ProfilePin: "work-readonly",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	store := &testTokenStore{validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
		if token == rawToken {
			return agent, nil
		}
		return nil, fmt.Errorf("token not found")
	}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(store, tmpDir)
	return srv, rawToken
}

func callerPinGet(t *testing.T, srv *Server, path, key string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var env struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return env.Data
}

func serverRows(data map[string]interface{}) map[string]float64 {
	out := map[string]float64{}
	rows, _ := data["servers"].([]interface{})
	for _, r := range rows {
		m := r.(map[string]interface{})
		count, _ := m["tool_count"].(float64)
		out[m["name"].(string)] = count
	}
	return out
}

func TestServersProfileCallerPin(t *testing.T) {
	for _, branch := range []struct {
		name           string
		withManagement bool
	}{{"management service", true}, {"legacy fallback", false}} {
		t.Run(branch.name, func(t *testing.T) {
			srv, token := callerPinServer(t, branch.withManagement)

			rows := serverRows(callerPinGet(t, srv, "/api/v1/servers?profile=work-full", token))
			assert.Equal(t, map[string]float64{"github": 1}, rows,
				"notion and filesystem are outside the caller's own pin; github counts only read_* tools")

			// /servers and /tools must agree for the same caller.
			tools := callerPinGet(t, srv, "/api/v1/tools?profile=work-full", token)
			githubRows := 0
			for _, tr := range tools["tools"].([]interface{}) {
				if tr.(map[string]interface{})["server"] == "github" || tr.(map[string]interface{})["server_name"] == "github" {
					githubRows++
				}
			}
			assert.Equal(t, githubRows, int(rows["github"]), "tool_count equals the GET /tools github row count")

			stats := callerPinGet(t, srv, "/api/v1/servers?profile=work-full", token)["stats"].(map[string]interface{})
			assert.Equal(t, float64(1), stats["total_servers"])
			assert.Equal(t, float64(1), stats["total_tools"])

			// The administrator path is unchanged: every viewed-profile server and tool.
			admin := serverRows(callerPinGet(t, srv, "/api/v1/servers?profile=work-full", scopeAdminAPIKey))
			assert.Equal(t, map[string]float64{"github": 2, "notion": 2, "filesystem": 1}, admin)
		})
	}
}
