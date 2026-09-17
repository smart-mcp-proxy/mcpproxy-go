//go:build server

package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 US1 (T069, compile-red until T074): the `access` block
// (contracts/config-keys.md line 30-32) lands on ServerEditionConfig as
// `Access *ServerEditionAccessConfig{GroupServers map[string][]string;
// DefaultServers []string}` (data-model.md §"Added (C)").
//
//	access                 object | absent; absent = today's Shared-only
//	                        semantics
//	access.group_servers    map[string][]string; {} default; non-empty
//	                        requires oauth.provider: "oidc" (legacy providers
//	                        yield no groups); each value a valid server name
//	                        or "*"; unknown names -> boot warning + doctor
//	                        finding only (not a Validate refusal)
//	access.default_servers  []string; absent/null/[] = no default grant
//	                        (semantics-neutral)
//
// This file pins the shape, the validation split between "invalid" (refused
// by Validate) and "unknown" (warned by DoctorFindings) names, and the
// contracts/config-keys.md:67 hot-reload liveness of `server_edition.access`
// ahead of the predicate (T075) and storage (T076) work that reads it.
const accessGroupOIDCOnlyMsg = `server_edition.access.group_servers requires oauth.provider "oidc" (legacy providers yield no groups)`

// accessBlock returns a minimal enabled, oidc-provider server_edition config
// with no access block set — callers attach one per case.
func accessBlock() *ServerEditionConfig {
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

func TestServerEditionAccess_ShapeRoundTrips(t *testing.T) {
	cfg := accessBlock()
	cfg.Access = &ServerEditionAccessConfig{
		GroupServers:   map[string][]string{"eng": {"a"}, "ops": {"a", "b"}},
		DefaultServers: []string{"a"},
	}
	require.NoError(t, cfg.Validate())

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"group_servers"`)
	assert.Contains(t, string(raw), `"default_servers"`)

	var decoded ServerEditionConfig
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NotNil(t, decoded.Access)
	assert.Equal(t, cfg.Access.GroupServers, decoded.Access.GroupServers)
	assert.Equal(t, cfg.Access.DefaultServers, decoded.Access.DefaultServers)
}

func TestServerEditionAccess_AbsentBlockValid(t *testing.T) {
	cfg := accessBlock()
	cfg.Access = nil // absent = today's Shared-only semantics
	assert.NoError(t, cfg.Validate())
}

func TestServerEditionAccess_DefaultServersNilVsEmptyEquivalence(t *testing.T) {
	// absent / null / [] default_servers are semantics-neutral by definition
	// (contracts/config-keys.md:32): none refuse Validate, and every form
	// reads back empty.
	const base = `{"enabled":true,"admin_emails":["admin@example.com"],"oauth":{"provider":"oidc","client_id":"cid","client_secret":"csec","issuer_url":"https://idp.example.com"},"access":{"group_servers":{}%s}}`
	cases := []struct {
		name string
		raw  string
	}{
		{"absent", ""},
		{"null", `,"default_servers":null`},
		{"empty", `,"default_servers":[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg ServerEditionConfig
			require.NoError(t, json.Unmarshal([]byte(strings.Replace(base, "%s", tc.raw, 1)), &cfg))
			require.NoError(t, cfg.Validate())
			require.NotNil(t, cfg.Access)
			assert.Empty(t, cfg.Access.DefaultServers)
		})
	}
}

func TestServerEditionAccess_GroupServersRequiresOIDC(t *testing.T) {
	cfg := accessBlock()
	cfg.OAuth.Provider = "google"
	cfg.Access = &ServerEditionAccessConfig{GroupServers: map[string][]string{"eng": {"a"}}}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Equal(t, accessGroupOIDCOnlyMsg, err.Error())
}

func TestServerEditionAccess_EmptyGroupServersMapAdmittedForLegacyProvider(t *testing.T) {
	// The empty map is the documented default ({}); it is not "non-empty" and
	// must not trip the oidc-only rule for a legacy provider.
	cfg := accessBlock()
	cfg.OAuth.Provider = "github"
	cfg.Access = &ServerEditionAccessConfig{GroupServers: map[string][]string{}}
	assert.NoError(t, cfg.Validate())
}

func TestServerEditionAccess_DefaultServersDoesNotRequireOIDC(t *testing.T) {
	// Only group_servers is gated on the provider (contracts/config-keys.md:31
	// names group_servers only); default_servers has no provider dependency.
	cfg := accessBlock()
	cfg.OAuth.Provider = "microsoft"
	cfg.OAuth.TenantID = "common"
	cfg.Access = &ServerEditionAccessConfig{DefaultServers: []string{"a"}}
	assert.NoError(t, cfg.Validate())
}

func TestServerEditionAccess_InvalidServerNamesRefused(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty string", ""},
		{"reserved colon routing separator", "a:b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := accessBlock()
			cfg.Access = &ServerEditionAccessConfig{GroupServers: map[string][]string{"eng": {tc.value}}}
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "eng")
			assert.Contains(t, err.Error(), "invalid server name")
		})
	}
}

func TestServerEditionAccess_WildcardIsAValidServerName(t *testing.T) {
	// "*" expands to every shared server (entitlement-predicate.md §1); it
	// must never be treated as an invalid name.
	cfg := accessBlock()
	cfg.Access = &ServerEditionAccessConfig{GroupServers: map[string][]string{"eng": {"*"}}}
	assert.NoError(t, cfg.Validate())
}

func TestServerEditionAccess_UnknownNamesWarnNotRefused(t *testing.T) {
	// Unknown names are shape-valid but do not match any configured server;
	// contracts/config-keys.md:31 says they "warn (+ doctor finding) only" —
	// Validate must NOT refuse them.
	cfg := accessBlock()
	cfg.Access = &ServerEditionAccessConfig{
		GroupServers:   map[string][]string{"eng": {"ghost-server"}},
		DefaultServers: []string{"another-ghost"},
	}
	assert.NoError(t, cfg.Validate(), "a shape-valid but unconfigured name must not refuse Validate")

	full := &Config{
		Listen:        "127.0.0.1:8080",
		ServerEdition: cfg,
		Servers: []*ServerConfig{
			{Name: "a", Shared: true, Enabled: true},
		},
	}
	findings := DoctorFindings(full)
	joined := strings.Join(findings, "\n")
	assert.Contains(t, joined, "ghost-server", "the doctor finding must name the unknown group_servers entry")
	assert.Contains(t, joined, "another-ghost", "the doctor finding must name the unknown default_servers entry")
}

func TestServerEditionAccess_KnownNamesDoNotWarn(t *testing.T) {
	cfg := accessBlock()
	cfg.Access = &ServerEditionAccessConfig{
		GroupServers:   map[string][]string{"eng": {"a"}},
		DefaultServers: []string{"a"},
	}
	full := &Config{
		Listen:        "127.0.0.1:8080",
		ServerEdition: cfg,
		Servers: []*ServerConfig{
			{Name: "a", Shared: true, Enabled: true},
		},
	}
	findings := DoctorFindings(full)
	for _, f := range findings {
		assert.NotContains(t, f, "\"a\"", "a real, shared server name must never be reported as unknown")
	}
}
