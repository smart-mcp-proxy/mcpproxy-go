//go:build server

package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 US2 / FR-020 (T040, compile-red until T041): the generic `oidc`
// provider lands in ServerEditionOAuthConfig with six new keys
// (contracts/config-keys.md):
//
//	issuer_url             required for oidc; https, or http only for a loopback
//	                       host with allow_insecure_issuer: true
//	allow_insecure_issuer  loopback-only development toggle
//	scopes                 default ["openid","profile","email"]; openid appended
//	groups_claim           default "groups"
//	email_verified_policy  refuse_false (default) | require_true | ignore
//	display_name           login-button label, <= 64 chars
//
// Validate stays non-mutating (FR-039): every default belongs to ApplyDefaults
// and an unset defaulted key is never refused, so PATCH /api/v1/config and
// /config/apply admit the same document boot admits.

const (
	oidcProviderMsg    = "server_edition.oauth.provider must be one of: google, github, microsoft, oidc (got: bogus)"
	oidcIssuerReqMsg   = "server_edition.oauth.issuer_url is required when provider is oidc"
	oidcIssuerHTTPSMsg = "server_edition.oauth.issuer_url must use https (http is allowed only for a loopback host with allow_insecure_issuer: true)"
	oidcEmailPolicyMsg = "server_edition.oauth.email_verified_policy must be one of: refuse_false, require_true, ignore"
)

// oidcBlock returns an enabled server_edition block with a minimal valid
// `oidc` provider; callers mutate the OAuth sub-block per case.
func oidcBlock() *ServerEditionConfig {
	return &ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"admin@example.com"},
		OAuth: &ServerEditionOAuthConfig{
			Provider:     "oidc",
			ClientID:     "cid",
			ClientSecret: "csec",
			IssuerURL:    "https://idp.example.com/realms/dev",
		},
	}
}

func TestServerEditionOIDC_ProviderAdmitted(t *testing.T) {
	cfg := oidcBlock()
	require.NoError(t, cfg.Validate(), "provider: oidc with an https issuer_url must validate")
}

func TestServerEditionOIDC_ProviderEnumMessageNamesOIDC(t *testing.T) {
	cfg := oidcBlock()
	cfg.OAuth.Provider = "bogus"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Equal(t, oidcProviderMsg, err.Error(), "the enum message must list oidc (FR-039: same string at boot and on the write doors)")
}

func TestServerEditionOIDC_IssuerURLRequired(t *testing.T) {
	cfg := oidcBlock()
	cfg.OAuth.IssuerURL = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Equal(t, oidcIssuerReqMsg, err.Error())
}

func TestServerEditionOIDC_IssuerURLNotRequiredForLegacyProviders(t *testing.T) {
	for _, provider := range []string{"google", "github", "microsoft"} {
		t.Run(provider, func(t *testing.T) {
			cfg := oidcBlock()
			cfg.OAuth.Provider = provider
			cfg.OAuth.IssuerURL = ""
			assert.NoError(t, cfg.Validate(), "issuer_url is an oidc-only requirement")
		})
	}
}

// The https rule and its single exception: http is admitted only when BOTH
// hold — the host is loopback AND allow_insecure_issuer is true. Non-loopback
// http is refused regardless of the flag (FR-020).
func TestServerEditionOIDC_IssuerURLSchemeRule(t *testing.T) {
	cases := []struct {
		name          string
		issuer        string
		allowInsecure bool
		wantErr       string // "" = admitted
	}{
		{name: "https public host", issuer: "https://idp.example.com", wantErr: ""},
		{name: "https with path", issuer: "https://idp.example.com/realms/dev", wantErr: ""},
		{name: "http public host, flag off", issuer: "http://idp.example.com", wantErr: oidcIssuerHTTPSMsg},
		{name: "http public host, flag on (refused regardless)", issuer: "http://idp.example.com", allowInsecure: true, wantErr: oidcIssuerHTTPSMsg},
		{name: "http 127.0.0.1, flag off", issuer: "http://127.0.0.1:9000", wantErr: oidcIssuerHTTPSMsg},
		{name: "http 127.0.0.1, flag on", issuer: "http://127.0.0.1:9000", allowInsecure: true, wantErr: ""},
		{name: "http localhost, flag on", issuer: "http://localhost:9000/oidc", allowInsecure: true, wantErr: ""},
		{name: "http [::1], flag on", issuer: "http://[::1]:9000", allowInsecure: true, wantErr: ""},
		{name: "http localhost, flag off", issuer: "http://localhost:9000", wantErr: oidcIssuerHTTPSMsg},
		{name: "non-http scheme", issuer: "ftp://idp.example.com", wantErr: oidcIssuerHTTPSMsg},
		{name: "scheme-less host", issuer: "idp.example.com", wantErr: oidcIssuerHTTPSMsg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := oidcBlock()
			cfg.OAuth.IssuerURL = tc.issuer
			cfg.OAuth.AllowInsecureIssuer = tc.allowInsecure
			err := cfg.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.wantErr, err.Error())
		})
	}
}

