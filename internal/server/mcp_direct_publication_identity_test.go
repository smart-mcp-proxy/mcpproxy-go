package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 PR F — scope-direct-publication (FR-008, gaps FR008-G1..G7,
// T080-T086a).
//
// The sibling skew tests in mcp_direct_skew_test.go exercise the internal
// filter functions directly (f.listed, f.registeredHandler). This file adds
// the assertions those did not carry before this PR: the FULL DEFINITION
// served to an authorized caller during a seam, both serialization modes for
// every gap, and the real mcp-go protocol path end to end (directServer.
// HandleMessage), so the guarantee is proven at the wire, not only against
// the filter functions that produce it.

// mustReadTool returns the mcp.Tool registered under display, and requires it
// to exist.
func mustReadTool(t *testing.T, f *skewFixture, display string) mcp.Tool {
	t.Helper()
	st, ok := f.proxy.directServer.ListTools()[display]
	require.Truef(t, ok, "%q must be registered", display)
	return st.Tool
}

// T080 (FR008-G1): the origin-flip fixture, in both serialization modes,
// asserting the FULL served definition (not just presence/absence) for every
// caller kind during the seam.
func TestDirectPublication_OriginFlip_DefinitionMatchesProducingIdentity(t *testing.T) {
	for _, mode := range []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred} {
		t.Run(mode, func(t *testing.T) {
			const display = "a__b__c"
			oldOrigin := []*config.ToolMetadata{
				skewTool("a", "b__c", "SENTINEL_OLD_OWNER_A description",
					`{"type":"object","properties":{"old_only_field":{"type":"string"}},"required":["old_only_field"]}`,
					&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
			}
			newOrigin := []*config.ToolMetadata{
				skewTool("a__b", "c", "SENTINEL_NEW_OWNER_AB description",
					`{"type":"object","properties":{"new_only_field":{"type":"integer"}},"required":["new_only_field"]}`,
					&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
			}

			f := newSkewFixtureInMode(t, oldOrigin, mode)
			require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a__b", Enabled: true}))
			require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a__b", ToolName: "c", Status: storage.ToolApprovalStatusApproved,
			}))

			oldOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "a-only",
				AllowedServers: []string{"a"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			})
			wildcard := auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AgentName: "wildcard",
				AllowedServers: []string{"*"},
				Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			})
			admin := auth.WithAuthContext(context.Background(), auth.AdminContext())

			f.rebuildPaused(t, newOrigin, func() {
				listedOld := f.listed(oldOnly)
				assert.NotContains(t, listedOld, display,
					"a-only must never see the new owner's tool during the seam, in either mode")

				for name, ctx := range map[string]context.Context{"wildcard-scoped agent": wildcard, "administrator": admin} {
					listed := f.listed(ctx)
					require.Containsf(t, listed, display, "%s must see it", name)

					tool := mustReadTool(t, f, display)
					assert.Containsf(t, tool.Description, "SENTINEL_NEW_OWNER_AB", "%s: served description must be the NEW owner's", name)
					assert.NotContainsf(t, tool.Description, "SENTINEL_OLD_OWNER_A", "%s: must never carry the OLD owner's description", name)

					if mode == config.DirectToolResponseModeFull {
						raw, err := json.Marshal(tool.InputSchema)
						require.NoError(t, err)
						assert.Containsf(t, string(raw), "new_only_field", "%s: schema must be the NEW owner's (S2)", name)
						assert.NotContainsf(t, string(raw), "old_only_field", "%s: must never carry the OLD owner's schema (S1)", name)
					}
				}
			})

			// After the publish the definitions agree everywhere.
			for name, ctx := range map[string]context.Context{"wildcard": wildcard, "admin": admin} {
				require.Containsf(t, f.listed(ctx), display, "%s", name)
			}
		})
	}
}

