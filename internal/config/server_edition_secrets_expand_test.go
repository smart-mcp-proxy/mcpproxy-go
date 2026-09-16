//go:build server

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 cross-review round 2, chunk 3 P2: a missing ${env:...} referenced
// by server_edition.oauth.client_secret/client_id must be refused by
// Validate's "is required" message at boot, not silently kept as the literal
// placeholder text (which is non-empty and so passed the required check,
// sending "${env:MISSING}" itself to the IdP as the client secret).
func TestExpandServerEditionSecrets_MissingEnvVarIsRefusedAtValidate(t *testing.T) {
	os.Unsetenv("MCPPROXY_TEST_MISSING_OIDC_SECRET_XYZ")

	dir := t.TempDir()
	doc := map[string]any{
		"listen":   "127.0.0.1:8080",
		"data_dir": dir,
		"server_edition": map[string]any{
			"enabled":      true,
			"admin_emails": []string{"admin@example.com"},
			"oauth": map[string]any{
				"provider":      "google",
				"client_id":     "cid",
				"client_secret": "${env:MCPPROXY_TEST_MISSING_OIDC_SECRET_XYZ}",
			},
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))

	_, err = LoadFromFile(path)
	require.Error(t, err, "a missing env var must be refused at boot, not silently accepted as the literal placeholder")
	assert.Contains(t, err.Error(), "server_edition.oauth.client_secret is required",
		"the required-field message, not a downstream IdP-login failure with the placeholder text")
}
