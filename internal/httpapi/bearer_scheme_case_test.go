package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The Authorization scheme name is case-insensitive (RFC 7235 §2.1), so a
// client that sends "bearer <key>" must be treated like "Bearer <key>".
func TestBearerScheme_CaseInsensitive(t *testing.T) {
	const apiKey = "admin-key-12345"

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/status", nil)
			req.Header.Set("Authorization", scheme+" "+apiKey)
			assert.Equal(t, apiKey, ExtractToken(req))

			srv := NewServer(&testControllerWithConfig{cfg: &config.Config{APIKey: apiKey}}, zap.NewNop().Sugar(), nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "admin key sent with %q scheme should authenticate", scheme)
		})
	}

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Basic "+apiKey)
	assert.Equal(t, "", ExtractToken(req), "non-Bearer schemes must not yield a token")
}
