//go:build server

package httpapi

// Spec 107 T009 (US5, FR-039): the two HTTP write doors refuse a document that
// carries a removed server-edition key or a never-implemented auth_broker mode,
// with the exact messages of contracts/config-keys.md, BEFORE the typed decode.
//
// Config.Validate cannot do this job: json.Unmarshal into config.Config
// silently drops unknown keys, so by the time ApplyConfig runs the offending
// keys are gone and the write would be persisted clean with a success toast.
// The refusal therefore comes from config.ValidateRemovedKeys run on the
// generic map — in handlePatchConfig on the merged map before
// json.Unmarshal(mergedBytes, &merged), and in handleApplyConfig on the decoded
// raw document. The boot door (normalise + record a LoadDiagnostic instead of
// refusing) is covered by internal/config/legacy_keys_load_test.go.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	runtime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

const removedKeysFixture = "../config/testdata/legacy_server_edition.json"

// Exact strings from contracts/config-keys.md (the same ones the boot door
// records as LoadDiagnostics).
var removedKeyMessages = []string{
	"server_edition.max_user_servers is no longer supported and was ignored",
	"server_edition.workspace_idle_timeout is no longer supported and was ignored",
	`auth_broker.mode "token_exchange" was never implemented; the auth_broker block for server "legacy-exchange" was ignored`,
	"auth_broker.header is no longer supported and was ignored",
}

// removedKeysController serves a clean live config and fails the test if a
// write door reaches ApplyConfig with a document that should have been refused.
type removedKeysController struct {
	baseController
	live    *config.Config
	applied int
}

func (m *removedKeysController) GetCurrentConfig() any { return &config.Config{APIKey: "test-key"} }
func (m *removedKeysController) GetConfig() (*config.Config, error) {
	return m.live, nil
}
func (m *removedKeysController) GetConfigPath() string { return "/tmp/mcp_config.json" }
func (m *removedKeysController) ApplyConfig(cfg *config.Config, _ string) (*runtime.ConfigApplyResult, error) {
	m.applied++
	return &runtime.ConfigApplyResult{Success: true, AppliedImmediately: true}, nil
}

func newRemovedKeysServer(t *testing.T) (*Server, *removedKeysController) {
	t.Helper()
	ctrl := &removedKeysController{
		live: &config.Config{
			Listen: "127.0.0.1:8080",
			APIKey: "test-key",
			ServerEdition: &config.ServerEditionConfig{
				Enabled:     true,
				AdminEmails: []string{"admin@example.com"},
				OAuth: &config.ServerEditionOAuthConfig{
					Provider:     "google",
					ClientID:     "live-client-id",
					ClientSecret: "live-client-secret",
				},
			},
		},
	}
	return NewServer(ctrl, zap.NewNop().Sugar(), nil), ctrl
}

func removedKeysDo(t *testing.T, srv *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// legacyDocument returns the shared fixture as a generic document with no
// data_dir (the write doors never create directories).
func legacyDocument(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(removedKeysFixture))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

// assertRefusedWithRemovedKeyMessages checks a 400 whose payload names every
// removed key/mode by its contract message and carries them structurally under
// data.validation_errors (the #1084 shape a client points at a field with).
func assertRefusedWithRemovedKeyMessages(t *testing.T, w *httptest.ResponseRecorder, door string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, w.Code, "%s: body=%s", door, w.Body.String())

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope), "%s: body=%s", door, w.Body.String())
	assert.Equal(t, false, envelope["success"], door)

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "%s: the refusal must carry a structured payload: %s", door, w.Body.String())
	rawErrs, ok := data["validation_errors"].([]any)
	require.True(t, ok, "%s: validation_errors must be present: %s", door, w.Body.String())

	messages := make([]string, 0, len(rawErrs))
	for _, e := range rawErrs {
		entry, ok := e.(map[string]any)
		require.True(t, ok, door)
		msg, _ := entry["message"].(string)
		messages = append(messages, msg)
		field, _ := entry["field"].(string)
		assert.NotEmpty(t, field, "%s: every refusal names its field: %v", door, entry)
	}
	assert.ElementsMatch(t, removedKeyMessages, messages,
		"%s: one refusal per removed key/mode, byte-identical to contracts/config-keys.md", door)
	assert.NotContains(t, w.Body.String(), "store_idp_tokens",
		"%s: store_idp_tokens is deprecated, not removed — it is not refused", door)
}

