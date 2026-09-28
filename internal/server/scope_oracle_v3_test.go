package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108 (Profiles v3) T017: extends the Spec 105 two-fixture differential
// oracle (scope_differential_test.go's newScopeFixture/contracts/
// differential-oracle.md) with a v3 tool policy applied on top — proving
// that hidden_by_profile (and every other retrieve_tools field) is IDENTICAL
// between a fixture that contains only the caller's own server ("a") and one
// that also carries hidden servers ("b", "a__b") the caller cannot see at
// all, for the SAME caller and profile. A hidden server's mere existence
// (let alone its own excluded tools) must never perturb hidden_by_profile —
// that count is defined over the caller's effective server scope only
// (contracts/mcp-tools.md).
//
// A dedicated builder is used rather than newScopeFixture itself: that
// helper's proxy has no config hook to add Profiles after construction, and
// this oracle's caller must be profile-scoped (a v3 policy resolved through
// a real profileIndex), not merely token-scoped.

type scopeOracleV3Fixture struct {
	proxy *MCPProxyServer
}

// newScopeOracleV3Fixture mirrors newScopeFixture's {a} / {a,b,a__b} split
// letter-for-letter on server "a"'s own tools (readSpec/writeSpec/
// destructiveSpec over the same names), with one v3 profile ("cap-read-a",
// max_tier read, servers=["a"]) — so the excluded tools (write_thing,
// destroy_thing, ns:erase) are exactly the ones a plain token-scope oracle
// would never distinguish from an admitted one.
func newScopeOracleV3Fixture(t *testing.T, full bool) *scopeOracleV3Fixture {
	t.Helper()
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: true}}
		if full {
			cfg.Servers = append(cfg.Servers,
				&config.ServerConfig{Name: "b", Enabled: true},
				&config.ServerConfig{Name: "a__b", Enabled: true},
			)
		}
		cfg.Profiles = []config.ProfileConfig{
			{Name: "cap-read-a", Servers: []string{"a"}, MaxTier: "read"},
		}
	})

	startCountingUpstream(t, proxy, rt, "a",
		readSpec("read_thing"), writeSpec("write_thing"), destructiveSpec("destroy_thing"),
		readSpec("erase"), writeSpec("ns:erase"))
	for _, tool := range []*config.ToolMetadata{
		{Name: "a:read_thing", ServerName: "a", Description: "read_thing", ParamsJSON: "{}", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "a:write_thing", ServerName: "a", Description: "write_thing", ParamsJSON: "{}", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}},
		{Name: "a:destroy_thing", ServerName: "a", Description: "destroy_thing", ParamsJSON: "{}", Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)}},
		{Name: "a:erase", ServerName: "a", Description: "erase", ParamsJSON: "{}", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "a:ns:erase", ServerName: "a", Description: "ns_erase", ParamsJSON: "{}", Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}},
	} {
		require.NoError(t, proxy.index.IndexTool(tool))
	}

	if full {
		sentB := "SENTINEL_scopeOracleV3B_71a2"
		sentAB := "SENTINEL_scopeOracleV3AB_39fe"
		startCountingUpstream(t, proxy, rt, "b",
			toolSpec{Name: sentB + "_tool", Description: "Handles " + sentB,
				Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}})
		startCountingUpstream(t, proxy, rt, "a__b",
			toolSpec{Name: sentAB + "_tool", Description: "Handles " + sentAB,
				Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}})
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "b:" + sentB + "_tool", ServerName: "b", Description: "Handles " + sentB, ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		}))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			Name: "a__b:" + sentAB + "_tool", ServerName: "a__b", Description: "Handles " + sentAB, ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		}))
	}

	require.NoError(t, proxy.index.RebuildProfileFromShared("cap-read-a", []string{"a"}))

	return &scopeOracleV3Fixture{proxy: proxy}
}

