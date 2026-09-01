package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #1148, round 6 finding 2. Moving the LIVE rendering onto the REST read
// door is only safe if the REST WRITE door reverts exactly that rendering.
// Without these, `GET` → edit one field → `PATCH` would persist the mask string
// over the live credential — turning a disclosure into corruption, which is the
// one trade this branch must never make.

const round6Token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

// storedRound6Server is the server the read door renders and the write door
// must restore.
func storedRound6Server() *config.ServerConfig {
	return &config.ServerConfig{
		Name:     "github",
		Protocol: "streamable-http",
		URL:      "https://host/mcp?opaque=" + round6Token,
		Env:      map[string]string{"BENIGN": round6Token},
		Headers:  map[string]string{"X-Weird": round6Token},
		Enabled:  true,
	}
}

// maskedRound6Payload renders the stored server through the REST READ door,
// exactly as a client would receive it from GET /api/v1/servers.
func maskedRound6Payload(t *testing.T) *contracts.Server {
	t.Helper()
	stored := storedRound6Server()
	view := &contracts.Server{
		Name:    stored.Name,
		URL:     stored.URL,
		Env:     map[string]string{"BENIGN": stored.Env["BENIGN"]},
		Headers: map[string]string{"X-Weird": stored.Headers["X-Weird"]},
	}
	oauth.RedactServerSecretFields(view)
	require.NotContains(t, view.URL, round6Token)
	require.NotContains(t, view.Env["BENIGN"], round6Token)
	require.NotContains(t, view.Headers["X-Weird"], round6Token)
	return view
}

func patchRound6(t *testing.T, body map[string]any) (*httptest.ResponseRecorder, *mockPatchServerController) {
	t.Helper()
	mockCtrl := &mockPatchServerController{
		apiKey:         "test-key",
		existingServer: storedRound6Server(),
	}
	srv := NewServer(mockCtrl, zap.NewNop().Sugar(), nil)

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/servers/github", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w, mockCtrl
}

// TestPatchServer_RevertsTheLiveMaskItRendered is the round trip: read the
// masked payload back, echo it into PATCH, and the stored credentials must
// survive untouched.
func TestPatchServer_RevertsTheLiveMaskItRendered(t *testing.T) {
	masked := maskedRound6Payload(t)

	w, ctrl := patchRound6(t, map[string]any{
		"url":     masked.URL,
		"env":     map[string]string{"BENIGN": masked.Env["BENIGN"]},
		"headers": map[string]string{"X-Weird": masked.Headers["X-Weird"]},
	})

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, ctrl.capturedUpdates)
	assert.Equal(t, "https://host/mcp?opaque="+round6Token, ctrl.capturedUpdates.URL,
		"the stored URL credential must not be overwritten by its own mask")
	assert.Equal(t, round6Token, ctrl.capturedUpdates.Env["BENIGN"],
		"the stored env credential must not be overwritten by its own mask")
	assert.Equal(t, round6Token, ctrl.capturedUpdates.Headers["X-Weird"],
		"the stored header credential must not be overwritten by its own mask")
}

// An edit to an unrelated part of the URL must still restore the credential —
// the per-parameter binding is what makes that work.
func TestPatchServer_RevertsTheLiveURLMaskAcrossAnUnrelatedEdit(t *testing.T) {
	masked := maskedRound6Payload(t)
	edited := replaceOnce(t, masked.URL, "/mcp?", "/mcp/v2?")

	w, ctrl := patchRound6(t, map[string]any{"url": edited})

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, ctrl.capturedUpdates)
	assert.Equal(t, "https://host/mcp/v2?opaque="+round6Token, ctrl.capturedUpdates.URL)
}

// A mask nothing can bind back must be REFUSED, never persisted.
func TestPatchServer_RefusesAMaskItCannotBind(t *testing.T) {
	masked := maskedRound6Payload(t)

	t.Run("url moved to another host", func(t *testing.T) {
		moved := replaceOnce(t, masked.URL, "https://host/", "https://evil.example/")
		w, ctrl := patchRound6(t, map[string]any{"url": moved})
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Nil(t, ctrl.capturedUpdates, "nothing may be persisted")
	})

	t.Run("env mask relocated to a key it was never read from", func(t *testing.T) {
		w, ctrl := patchRound6(t, map[string]any{
			"env": map[string]string{"OTHER": masked.Env["BENIGN"]},
		})
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Nil(t, ctrl.capturedUpdates, "nothing may be persisted")
	})

	t.Run("mask in a field with no revert at all", func(t *testing.T) {
		w, ctrl := patchRound6(t, map[string]any{
			"isolation": map[string]any{"extra_args": []string{"--env", "••••ab (12 chars)"}},
		})
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Nil(t, ctrl.capturedUpdates, "nothing may be persisted")
	})
}

// POST has no stored value to bind a mask to, so every mask is a placeholder
// copied out of a read payload and must be refused — on every field, not just
// args (round 6 finding 4's REST half).
func TestAddServer_RefusesEveryEchoedMask(t *testing.T) {
	masked := maskedRound6Payload(t)

	for name, body := range map[string]map[string]any{
		"url":     {"name": "new", "url": masked.URL},
		"env":     {"name": "new", "command": "npx", "env": map[string]string{"BENIGN": masked.Env["BENIGN"]}},
		"headers": {"name": "new", "url": "https://host/mcp", "headers": map[string]string{"X-Weird": masked.Headers["X-Weird"]}},
	} {
		t.Run(name, func(t *testing.T) {
			mockCtrl := &mockPatchServerController{apiKey: "test-key"}
			srv := NewServer(mockCtrl, zap.NewNop().Sugar(), nil)
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		})
	}
}

func replaceOnce(t *testing.T, s, old, replacement string) string {
	t.Helper()
	idx := indexOf(s, old)
	require.GreaterOrEqual(t, idx, 0, "%q not found in %q", old, s)
	return s[:idx] + replacement + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
