package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockFixController gives us GetCurrentConfig; the rest comes from baseController.
type mockFixController struct {
	baseController
	apiKey string
}

func (m *mockFixController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

// resetFixLimiter clears the global rate limiter between subtests so 1/s rate
// limiting doesn't cause spurious 429s.
func resetFixLimiter() {
	globalFixLimiter.mu.Lock()
	globalFixLimiter.last = map[string]time.Time{}
	globalFixLimiter.mu.Unlock()
}

// TestDiagnosticsFix_ModeGuard covers the destructive-mode 409 guard restored
// in the gemini P2 fix. Previously mode="" + destructive was silently downgraded
// to dry_run, making the guard dead code.
func TestDiagnosticsFix_ModeGuard(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockCtrl := &mockFixController{apiKey: "test-key"}
	srv := NewServer(mockCtrl, logger, nil)

	// Register a harmless fixer for both the non-destructive and destructive
	// fix-step keys we reference below. InvokeFixer will succeed without side
	// effects.
	diagnostics.Register("stdio_show_last_logs", func(ctx context.Context, _ diagnostics.FixRequest) (diagnostics.FixResult, error) {
		return diagnostics.FixResult{Outcome: diagnostics.OutcomeSuccess, Preview: "ok"}, nil
	})
	diagnostics.Register("oauth_reauth", func(ctx context.Context, _ diagnostics.FixRequest) (diagnostics.FixResult, error) {
		return diagnostics.FixResult{Outcome: diagnostics.OutcomeSuccess, Preview: "ok"}, nil
	})

	call := func(t *testing.T, body fixRequestBody) *httptest.ResponseRecorder {
		t.Helper()
		resetFixLimiter()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/diagnostics/fix", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}

	t.Run("non-destructive fixer, mode unset -> 200 (defaults to execute)", func(t *testing.T) {
		w := call(t, fixRequestBody{
			Server:   "srv1",
			Code:     string(diagnostics.STDIOSpawnENOENT),
			FixerKey: "stdio_show_last_logs",
		})
		assert.Equal(t, http.StatusOK, w.Code, "non-destructive fix without mode should succeed")
	})

	t.Run("destructive fixer, mode unset -> 409", func(t *testing.T) {
		w := call(t, fixRequestBody{
			Server:   "srv1",
			Code:     string(diagnostics.OAuthRefreshExpired),
			FixerKey: "oauth_reauth",
		})
		assert.Equal(t, http.StatusConflict, w.Code,
			"destructive fix with absent mode must return 409 (gemini P2 guard)")
	})

	t.Run("destructive fixer, mode=dry_run -> 200", func(t *testing.T) {
		w := call(t, fixRequestBody{
			Server:   "srv1",
			Code:     string(diagnostics.OAuthRefreshExpired),
			FixerKey: "oauth_reauth",
			Mode:     diagnostics.ModeDryRun,
		})
		assert.Equal(t, http.StatusOK, w.Code, "destructive fix with explicit dry_run should succeed")
	})

	t.Run("destructive fixer, mode=execute -> 200", func(t *testing.T) {
		w := call(t, fixRequestBody{
			Server:   "srv1",
			Code:     string(diagnostics.OAuthRefreshExpired),
			FixerKey: "oauth_reauth",
			Mode:     diagnostics.ModeExecute,
		})
		assert.Equal(t, http.StatusOK, w.Code, "destructive fix with explicit execute should succeed")
	})
}