// PATCH door: a partial document carrying the removed keys/modes is refused by
// the raw-document check on the MERGED map, and ApplyConfig is never reached.
func TestPatchConfig_RemovedServerEditionKeysAreRefusedBeforeTypedDecode(t *testing.T) {
	srv, ctrl := newRemovedKeysServer(t)
	doc := legacyDocument(t)
	patch := map[string]any{
		"server_edition": doc["server_edition"],
		"mcpServers":     doc["mcpServers"],
	}
	body, err := json.Marshal(patch)
	require.NoError(t, err)

	w := removedKeysDo(t, srv, http.MethodPatch, "/api/v1/config", body)
	assertRefusedWithRemovedKeyMessages(t, w, "PATCH /api/v1/config")
	assert.Zero(t, ctrl.applied, "PATCH must refuse before ApplyConfig: the keys would already be gone from the typed struct")
}

// PATCH door, narrow patch: a single removed key inside server_edition — the
// shape the raw-JSON editor produces for one field — is enough to refuse.
func TestPatchConfig_SingleRemovedKeyIsRefused(t *testing.T) {
	srv, ctrl := newRemovedKeysServer(t)
	body := []byte(`{"server_edition":{"max_user_servers":5}}`)

	w := removedKeysDo(t, srv, http.MethodPatch, "/api/v1/config", body)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), removedKeyMessages[0])
	assert.Zero(t, ctrl.applied)
}

// PATCH door, control: the deprecated store_idp_tokens is NOT a removed key
// and an oauth_connect block without header/header_format is accepted.
func TestPatchConfig_DeprecatedAndRetainedKeysStillPass(t *testing.T) {
	srv, ctrl := newRemovedKeysServer(t)
	body := []byte(`{
		"server_edition": {"store_idp_tokens": true},
		"mcpServers": [{
			"name": "connect",
			"url": "https://connect.example.com/mcp",
			"protocol": "http",
			"enabled": true,
			"auth_broker": {
				"mode": "oauth_connect",
				"token_endpoint": "https://idp.example.com/token",
				"authorization_endpoint": "https://idp.example.com/authorize"
			}
		}]
	}`)

	w := removedKeysDo(t, srv, http.MethodPatch, "/api/v1/config", body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied, "a document with no removed key reaches ApplyConfig")
}

// Apply door: the whole legacy document POSTed to /config/apply is refused on
// the decoded raw document, before UnmaskLiveConfigDocument types it.
func TestApplyConfig_RemovedServerEditionKeysAreRefusedBeforeTypedDecode(t *testing.T) {
	srv, ctrl := newRemovedKeysServer(t)
	body, err := json.Marshal(legacyDocument(t))
	require.NoError(t, err)

	w := removedKeysDo(t, srv, http.MethodPost, "/api/v1/config/apply", body)
	assertRefusedWithRemovedKeyMessages(t, w, "POST /api/v1/config/apply")
	assert.Zero(t, ctrl.applied, "/config/apply must refuse before ApplyConfig")
}

// Apply door, control: the same document with the removed keys stripped is
// accepted — the check refuses the keys, not the block.
func TestApplyConfig_CleanServerEditionDocumentPasses(t *testing.T) {
	srv, ctrl := newRemovedKeysServer(t)
	doc := legacyDocument(t)
	require.Empty(t, config.ValidateRemovedKeys(stripRemovedKeys(t, doc)), "the control document must itself be clean")
	body, err := json.Marshal(doc)
	require.NoError(t, err)

	w := removedKeysDo(t, srv, http.MethodPost, "/api/v1/config/apply", body)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, ctrl.applied)
}

// stripRemovedKeys mutates doc in place the way the boot normaliser is
// specified to (whole block for token_exchange/entra_obo, leaves otherwise)
// and returns it.
func stripRemovedKeys(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	if se, ok := doc["server_edition"].(map[string]any); ok {
		delete(se, "max_user_servers")
		delete(se, "workspace_idle_timeout")
	}
	servers, _ := doc["mcpServers"].([]any)
	for _, s := range servers {
		server, ok := s.(map[string]any)
		if !ok {
			continue
		}
		broker, ok := server["auth_broker"].(map[string]any)
		if !ok {
			continue
		}
		switch broker["mode"] {
		case "token_exchange", "entra_obo":
			delete(server, "auth_broker")
		default:
			delete(broker, "header")
			delete(broker, "header_format")
		}
	}
	return doc
}
