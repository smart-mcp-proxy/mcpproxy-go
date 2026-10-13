package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// secretResolverController wraps MockServerController with a real
// secret.Resolver so GET /secrets/config exercises the real
// keyring-availability probe (FR-065) instead of the always-nil resolver the
// bare mock returns.
type secretResolverController struct {
	*MockServerController
	resolver *secret.Resolver
}

func (c *secretResolverController) GetSecretResolver() *secret.Resolver {
	return c.resolver
}

func newSecretResolverTestServer(t *testing.T) *Server {
	t.Helper()
	logger := zaptest.NewLogger(t).Sugar()
	controller := &secretResolverController{
		MockServerController: &MockServerController{},
		resolver:             secret.NewResolver(),
	}
	return NewServer(controller, logger, nil)
}

// TestGetConfigSecrets_KeyringAvailability pins FR-065: GET /secrets/config
// reports keyring_available and, when false, a non-empty keyring_reason — a
// CI runner has no usable OS keyring, so this also runs deterministically
// under `go test`.
func TestGetConfigSecrets_KeyringAvailability(t *testing.T) {
	t.Setenv("CI", "true")
	server := newSecretResolverTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/config", nil)
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Data struct {
			KeyringAvailable bool   `json:"keyring_available"`
			KeyringReason    string `json:"keyring_reason"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.False(t, body.Data.KeyringAvailable, "expected keyring unavailable in CI")
	require.NotEmpty(t, body.Data.KeyringReason, "expected a non-empty reason")
}
