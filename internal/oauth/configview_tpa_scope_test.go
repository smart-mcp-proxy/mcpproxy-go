package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The public-key exemption of security.tpa_operator_signers is scoped to that
// config path. The same key NAME elsewhere is a user-chosen map key (an env
// var, a header, an OAuth extra param, any free-form map) and its value is
// masked like any other secret.
const tpaScopeSecret = "sk-live-AbCdEf0123456789AbCdEf0123456789XYZ"

func TestRedactedConfig_TPASignerExemptionIsScopedToTheSecurityPath(t *testing.T) {
	signer := "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	cfg := config.DefaultConfig()
	cfg.Security = &config.SecurityConfig{TPAOperatorSigners: []string{signer}}
	cfg.Servers = []*config.ServerConfig{{
		Name:     "srv",
		Command:  "node",
		Protocol: "stdio",
		Enabled:  true,
		Env:      map[string]string{"tpa_operator_signers": tpaScopeSecret, "OTHER": tpaScopeSecret},
		Headers:  map[string]string{"tpa_operator_signers": tpaScopeSecret},
		OAuth:    &config.OAuthConfig{ExtraParams: map[string]string{"tpa_operator_signers": tpaScopeSecret}},
	}}

	masked := RedactedConfig(cfg)
	require.NotNil(t, masked)

	assert.Equal(t, []string{signer}, masked.Security.TPAOperatorSigners, "the real signer list stays readable")
	require.Len(t, masked.Servers, 1)
	srv := masked.Servers[0]
	assert.NotEqual(t, tpaScopeSecret, srv.Env["tpa_operator_signers"], "env var of that name is masked")
	assert.Equal(t, srv.Env["OTHER"], srv.Env["tpa_operator_signers"], "masked exactly like any other env var")
	assert.NotEqual(t, tpaScopeSecret, srv.Headers["tpa_operator_signers"], "header of that name is masked")
	assert.NotEqual(t, tpaScopeSecret, srv.OAuth.ExtraParams["tpa_operator_signers"], "oauth extra param of that name is masked")
	assert.Equal(t, tpaScopeSecret, cfg.Servers[0].Env["tpa_operator_signers"], "the source config is untouched")
}

func TestRedactionValue_TPASignerExemptionNeedsTheSecurityParent(t *testing.T) {
	signer := "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	list := func() []interface{} { return []interface{}{signer} }

	t.Run("under security: unmasked", func(t *testing.T) {
		got := LiveRedaction.Value("", map[string]interface{}{
			"security": map[string]interface{}{"tpa_operator_signers": list()},
		})
		assert.Equal(t, map[string]interface{}{
			"security": map[string]interface{}{"tpa_operator_signers": []interface{}{signer}},
		}, got)
	})

	t.Run("as a free-form string leaf anywhere: masked", func(t *testing.T) {
		got := LiveRedaction.Value("", map[string]interface{}{
			"free":                 map[string]interface{}{"tpa_operator_signers": tpaScopeSecret},
			"tpa_operator_signers": tpaScopeSecret,
		}).(map[string]interface{})
		assert.NotEqual(t, tpaScopeSecret, got["tpa_operator_signers"])
		assert.NotEqual(t, tpaScopeSecret, got["free"].(map[string]interface{})["tpa_operator_signers"])
	})

	t.Run("a list under a non-security parent: masked", func(t *testing.T) {
		got := LiveRedaction.Value("", map[string]interface{}{
			"other": map[string]interface{}{"tpa_operator_signers": []interface{}{tpaScopeSecret}},
		}).(map[string]interface{})
		inner := got["other"].(map[string]interface{})["tpa_operator_signers"].([]interface{})
		assert.NotEqual(t, tpaScopeSecret, inner[0])
	})

	t.Run("a bare string under security is not the signer list: masked", func(t *testing.T) {
		got := LiveRedaction.Value("", map[string]interface{}{
			"security": map[string]interface{}{"tpa_operator_signers": tpaScopeSecret},
		}).(map[string]interface{})
		assert.NotEqual(t, tpaScopeSecret, got["security"].(map[string]interface{})["tpa_operator_signers"])
	})
}