func TestServerEditionOIDC_EmailVerifiedPolicyEnum(t *testing.T) {
	for _, policy := range []string{"", "refuse_false", "require_true", "ignore"} {
		t.Run("admits "+policy, func(t *testing.T) {
			cfg := oidcBlock()
			cfg.OAuth.EmailVerifiedPolicy = policy
			assert.NoError(t, cfg.Validate(), "unset is defaulted by ApplyDefaults, never refused by Validate")
		})
	}
	for _, policy := range []string{"bogus", "REFUSE_FALSE", "true"} {
		t.Run("refuses "+policy, func(t *testing.T) {
			cfg := oidcBlock()
			cfg.OAuth.EmailVerifiedPolicy = policy
			err := cfg.Validate()
			require.Error(t, err)
			assert.Equal(t, oidcEmailPolicyMsg, err.Error())
		})
	}
}

func TestServerEditionOIDC_GroupsClaimUnsetIsAdmitted(t *testing.T) {
	// groups_claim is "non-empty for oidc" only after ApplyDefaults; Validate
	// alone must not refuse the unset key (FR-039 non-mutating, write-door parity).
	cfg := oidcBlock()
	cfg.OAuth.GroupsClaim = ""
	assert.NoError(t, cfg.Validate())

	cfg.OAuth.GroupsClaim = "memberOf"
	assert.NoError(t, cfg.Validate())
}

func TestServerEditionOIDC_DisplayNameLength(t *testing.T) {
	for _, provider := range []string{"oidc", "google"} {
		t.Run(provider, func(t *testing.T) {
			cfg := oidcBlock()
			cfg.OAuth.Provider = provider

			cfg.OAuth.DisplayName = strings.Repeat("x", 64)
			assert.NoError(t, cfg.Validate(), "64 chars is the inclusive maximum")

			cfg.OAuth.DisplayName = strings.Repeat("x", 65)
			err := cfg.Validate()
			require.Error(t, err, "65 chars must be refused")
			assert.Contains(t, err.Error(), "server_edition.oauth.display_name")
		})
	}
}

func TestServerEditionOIDC_ValidateIsNonMutating(t *testing.T) {
	cfg := oidcBlock()
	require.NoError(t, cfg.Validate())
	assert.Nil(t, cfg.OAuth.Scopes, "Validate must not fill scopes")
	assert.Equal(t, "", cfg.OAuth.GroupsClaim, "Validate must not fill groups_claim")
	assert.Equal(t, "", cfg.OAuth.EmailVerifiedPolicy, "Validate must not fill email_verified_policy")
}

func TestServerEditionOIDC_ApplyDefaults_Scopes(t *testing.T) {
	t.Run("unset gets the full default", func(t *testing.T) {
		cfg := oidcBlock()
		cfg.ApplyDefaults()
		assert.Equal(t, []string{"openid", "profile", "email"}, cfg.OAuth.Scopes)
	})
	t.Run("openid appended when missing", func(t *testing.T) {
		cfg := oidcBlock()
		cfg.OAuth.Scopes = []string{"profile", "email", "groups"}
		cfg.ApplyDefaults()
		assert.Equal(t, []string{"profile", "email", "groups", "openid"}, cfg.OAuth.Scopes,
			"operator scopes are kept in order and openid is appended")
	})
	t.Run("openid present is left alone", func(t *testing.T) {
		cfg := oidcBlock()
		cfg.OAuth.Scopes = []string{"openid", "email"}
		cfg.ApplyDefaults()
		assert.Equal(t, []string{"openid", "email"}, cfg.OAuth.Scopes)
	})
}

