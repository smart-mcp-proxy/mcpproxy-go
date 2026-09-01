package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	runtime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Issue #1148, round 9. Two doors the round-8 inventory never saw.
//
//  1. `POST /api/v1/servers/import{,/json,/path}` echoes the operator's source
//     file back with `url`, `command` and `args` RAW, while its CLI twin
//     (`upstream import`, buildImportedServersOutput) was redacted in round 8.
//     Same data, two doors, one redacted.
//
//  2. `GET /api/v1/config` returns the ENTIRE config — every server's env,
//     headers, oauth.client_secret and url credentials, plus the global
//     docker_isolation.extra_args — with no rule at all. It is the widest door
//     on the tree, and masking it is only half an answer: the raw-JSON editor
//     and the onboarding wizard both GET this document and POST it back, so
//     without a matching bind-or-refuse on the write path the mask would be
//     PERSISTED over the credential — the #1142 corruption this branch has
//     spent four rounds preventing.

const (
	round9Secret     = "ghp_1234567890abcdefghijABCDEFGHIJ123456"
	round9OAuthSecre = "oauth-client-secret-value"
)

func round9Config() *config.Config {
	return &config.Config{
		APIKey: "super-secret-api-key",
		Servers: []*config.ServerConfig{{
			Name:     "github",
			Protocol: "http",
			URL:      "https://host/mcp?access_token=" + round9Secret,
			Command:  "npx",
			Args:     []string{"--token", round9Secret},
			Env:      map[string]string{"GITHUB_TOKEN": round9Secret},
			Headers:  map[string]string{"Authorization": "Bearer " + round9Secret},
			OAuth:    &config.OAuthConfig{ClientID: "abc", ClientSecret: round9OAuthSecre},
		}},
		DockerIsolation: &config.DockerIsolationConfig{
			Enabled:   true,
			ExtraArgs: []string{"-e", "API_KEY=" + round9Secret},
		},
	}
}

// mockRound9ConfigController serves a config with secrets in every shape a
// server can carry one, and captures whatever the write doors resolve.
type mockRound9ConfigController struct {
	baseController
	live     *config.Config
	captured *config.Config
	applied  int
}

func (m *mockRound9ConfigController) GetCurrentConfig() any {
	return &config.Config{APIKey: "test-key"}
}
func (m *mockRound9ConfigController) GetConfig() (*config.Config, error) { return m.live, nil }
func (m *mockRound9ConfigController) GetConfigPath() string              { return "/tmp/mcp_config.json" }

func (m *mockRound9ConfigController) ApplyConfig(cfg *config.Config, _ string) (*runtime.ConfigApplyResult, error) {
	clone := *cfg
	m.captured = &clone
	m.applied++
	return &runtime.ConfigApplyResult{Success: true, AppliedImmediately: true}, nil
}

