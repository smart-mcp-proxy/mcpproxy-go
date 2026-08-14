package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// codeScriptsController serves the ACTIVE config file path, which is the sole
// authority for where stored scripts live (Spec 097).
type codeScriptsController struct {
	baseController
	apiKey     string
	configPath string
}

func (c *codeScriptsController) GetCurrentConfig() interface{} {
	return &config.Config{APIKey: c.apiKey}
}

func (c *codeScriptsController) GetConfigPath() string { return c.configPath }

// newCodeScriptsServer wires a server whose config file lives in a fresh temp
// directory and returns it with the scripts directory that implies.
func newCodeScriptsServer(t *testing.T, apiKey string) (*Server, string) {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "mcp_config.json")
	srv := NewServer(&codeScriptsController{apiKey: apiKey, configPath: configPath}, zap.NewNop().Sugar(), nil)
	return srv, filepath.Join(dir, codescripts.DirName)
}

func getCodeScripts(t *testing.T, srv *Server, apiKey string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/code/scripts", nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, req)
	return recorder
}

// TestHandleListScripts_ReportsEveryEntry (T008) pins the discovery contract:
// every token-valid script in the directory is listed with its status, so a
// user can see WHY a file they created is not invocable.
func TestHandleListScripts_ReportsEveryEntry(t *testing.T) {
	const apiKey = "test-code-scripts-key"
	srv, scriptsDir := newCodeScriptsServer(t, apiKey)
	require.NoError(t, os.MkdirAll(scriptsDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "alpha.js"), []byte("({a: 1})"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "typed.ts"), []byte("const a: number = 1"), 0o600))
	// Both extensions for one name: invocable by neither, reported as such.
	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "both.js"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "both.ts"), []byte("1"), 0o600))
	// Present but unusable.
	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "blank.js"), nil, 0o600))
	// Not a script at all.
	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "notes.txt"), []byte("hi"), 0o600))

	recorder := getCodeScripts(t, srv, apiKey)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Scripts []codescripts.Entry `json:"scripts"`
			Dir     string              `json:"dir"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	assert.Equal(t, scriptsDir, resp.Data.Dir, "the listing names the directory it read")

	byName := map[string]codescripts.Entry{}
	for _, entry := range resp.Data.Scripts {
		byName[entry.Name] = entry
	}
	require.Contains(t, byName, "alpha")
	assert.Equal(t, codescripts.StatusOK, byName["alpha"].Status)
	require.Len(t, byName["alpha"].Paths, 1)
	assert.True(t, strings.HasSuffix(byName["alpha"].Paths[0], "alpha.js"))

	require.Contains(t, byName, "typed")
	assert.Equal(t, codescripts.StatusOK, byName["typed"].Status)

	require.Contains(t, byName, "both")
	assert.Equal(t, codescripts.StatusAmbiguous, byName["both"].Status)
	assert.Len(t, byName["both"].Paths, 2)

	require.Contains(t, byName, "blank")
	assert.Equal(t, codescripts.StatusInvalid, byName["blank"].Status)
	assert.Equal(t, codescripts.ReasonEmpty, byName["blank"].Reason)

	assert.NotContains(t, byName, "notes", "only .js/.ts files are stored scripts")
}

// TestHandleListScripts_EmptyDirectory: a missing or empty scripts directory is
// an empty list, not an error — nothing is misconfigured about having none.
func TestHandleListScripts_EmptyDirectory(t *testing.T) {
	const apiKey = "test-code-scripts-key"
	srv, scriptsDir := newCodeScriptsServer(t, apiKey)

	recorder := getCodeScripts(t, srv, apiKey)
	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Scripts []codescripts.Entry `json:"scripts"`
			Dir     string              `json:"dir"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	assert.Empty(t, resp.Data.Scripts)
	assert.Equal(t, scriptsDir, resp.Data.Dir)
}

// TestHandleListScripts_RequiresAPIKey: the listing exposes the names and paths
// of everything stored, so it inherits the /api/v1 key requirement.
func TestHandleListScripts_RequiresAPIKey(t *testing.T) {
	srv, _ := newCodeScriptsServer(t, "test-code-scripts-key")

	recorder := getCodeScripts(t, srv, "")
	assert.Equal(t, http.StatusUnauthorized, recorder.Code, "body: %s", recorder.Body.String())
}
