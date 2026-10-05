package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthEndpointOverrides_JSONRoundTripAndMerge(t *testing.T) {
	in := &OAuthConfig{
		AuthorizationEndpoint: "https://idp.example.com/authorize",
		TokenEndpoint:         "https://idp.example.com/token",
		RegistrationEndpoint:  "https://idp.example.com/register",
		AuthServerMetadataURL: "https://idp.example.com/.well-known/custom",
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"authorization_endpoint"`)
	assert.Contains(t, string(data), `"token_endpoint"`)
	assert.Contains(t, string(data), `"registration_endpoint"`)
	assert.Contains(t, string(data), `"auth_server_metadata_url"`)

	var out OAuthConfig
	require.NoError(t, json.Unmarshal(data, &out))
	assert.Equal(t, *in, out)

	// omitempty: absent when unset.
	empty, err := json.Marshal(&OAuthConfig{ClientID: "x"})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), "endpoint")
	assert.NotContains(t, string(empty), "auth_server_metadata_url")

	// copy + merge keep every field.
	assert.Equal(t, in, copyOAuthConfig(in))
	merged := MergeOAuthConfig(&OAuthConfig{ClientID: "a"}, in, false)
	assert.Equal(t, in.AuthorizationEndpoint, merged.AuthorizationEndpoint)
	assert.Equal(t, in.TokenEndpoint, merged.TokenEndpoint)
	assert.Equal(t, in.RegistrationEndpoint, merged.RegistrationEndpoint)
	assert.Equal(t, in.AuthServerMetadataURL, merged.AuthServerMetadataURL)
	assert.Equal(t, "a", merged.ClientID)
	// patch with empty value keeps the base override
	kept := MergeOAuthConfig(in, &OAuthConfig{ClientID: "b"}, false)
	assert.Equal(t, in.TokenEndpoint, kept.TokenEndpoint)
}

func TestOAuthConfigChanged_EndpointOverrides(t *testing.T) {
	base := func() *OAuthConfig { return &OAuthConfig{ClientID: "c"} }
	for name, mutate := range map[string]func(o *OAuthConfig){
		"authorization_endpoint":   func(o *OAuthConfig) { o.AuthorizationEndpoint = "https://a.example/authorize" },
		"token_endpoint":           func(o *OAuthConfig) { o.TokenEndpoint = "https://a.example/token" },
		"registration_endpoint":    func(o *OAuthConfig) { o.RegistrationEndpoint = "https://a.example/register" },
		"auth_server_metadata_url": func(o *OAuthConfig) { o.AuthServerMetadataURL = "https://a.example/meta" },
	} {
		t.Run(name, func(t *testing.T) {
			n := base()
			mutate(n)
			assert.True(t, OAuthConfigChanged(base(), n))
			assert.False(t, OAuthConfigChanged(n, copyOAuthConfig(n)))
		})
	}
}

func TestValidateOAuthEndpointOverrides(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"https ok", "https://idp.example.com/oauth2/v1/authorize", false},
		{"https with port ok", "https://idp.example.com:8443/authorize", false},
		{"http localhost ok", "http://localhost:9000/authorize", false},
		{"http 127.0.0.5 ok", "http://127.0.0.5:9000/authorize", false},
		{"http ipv6 loopback ok", "http://[::1]:9000/authorize", false},
		{"http public rejected", "http://idp.example.com/authorize", true},
		{"relative rejected", "/authorize", true},
		{"scheme-relative rejected", "//idp.example.com/authorize", true},
		{"fragment rejected", "https://idp.example.com/authorize#frag", true},
		{"userinfo rejected", "https://user:pw@idp.example.com/authorize", true},
		{"query rejected", "https://idp.example.com/authorize?x=1", true},
		{"empty host rejected", "https:///authorize", true},
		{"ftp rejected", "ftp://idp.example.com/authorize", true},
		{"javascript rejected", "javascript:alert(1)", true},
	}
	for _, tt := range tests {
		for _, field := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint", "auth_server_metadata_url"} {
			t.Run(tt.name+"/"+field, func(t *testing.T) {
				o := &OAuthConfig{}
				switch field {
				case "authorization_endpoint":
					o.AuthorizationEndpoint = tt.value
				case "token_endpoint":
					o.TokenEndpoint = tt.value
				case "registration_endpoint":
					o.RegistrationEndpoint = tt.value
				default:
					o.AuthServerMetadataURL = tt.value
				}
				errs := ValidateOAuthEndpointOverrides(o)
				if tt.wantErr {
					require.Len(t, errs, 1)
					assert.Equal(t, "oauth."+field, errs[0].Field)
					// The offending value (may carry credentials) is never echoed.
					assert.NotContains(t, errs[0].Message, "pw@")
				} else {
					assert.Empty(t, errs)
				}
			})
		}
	}
	assert.Empty(t, ValidateOAuthEndpointOverrides(nil))
	assert.Empty(t, ValidateOAuthEndpointOverrides(&OAuthConfig{}))
}

func TestOAuthEndpointOverrides_WriteTimeVsLoadTime(t *testing.T) {
	bad := &ServerConfig{Name: "s", URL: "https://x.example/mcp", Protocol: "http",
		OAuth: &OAuthConfig{TokenEndpoint: "http://idp.example.com/token", AuthorizationEndpoint: "https://ok.example/authorize"}}

	// Write time: OAuthConfig.Validate and Config.ValidateDetailed reject it.
	require.Error(t, bad.OAuth.Validate())
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{bad}
	var found bool
	for _, e := range cfg.ValidateDetailed() {
		if e.Field == "mcpServers[0].oauth.token_endpoint" {
			found = true
		}
	}
	assert.True(t, found, "ValidateDetailed must report the bad override")

	// Boot path (Validate) must not fail on it.
	assert.NoError(t, cfg.Validate())

	// Load-time sanitize drops only the invalid override.
	dropped := NormalizeOAuthEndpointOverrides(cfg)
	require.Len(t, dropped, 1)
	assert.Equal(t, "s", dropped[0].Server)
	assert.Equal(t, []string{"token_endpoint"}, dropped[0].Dropped)
	assert.Empty(t, bad.OAuth.TokenEndpoint)
	assert.Equal(t, "https://ok.example/authorize", bad.OAuth.AuthorizationEndpoint)
	assert.Empty(t, NormalizeOAuthEndpointOverrides(cfg), "idempotent")
	assert.Empty(t, NormalizeOAuthEndpointOverrides(nil))
}

func TestValidateOAuthEndpointOverrides_WhitespaceIsRejectedNotTrimmed(t *testing.T) {
	for _, v := range []string{" https://idp.example.com/token", "https://idp.example.com/token ", " ", "\thttps://idp.example.com/token"} {
		errs := ValidateOAuthEndpointOverrides(&OAuthConfig{TokenEndpoint: v})
		assert.Len(t, errs, 1, "%q", v)
	}
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{{Name: "w", URL: "https://x.example/mcp", Protocol: "http", OAuth: &OAuthConfig{AuthServerMetadataURL: " "}}}
	require.Len(t, NormalizeOAuthEndpointOverrides(cfg), 1)
	assert.Empty(t, cfg.Servers[0].OAuth.AuthServerMetadataURL)
}