func TestServerEditionOIDC_ApplyDefaults_GroupsClaimAndPolicy(t *testing.T) {
	cfg := oidcBlock()
	cfg.ApplyDefaults()
	assert.Equal(t, "groups", cfg.OAuth.GroupsClaim)
	assert.Equal(t, "refuse_false", cfg.OAuth.EmailVerifiedPolicy)

	explicit := oidcBlock()
	explicit.OAuth.GroupsClaim = "memberOf"
	explicit.OAuth.EmailVerifiedPolicy = "ignore"
	explicit.OAuth.DisplayName = "Acme SSO"
	explicit.ApplyDefaults()
	assert.Equal(t, "memberOf", explicit.OAuth.GroupsClaim, "explicit value wins")
	assert.Equal(t, "ignore", explicit.OAuth.EmailVerifiedPolicy, "explicit value wins")
	assert.Equal(t, "Acme SSO", explicit.OAuth.DisplayName, "explicit value wins")
}

func TestServerEditionOIDC_ApplyDefaultsThenValidate(t *testing.T) {
	cfg := oidcBlock()
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate(), "the boot order ApplyDefaults → Validate must admit the defaulted block")
}

// The six keys ride under the documented JSON names so the config file, the
// PATCH merge and the Web UI settings rows all agree (contracts/config-keys.md).
func TestServerEditionOIDC_JSONKeys(t *testing.T) {
	doc := `{
		"enabled": true,
		"admin_emails": ["admin@example.com"],
		"oauth": {
			"provider": "oidc",
			"client_id": "cid",
			"client_secret": "csec",
			"issuer_url": "http://127.0.0.1:9000",
			"allow_insecure_issuer": true,
			"scopes": ["openid", "email"],
			"groups_claim": "memberOf",
			"email_verified_policy": "require_true",
			"display_name": "Acme SSO"
		}
	}`
	var cfg ServerEditionConfig
	require.NoError(t, json.Unmarshal([]byte(doc), &cfg))
	require.NotNil(t, cfg.OAuth)
	assert.Equal(t, "oidc", cfg.OAuth.Provider)
	assert.Equal(t, "http://127.0.0.1:9000", cfg.OAuth.IssuerURL)
	assert.True(t, cfg.OAuth.AllowInsecureIssuer)
	assert.Equal(t, []string{"openid", "email"}, cfg.OAuth.Scopes)
	assert.Equal(t, "memberOf", cfg.OAuth.GroupsClaim)
	assert.Equal(t, "require_true", cfg.OAuth.EmailVerifiedPolicy)
	assert.Equal(t, "Acme SSO", cfg.OAuth.DisplayName)
	assert.NoError(t, cfg.Validate())

	out, err := json.Marshal(cfg.OAuth)
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &keys))
	for _, k := range []string{"issuer_url", "allow_insecure_issuer", "scopes", "groups_claim", "email_verified_policy", "display_name"} {
		assert.Contains(t, keys, k, "marshalled oauth block must carry %q", k)
	}
}

// Clone must deep-copy the new slice so a Clone-then-ApplyDefaults at setup
// never leaks the derived scopes into the runtime's own (persisted) pointer.
func TestServerEditionOIDC_CloneDeepCopiesScopes(t *testing.T) {
	orig := oidcBlock()
	orig.OAuth.Scopes = []string{"profile", "email"}

	clone := orig.Clone()
	clone.OAuth.Scopes[0] = "mutated"
	clone.OAuth.Scopes = append(clone.OAuth.Scopes, "openid")

	assert.Equal(t, []string{"profile", "email"}, orig.OAuth.Scopes, "original scopes must not observe the clone's mutation")
}

// Write-door parity (FR-039): the oidc rules are reached from Config.Validate
// and ValidateDetailed with the same message boot emits.
func TestConfigValidate_ReachesOIDCIssuerRule(t *testing.T) {
	block := oidcBlock()
	block.OAuth.IssuerURL = ""
	cfg := minimalServerEditionConfig(block)

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), oidcIssuerReqMsg)

	errs := cfg.ValidateDetailed()
	require.NotEmpty(t, errs)
	found := false
	for _, ve := range errs {
		if strings.Contains(ve.Error(), oidcIssuerReqMsg) {
			found = true
			break
		}
	}
	assert.True(t, found, "ValidateDetailed must report %q, got %v", oidcIssuerReqMsg, errs)
}
