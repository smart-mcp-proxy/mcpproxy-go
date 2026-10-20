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

// The generic walker never exempts anything by field name: it also redacts
// audit payloads and request bodies, where a "security.tpa_operator_signers"
// shape is attacker-chosen and may carry a real credential (Sol 6.1 review,
// round 1). The exemption lives only in the typed config read door.
func TestRedactionValue_NeverExemptsTheSignerPathInGenericPayloads(t *testing.T) {
	const credential = "mcp_agt_1234567890abcdef1234567890abcdef"
	payload := func() map[string]interface{} {
		return map[string]interface{}{
			"request": map[string]interface{}{
				"security": map[string]interface{}{"tpa_operator_signers": []interface{}{credential}},
			},
			"security": map[string]interface{}{"tpa_operator_signers": []interface{}{credential}},
		}
	}
	for name, r := range map[string]Redaction{"live": LiveRedaction, "audit": AuditRedaction} {
		t.Run(name, func(t *testing.T) {
			got := r.Value("", payload()).(map[string]interface{})
			top := got["security"].(map[string]interface{})["tpa_operator_signers"].([]interface{})
			nested := got["request"].(map[string]interface{})["security"].(map[string]interface{})["tpa_operator_signers"].([]interface{})
			assert.NotEqual(t, credential, top[0], "top-level shape is masked in a generic payload")
			assert.NotEqual(t, credential, nested[0], "nested shape is masked")
		})
	}
}

// Only values that ARE public keys come back verbatim from the config read
// door; anything else in the list is masked like any secret.
func TestRedactedConfig_OnlyPublicKeyShapedSignersAreReadable(t *testing.T) {
	signer := "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	cfg := config.DefaultConfig()
	cfg.Security = &config.SecurityConfig{TPAOperatorSigners: []string{signer, tpaScopeSecret}}
	masked := RedactedConfig(cfg)
	require.NotNil(t, masked)
	require.Len(t, masked.Security.TPAOperatorSigners, 2)
	assert.Equal(t, signer, masked.Security.TPAOperatorSigners[0])
	assert.NotEqual(t, tpaScopeSecret, masked.Security.TPAOperatorSigners[1])
	assert.Equal(t, []string{signer, tpaScopeSecret}, cfg.Security.TPAOperatorSigners, "the source config is untouched")
}
