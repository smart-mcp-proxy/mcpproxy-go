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

// Spec 107 cross-review round 6, chunk 3 P2: LoadFromFile must NOT resolve
// server_edition.oauth.client_id/client_secret into the returned Config — that
// object is r.cfg/r.desiredCfg, and GetDesiredConfig/ApplyConfig round-trip it
// back to disk through SaveConfig on every later PATCH /api/v1/config or
// /config/apply, even one editing an unrelated field. An earlier design
// resolved the `${env:...}` reference in place at Load time, so that very
// next unrelated save permanently overwrote the operator's reference in
// mcp_config.json with the plaintext secret. The fix: the returned Config
// keeps the operator's literal `${env:...}` text (Validate() resolves it
// read-only, just to enforce "required"; auth.NewOAuthHandler resolves it
// again on its own private, never-persisted clone for the actual token
// exchange) — proved here two ways: the loaded value is still the reference,
// and a save-after-load (SaveConfig on the returned cfg, exactly what
// ApplyConfig does with the merged PATCH result) writes the reference back to
// disk, never the secret.
func TestLoadFromFile_DoesNotResolveOAuthSecretIntoTheReturnedConfig(t *testing.T) {
	t.Setenv("MCPPROXY_TEST_OIDC_SECRET_ABC", "super-secret-plaintext-value")

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
				"client_secret": "${env:MCPPROXY_TEST_OIDC_SECRET_ABC}",
			},
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ServerEdition)
	require.NotNil(t, cfg.ServerEdition.OAuth)
	assert.Equal(t, "${env:MCPPROXY_TEST_OIDC_SECRET_ABC}", cfg.ServerEdition.OAuth.ClientSecret,
		"the loaded Config must keep the operator's reference text, not the resolved secret — "+
			"this is the object every later PATCH/apply merges onto and SaveConfig writes back to disk")

	// Simulate the next unrelated save (SaveConfig on this exact object, as
	// ApplyConfig does with its merged result): the file on disk must still
	// carry the reference, never the plaintext secret.
	require.NoError(t, SaveConfig(cfg, path))
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(saved), "${env:MCPPROXY_TEST_OIDC_SECRET_ABC}",
		"a later save must persist the reference")
	assert.NotContains(t, string(saved), "super-secret-plaintext-value",
		"a later save must never persist the resolved secret")
}