// T082 (FR008-G3): the reverse flip (new-scope-restricted origin -> back to
// old, wider-visible one) and a plain addition, in the SAME window, proving
// visibility WIDENS immediately too — the stamp is not merely conservative,
// it tracks the CURRENT registration in both directions.
func TestDirectPublication_ReverseFlipAndPlainAddition_VisibleDuringWindow(t *testing.T) {
	const flipped = "a__b__c"

	// Start on the NEW (restricted) origin: server "a__b", tool "c" — out of
	// scope for an a-only token.
	restricted := []*config.ToolMetadata{
		skewTool("a__b", "c", "restricted origin", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	f := newSkewFixture(t, restricted)
	require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a", Enabled: true}))
	require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a", ToolName: "b__c", Status: storage.ToolApprovalStatusApproved,
	}))
	require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "b", Enabled: true}))
	require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "b", ToolName: "x", Status: storage.ToolApprovalStatusApproved,
	}))

	aOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	bOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "b-only",
		AllowedServers: []string{"b"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	require.NotContains(t, f.listed(aOnly), flipped, "precondition: a-only cannot see the restricted origin")

	// Reverse flip to server "a" tool "b__c" (in scope for a-only), PLUS a
	// plain addition of "b__x" (in scope for b-only), in the same rebuild.
	reversedPlusAddition := []*config.ToolMetadata{
		skewTool("a", "b__c", "reverse-flipped to a", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		skewTool("b", "x", "plain addition on b", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}

	f.rebuildPaused(t, reversedPlusAddition, func() {
		assert.Contains(t, f.listed(aOnly), flipped,
			"the reverse flip is registered before this pause starts, so a-only sees it immediately, not one generation late")
		assert.Contains(t, f.listed(bOnly), "b__x",
			"the plain addition on b's own server is visible to b-only immediately")
		assert.NotContains(t, f.listed(bOnly), flipped, "b-only must not see the (now a-owned) flipped tool")
		assert.NotContains(t, f.listed(aOnly), "b__x", "a-only must not see b's addition")
	})

	assert.Contains(t, f.listed(aOnly), flipped)
	assert.Contains(t, f.listed(bOnly), "b__x")
}

// T083 (FR008-G4), deferred-mode companion to
// TestSkew_AnnotationsOnlyChangeIsStaleButNeverAdmitsTheCall: a same-owner
// tier change from read to destructive must withhold the tool from a
// read-scoped token immediately in DEFERRED mode too, not only full mode —
// deferred rendering must not reopen the window full mode closed.
func TestDirectPublication_TierChangeWithheldDuringSeam_DeferredMode(t *testing.T) {
	before := []*config.ToolMetadata{
		skewTool("fs", "purge", "Purge", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	after := []*config.ToolMetadata{
		skewTool("fs", "purge", "Purge", `{"type":"object"}`, &config.ToolAnnotations{DestructiveHint: boolPtr(true)}),
	}

	f := newSkewFixtureInMode(t, before, config.DirectToolResponseModeDeferred)
	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "reader",
		AllowedServers: []string{"fs"},
		Permissions:    []string{auth.PermRead},
	})

	require.Contains(t, f.listed(readOnly), "fs__purge", "precondition: visible while still read-tier")

	f.rebuildPaused(t, after, func() {
		assert.NotContains(t, f.listed(readOnly), "fs__purge",
			"deferred mode must withhold the tool as soon as the destructive registration lands, exactly like full mode")

		result, err := f.registeredHandler(t, "fs__purge")(readOnly, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "fs__purge"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Permission denied")
	})
}

// directHandleMessage drives one raw JSON-RPC request through a proxy's
// direct server and returns the decoded envelope (result or error).
func directHandleMessage(t *testing.T, f *skewFixture, ctx context.Context, id int, method, params string) map[string]interface{} {
	t.Helper()
	raw := []byte(`{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"` + method + `","params":` + params + `}`)
	encoded, err := json.Marshal(f.proxy.directServer.HandleMessage(ctx, raw))
	require.NoError(t, err)
	var envelope map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &envelope))
	return envelope
}

func itoa(i int) string {
	// Tiny local helper so this file needs no strconv import for one call site.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// T084 (FR008-G5), the protocol-level proof: inside the origin-flip pause, an
// a-only token's tools/call for the flipped display name through the REAL
// mcp-go protocol path (directServer.HandleMessage, which re-evaluates the
// tool filters at call time — D7) must produce the BYTE-IDENTICAL envelope
// (code, message, data) that the same call gets when "a__b" never existed at
// all, and the caller-supplied name is the only thing echoed in either case.
func TestDirectPublication_InSeamCallEnvelopeMatchesUnregisteredName(t *testing.T) {
	const display = "a__b__c"

	oldOrigin := []*config.ToolMetadata{
		skewTool("a", "b__c", "Owned by a", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	newOrigin := []*config.ToolMetadata{
		skewTool("a__b", "c", "Owned by a__b", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}

	oldOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	// Fixture 1: the real seam, mid-flip.
	f := newSkewFixture(t, oldOrigin)
	require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a__b", Enabled: true}))
	require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a__b", ToolName: "c", Status: storage.ToolApprovalStatusApproved,
	}))

	var seamEnvelope map[string]interface{}
	f.rebuildPaused(t, newOrigin, func() {
		require.NotNil(t, f.proxy.directServer.HandleMessage(oldOnly,
			[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)))
		seamEnvelope = directHandleMessage(t, f, oldOnly, 2, "tools/call", `{"name":"`+display+`","arguments":{}}`)
	})

	// Fixture 2: the display name "a__b__c" is registered under NEITHER
	// interpretation at all — server "a" exposes a different tool, and there
	// is no "a__b" — the genuinely unregistered-name case.
	absentFixtureTools := []*config.ToolMetadata{
		skewTool("a", "unrelated", "Owned by a, unrelated tool", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	g := newSkewFixture(t, absentFixtureTools)
	require.NotNil(t, g.proxy.directServer.HandleMessage(oldOnly,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)))
	absentEnvelope := directHandleMessage(t, g, oldOnly, 2, "tools/call", `{"name":"`+display+`","arguments":{}}`)

	require.NotNil(t, seamEnvelope["error"], "in-seam call to the now-unauthorized name must be a JSON-RPC error: %v", seamEnvelope)
	require.NotNil(t, absentEnvelope["error"], "precondition: %v", absentEnvelope)

	seamErr := seamEnvelope["error"].(map[string]interface{})
	absentErr := absentEnvelope["error"].(map[string]interface{})

	assert.Equal(t, absentErr["code"], seamErr["code"], "the JSON-RPC error code must match an unregistered name's")
	assert.Equal(t, float64(mcp.INVALID_PARAMS), seamErr["code"], "and specifically be INVALID_PARAMS (-32602)")
	assert.Equal(t, absentErr["message"], seamErr["message"],
		"the message is byte-identical: the caller-supplied name is the only thing echoed, in BOTH fixtures")
	assert.Equal(t, absentErr["data"], seamErr["data"], "data must match too")

	// "a__b__c" (the caller-supplied name, which D12 permits echoing) happens
	// to contain "a__b" as a raw substring, so the disclosure check targets
	// the OLD message's own distinguishing vocabulary — a "server" field, or
	// the word "owner" — never that coincidental substring.
	rawSeam, err := json.Marshal(seamErr)
	require.NoError(t, err)
	assert.NotContains(t, string(rawSeam), "owner", "no owner metadata may appear anywhere in the envelope (D12)")
	assert.NotContains(t, string(rawSeam), `"server"`)
	assert.NotContains(t, string(rawSeam), "does not have access")
}

// T081 (FR008-G2): during a plain-addition seam, two structurally awkward new
// names — one with a leading "__" (an empty server name) and one with an
// empty raw tool name (withheld at the catalog, FR008-G7) — must never be
// listed for an a-only token, carrying a sentinel to prove no content leaks
// either.
func TestDirectPublication_SeamAddition_StructurallyAwkwardNamesNeverListedOutOfScope(t *testing.T) {
	base := []*config.ToolMetadata{
		skewTool("fs", "read", "Read a file", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	f := newSkewFixture(t, base)

	aOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	added := append(base,
		// An empty SERVER name: FormatDirectToolName("", "a__review") =
		// "__a__review", which fails ParseDirectToolName (the "__" separator
		// sits at index 0) — structurally identical to what used to be
		// misclassified as a built-in.
		skewTool("", "a__review", "SENTINEL_EMPTY_SERVER description", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		// An empty RAW TOOL name (FR008-G7): "b__", withheld at the catalog.
		skewTool("b", "", "SENTINEL_EMPTY_RAW_NAME description", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	)
	require.Equal(t, "__a__review", FormatDirectToolName("", "a__review"))
	require.Equal(t, "b__", FormatDirectToolName("b", ""))

	f.rebuildPaused(t, added, func() {
		listed := f.listed(aOnly)
		assert.NotContains(t, listed, "__a__review", "an empty-server-name tool must not be listed for a-only")
		assert.NotContains(t, listed, "b__", "an empty-raw-name tool is never even admitted to the catalog")

		for name := range listed {
			assert.NotContains(t, name, "SENTINEL", "no sentinel-bearing tool name leaks to a-only")
		}
	})
}