// TestScopeOracleV3_HiddenByProfileIdenticalAcrossFixtures is T017: the SAME
// pinned caller run against the narrow and full fixtures must get byte-
// identical retrieve_tools responses, hidden_by_profile included, for every
// query the fixture's own ("a"-server) tool set matches.
func TestScopeOracleV3_HiddenByProfileIdenticalAcrossFixtures(t *testing.T) {
	narrow := newScopeOracleV3Fixture(t, false)
	full := newScopeOracleV3Fixture(t, true)

	pinned := pinnedProfileCtx("cap-read-a")

	// The two sentinel-matching queries are the actual scope-leak probe
	// (zcode review F2): the five original queries share no token at all
	// with either hidden server's tool name/description, so a regression
	// that let a hidden server's tool reach the pinned caller's response
	// could pass every assertion below completely unnoticed — the oracle
	// was only ever comparing two empty result sets to each other. These two
	// queries are chosen to hit deterministically (proven by the admin
	// control subtest) and must return NOTHING for the pinned caller.
	queries := []string{
		"read_thing", "write_thing", "destroy_thing", "erase", "ns_erase",
		"SENTINEL_scopeOracleV3B_71a2_tool", "SENTINEL_scopeOracleV3AB_39fe_tool",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			narrowResp := callRetrieveToolsV3(t, narrow.proxy, pinned, query, 10)
			fullResp := callRetrieveToolsV3(t, full.proxy, pinned, query, 10)

			require.NotNil(t, narrowResp.HiddenByProfile)
			require.NotNil(t, fullResp.HiddenByProfile)
			assert.Equal(t, *narrowResp.HiddenByProfile, *fullResp.HiddenByProfile,
				"a hidden server's existence must never perturb hidden_by_profile")
			assert.Equal(t, narrowResp.Total, fullResp.Total)

			narrowNames := map[string]bool{}
			for _, tl := range narrowResp.Tools {
				narrowNames[tl["name"].(string)] = true
			}
			fullNames := map[string]bool{}
			for _, tl := range fullResp.Tools {
				name := tl["name"].(string)
				assert.NotContains(t, name, "SENTINEL", "a hidden server's sentinel tool must never appear")
				fullNames[name] = true
			}
			assert.Equal(t, narrowNames, fullNames)
		})
	}

	t.Run("admin control: the full fixture really does contain the hidden sentinel tools", func(t *testing.T) {
		resp := callRetrieveToolsV3(t, full.proxy, adminCtx(), "SENTINEL_scopeOracleV3B_71a2_tool", 10)
		require.NotEmpty(t, resp.Tools, "fixture premise: an unscoped administrator must see the hidden sentinel tool")
	})
}

// The same two-fixture non-disclosure oracle must hold for the newer
// resolution sources added by client credentials and anonymous_profile.
// Previously T017 only exercised an agent pin, leaving those sources
// unprotected against hidden-server influence on hidden_by_profile/results.
func TestScopeOracleV3_NewResolutionSourcesMatchAcrossFixtures(t *testing.T) {
	narrow := newScopeOracleV3Fixture(t, false)
	full := newScopeOracleV3Fixture(t, true)
	narrow.proxy.currentConfig().AnonymousProfile = "cap-read-a"
	full.proxy.currentConfig().AnonymousProfile = "cap-read-a"

	sources := map[string]struct {
		narrow context.Context
		full   context.Context
	}{
		"locked client pin":         {clientCtx("desktop", "cap-read-a", "locked"), clientCtx("desktop", "cap-read-a", "locked")},
		"switchable client binding": {clientCtx("desktop", "cap-read-a", "switchable"), clientCtx("desktop", "cap-read-a", "switchable")},
		"anonymous profile":         {anonCtx(), anonCtx()},
	}
	for source, callers := range sources {
		t.Run(source, func(t *testing.T) {
			for _, query := range []string{"read_thing", "write_thing", "destroy_thing", "erase", "ns_erase"} {
				t.Run(query, func(t *testing.T) {
					narrowResp := callRetrieveToolsV3(t, narrow.proxy, callers.narrow, query, 10)
					fullResp := callRetrieveToolsV3(t, full.proxy, callers.full, query, 10)
					assert.Nil(t, narrowResp.Profile, "credential and anonymous resolution sources never reveal the base profile")
					assert.Nil(t, fullResp.Profile, "credential and anonymous resolution sources never reveal the base profile")
					left, err := json.Marshal(narrowResp)
					require.NoError(t, err)
					right, err := json.Marshal(fullResp)
					require.NoError(t, err)
					assert.JSONEq(t, string(left), string(right), "hidden servers must not perturb discovery for %s", source)
				})
			}
		})
	}
}