func round9Server(t *testing.T, ctrl *mockRound9ConfigController) *Server {
	t.Helper()
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func round9Do(t *testing.T, srv *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body == nil {
		rdr = bytes.NewReader(nil)
	} else {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// getConfigDocument returns the `config` object GET /api/v1/config published.
func getConfigDocument(t *testing.T, srv *Server) map[string]interface{} {
	t.Helper()
	w := round9Do(t, srv, http.MethodGet, "/api/v1/config", nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var envelope struct {
		Data struct {
			Config map[string]interface{} `json:"config"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data.Config, "GET /api/v1/config returned no config document")
	return envelope.Data.Config
}

// --- door 2: GET /api/v1/config ---------------------------------------------

func TestGetConfig_MasksEverySecretItPublishes(t *testing.T) {
	ctrl := &mockRound9ConfigController{live: round9Config()}
	srv := round9Server(t, ctrl)

	w := round9Do(t, srv, http.MethodGet, "/api/v1/config", nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := w.Body.String()

	for _, secret := range []string{round9Secret, round9OAuthSecre, "super-secret-api-key"} {
		assert.NotContains(t, body, secret,
			"GET /api/v1/config published a credential in the clear. It serves the WHOLE config — "+
				"every server's env, headers, oauth.client_secret and url credentials, plus the global "+
				"docker_isolation.extra_args — and every sibling door masks the same leaves.")
	}

	// A mask that eats the document is not a fix: this endpoint backs the
	// raw-JSON editor and the Settings form, so every non-secret leaf and every
	// key must survive the walk.
	document := getConfigDocument(t, srv)
	servers, ok := document["mcpServers"].([]interface{})
	require.True(t, ok, "the walk dropped mcpServers")
	require.Len(t, servers, 1)
	server, ok := servers[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "github", server["name"], "a server name is not a secret and must stay readable")
	assert.Equal(t, "npx", server["command"], "an ordinary command passes the shared leaf rule unchanged")
	assert.Contains(t, server, "env", "the env block was dropped rather than masked")
	assert.Contains(t, server, "oauth", "the oauth block was dropped rather than masked")
	isolation, ok := document["docker_isolation"].(map[string]interface{})
	require.True(t, ok, "the walk dropped the global docker_isolation block")
	assert.Equal(t, true, isolation["enabled"], "a non-secret isolation flag must survive the walk")
}

// The mask is only safe if the write path can put it back. Both the raw-JSON
// editor and the onboarding wizard GET this document, splice one field, and
// POST the whole thing to /config/apply.
func TestConfigApply_RoundTripOfTheMaskedReadKeepsTheCredentials(t *testing.T) {
	ctrl := &mockRound9ConfigController{live: round9Config()}
	srv := round9Server(t, ctrl)

	document := getConfigDocument(t, srv)
	document["quarantine_enabled"] = false // the one field the operator changed

	body, err := json.Marshal(document)
	require.NoError(t, err)
	w := round9Do(t, srv, http.MethodPost, "/api/v1/config/apply", body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NotNil(t, ctrl.captured, "ApplyConfig was never called")
	require.Len(t, ctrl.captured.Servers, 1)
	got := ctrl.captured.Servers[0]

	assert.Equal(t, round9Secret, got.Env["GITHUB_TOKEN"], "the env mask was persisted over the credential")
	assert.Equal(t, "Bearer "+round9Secret, got.Headers["Authorization"], "the header mask was persisted over the credential")
	assert.Equal(t, "https://host/mcp?access_token="+round9Secret, got.URL, "the url mask was persisted over the credential")
	require.NotNil(t, got.OAuth)
	assert.Equal(t, round9OAuthSecre, got.OAuth.ClientSecret, "the oauth mask was persisted over the credential")
	assert.Equal(t, "super-secret-api-key", ctrl.captured.APIKey, "the api_key mask was persisted over the key")
	assert.Equal(t, []string{"--token", round9Secret}, got.Args,
		"the argv mask was persisted over the credential. argv has no key to bind to leaf by leaf, so the "+
			"only binding it can ever have is the one this round trip gives it: the server block came back "+
			"byte for byte as the read door rendered it, which is proof the caller changed nothing in it.")
	assert.Equal(t, []string{"-e", "API_KEY=" + round9Secret}, ctrl.captured.DockerIsolation.ExtraArgs,
		"the global docker_isolation.extra_args mask was persisted over the credential")
}

// The binding above is proof of NO CHANGE, not a licence to revert. Edit
// anything inside the block and it is gone — and an argv slot has nothing else
// to bind to, so the write must be refused rather than written through.
func TestConfigApply_RefusesAnArgvMaskUnderAnEditedServer(t *testing.T) {
	ctrl := &mockRound9ConfigController{live: round9Config()}
	srv := round9Server(t, ctrl)

	document := getConfigDocument(t, srv)
	servers, ok := document["mcpServers"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, servers)
	server, ok := servers[0].(map[string]interface{})
	require.True(t, ok)
	server["protocol"] = "sse" // an ordinary edit, but it breaks the whole-block echo

	body, err := json.Marshal(document)
	require.NoError(t, err)
	w := round9Do(t, srv, http.MethodPost, "/api/v1/config/apply", body)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"an argv mask under an EDITED server was accepted; nothing binds an argv slot but its index")
	assert.Contains(t, w.Body.String(), "args",
		"the refusal must name the field the operator has to resend")
	assert.Nil(t, ctrl.captured, "ApplyConfig must not run on a refused write")
}

// And the other half: a mask the write path cannot BIND must be refused, not
// written through. `args` has only an index to bind to, which is no binding —
// the decision table has said `refuse` for it since round 6.
func TestConfigApply_RefusesAMaskItCannotBind(t *testing.T) {
	ctrl := &mockRound9ConfigController{live: round9Config()}
	srv := round9Server(t, ctrl)

	document := getConfigDocument(t, srv)
	servers, ok := document["mcpServers"].([]interface{})
	require.True(t, ok, "config document carries no mcpServers array")
	require.NotEmpty(t, servers)
	server, ok := servers[0].(map[string]interface{})
	require.True(t, ok)
	// The operator renamed the server: nothing binds the masked argv token back.
	server["name"] = "github-renamed"

	body, err := json.Marshal(document)
	require.NoError(t, err)
	w := round9Do(t, srv, http.MethodPost, "/api/v1/config/apply", body)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"an unbindable mask was ACCEPTED on /config/apply and would be persisted over the credential")
	assert.Nil(t, ctrl.captured, "ApplyConfig must not run on a refused write")
}

func TestPatchConfig_RevertsWhatItCanBindAndRefusesWhatItCannot(t *testing.T) {
	t.Run("bound by key, reverted", func(t *testing.T) {
		ctrl := &mockRound9ConfigController{live: round9Config()}
		srv := round9Server(t, ctrl)

		masked := oauth.LiveRedaction.EnvValue("GITHUB_TOKEN", round9Secret)
		require.NotEqual(t, round9Secret, masked, "precondition: the read door masks this env value")
		patch := map[string]interface{}{
			"mcpServers": []interface{}{map[string]interface{}{
				"name": "github",
				"env":  map[string]interface{}{"GITHUB_TOKEN": masked},
			}},
		}
		body, err := json.Marshal(patch)
		require.NoError(t, err)
		w := round9Do(t, srv, http.MethodPatch, "/api/v1/config", body)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		require.NotNil(t, ctrl.captured)
		require.Len(t, ctrl.captured.Servers, 1)
		assert.Equal(t, round9Secret, ctrl.captured.Servers[0].Env["GITHUB_TOKEN"],
			"an echoed env mask bound to its own key must be reverted, never persisted")
	})

	t.Run("no binding, refused", func(t *testing.T) {
		ctrl := &mockRound9ConfigController{live: round9Config()}
		srv := round9Server(t, ctrl)

		maskedArgs := oauth.LiveRedaction.Argv([]string{"--token", round9Secret})
		require.NotEqual(t, round9Secret, maskedArgs[1], "precondition: the read door masks this argv token")
		patch := map[string]interface{}{
			"mcpServers": []interface{}{map[string]interface{}{
				"name": "github-renamed",
				"args": []interface{}{maskedArgs[0], maskedArgs[1]},
			}},
		}
		body, err := json.Marshal(patch)
		require.NoError(t, err)
		w := round9Do(t, srv, http.MethodPatch, "/api/v1/config", body)

		assert.Equal(t, http.StatusBadRequest, w.Code,
			"an argv mask with nothing to bind to was ACCEPTED and would be persisted over the credential")
		assert.Nil(t, ctrl.captured, "ApplyConfig must not run on a refused write")
	})
}

// --- door 1: the import preview ---------------------------------------------

type mockRound9ImportController struct {
	baseController
}

func (m *mockRound9ImportController) GetCurrentConfig() any {
	return &config.Config{APIKey: "test-key"}
}
func (m *mockRound9ImportController) AddServer(_ context.Context, _ *config.ServerConfig) error {
	return nil
}

func TestImportPreview_MasksTheSameLeavesItsCLITwinDoes(t *testing.T) {
	srv := NewServer(&mockRound9ImportController{}, zap.NewNop().Sugar(), nil)

	source := `{"mcpServers":{"github":{"command":"npx","args":["--token","` + round9Secret + `"],` +
		`"env":{"GITHUB_TOKEN":"` + round9Secret + `"}},` +
		`"remote":{"url":"https://host/mcp?access_token=` + round9Secret + `"}}}`
	body, err := json.Marshal(ImportRequest{Content: source})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	assert.NotContains(t, w.Body.String(), round9Secret,
		"POST /api/v1/servers/import echoed the operator's source file back with url/command/args RAW. "+
			"Its CLI twin (upstream import → buildImportedServersOutput) masks exactly these leaves with the "+
			"shared LIVE rule; same data, two doors, one answer.")

	// And the readable half of the rule still holds: an ordinary command and an
	// ordinary flag pass through byte-identical.
	var wrapped wrappedImportResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &wrapped))
	require.Len(t, wrapped.Data.Imported, 2)
	for _, imported := range wrapped.Data.Imported {
		if imported.Command != "" {
			assert.Equal(t, "npx", imported.Command, "an ordinary command must survive the rule unchanged")
			require.NotEmpty(t, imported.Args)
			assert.Equal(t, "--token", imported.Args[0], "an ordinary flag must survive the rule unchanged")
		}
		if imported.URL != "" {
			assert.True(t, strings.HasPrefix(imported.URL, "https://host/mcp"),
				"the URL is masked component-wise, so scheme and host stay readable: %q", imported.URL)
		}
	}
}
