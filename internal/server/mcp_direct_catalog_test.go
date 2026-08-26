package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 102 Phase 2 (T013/T014): the directCatalog.
//
// FR-017 makes one immutable snapshot the single source for (1) listing
// rendering in both serialization modes, (2) signature lookup, (3)
// direct-surface describe_tool resolution, and (4) pre-dispatch validation.
// Today those four read from three different places — a live projection, a
// separate directToolPermissions map, and the search index — which is the
// split-source-of-truth defect the catalog removes.
//
// These tests drive the PURE builder with a fixture []*config.ToolMetadata,
// because upstream.Manager is concrete and DiscoverTools only returns tools
// from CONNECTED clients (research.md R12) — there is no seam to inject a fake
// upstream, so a test that went through DiscoverTools could only ever assert
// the empty case.

func fixtureTools() []*config.ToolMetadata {
	return []*config.ToolMetadata{
		{
			ServerName:       "github",
			Name:             "create_issue",
			Description:      "Create an issue",
			ParamsJSON:       `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
			OutputSchemaJSON: `{"type":"object","properties":{"number":{"type":"integer"}}}`,
			Hash:             "hash-create-issue",
			Annotations:      &config.ToolAnnotations{DestructiveHint: boolPtr(false)},
		},
		{
			ServerName:  "github",
			Name:        "read_file",
			Description: "Read a file",
			ParamsJSON:  `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			Hash:        "hash-read-file",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		},
	}
}

func TestBuildDirectCatalog_OneEntryPerTool(t *testing.T) {
	cat := buildDirectCatalog(fixtureTools(), nil)
	require.NotNil(t, cat, "the builder must never return a nil catalog")

	require.Equal(t, 2, cat.Len())

	e, ok := cat.Lookup("github__create_issue")
	require.True(t, ok, "a tool must be reachable by its display name")

	assert.Equal(t, "github", e.ServerName)
	assert.Equal(t, "create_issue", e.ToolName)
	assert.Equal(t, "Create an issue", e.Description)
	assert.Equal(t, `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`, e.ParamsJSON)
	assert.Equal(t, `{"type":"object","properties":{"number":{"type":"integer"}}}`, e.OutputSchemaJSON)
	assert.Equal(t, "hash-create-issue", e.Hash)
	require.NotNil(t, e.Annotations)

	// The tier is derived from the UPSTREAM annotations, exactly as dispatch
	// derives it (D13 rule 3). Deriving it from the registered mcp.Tool instead
	// would read mcp-go's NewTool defaults, which set destructiveHint=true on
	// every tool and would classify almost the whole catalog destructive.
	assert.Equal(t, requiredPermissionForDirectTool(e.Annotations), e.RequiredPermission)
}

// TestBuildDirectCatalog_IsDeterministic pins the ordering the FR-010 built-ins
// golden will depend on. A map-ordered build would make that golden flaky.
func TestBuildDirectCatalog_IsDeterministic(t *testing.T) {
	first := buildDirectCatalog(fixtureTools(), nil).DisplayNames()
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, buildDirectCatalog(fixtureTools(), nil).DisplayNames(),
			"catalog build must be deterministic across runs")
	}
	assert.Equal(t, []string{"github__create_issue", "github__read_file"}, first,
		"entries must be sorted by display name")
}

// TestBuildDirectCatalog_WithholdsCollidingDisplayNames is D6 / D13 rule 5.
//
// Two distinct upstream pairs can flatten to ONE display name: server "a" with
// tool "b__c", and server "a__b" with tool "c". Registration silently keeps one
// today — undefined behaviour, not merely non-deterministic — and deferral makes
// it observable, because describe_tool would then have one id with two candidate
// definitions.
//
// A display name must never denote two origins, so BOTH are withheld: not
// listed, not resolvable. The spec sanctions "resolve deterministically or
// report it"; picking a winner silently is the one option ruled out, since the
// loser's caller would get the other tool's schema.
func TestBuildDirectCatalog_WithholdsCollidingDisplayNames(t *testing.T) {
	// Both flatten to "a__b__c".
	collide := []*config.ToolMetadata{
		{ServerName: "a", Name: "b__c", Description: "first origin", Hash: "h1"},
		{ServerName: "a__b", Name: "c", Description: "second origin", Hash: "h2"},
		{ServerName: "safe", Name: "tool", Description: "unaffected", Hash: "h3"},
	}

	for _, order := range [][]*config.ToolMetadata{
		collide,
		{collide[1], collide[0], collide[2]}, // reversed: withholding must not depend on order
	} {
		cat := buildDirectCatalog(order, nil)

		_, ok := cat.Lookup("a__b__c")
		assert.False(t, ok, "a colliding display name must resolve to NOTHING, not to a winner")

		assert.NotContains(t, cat.DisplayNames(), "a__b__c",
			"a colliding display name must not be listed")

		// The unaffected tool must survive — withholding is per-name, not a
		// reason to drop the whole build.
		_, ok = cat.Lookup("safe__tool")
		assert.True(t, ok, "a non-colliding tool must be unaffected by someone else's collision")

		require.Len(t, cat.Withheld(), 1, "the collision must be reported, not swallowed")
		w := cat.Withheld()[0]
		assert.Equal(t, "a__b__c", w.DisplayName)
		assert.Len(t, w.Origins, 2, "both origins must be named so an operator can fix the clash")
	}
}

// TestBuildDirectCatalog_EmptyInputIsAnEmptyCatalogNotNil is the invariant the
// DiscoverTools error path depends on: D13 rule 2 distinguishes "not in the
// catalog" (deny) from "no catalog yet" (nil, do not deny), so an empty build
// must be a real, empty catalog rather than nil.
func TestBuildDirectCatalog_EmptyInputIsAnEmptyCatalogNotNil(t *testing.T) {
	cat := buildDirectCatalog(nil, nil)
	require.NotNil(t, cat)
	assert.Equal(t, 0, cat.Len())
	assert.Empty(t, cat.DisplayNames())
}

// TestBuildDirectCatalog_DuplicateOriginIsNotACollision guards the distinction
// cross-model review caught: a collision is two DISTINCT origins flattening to
// one display name, not merely two entries under one name.
//
// The same (server, tool) appearing twice — a reindex race, a double-append —
// is not ambiguous: both entries denote the same tool. Counting entries instead
// of origins would withhold a perfectly good tool over a bookkeeping artefact,
// and the operator would see it vanish with a "colliding display name" warning
// naming the same origin twice.
func TestBuildDirectCatalog_DuplicateOriginIsNotACollision(t *testing.T) {
	dup := []*config.ToolMetadata{
		{ServerName: "github", Name: "read_file", Description: "Read a file", Hash: "h1"},
		{ServerName: "github", Name: "read_file", Description: "Read a file", Hash: "h1"},
	}

	cat := buildDirectCatalog(dup, nil)

	entry, ok := cat.Lookup("github__read_file")
	require.True(t, ok, "a duplicated origin must still be served — it is one tool, listed twice")
	assert.Equal(t, "github", entry.ServerName)
	assert.Empty(t, cat.Withheld(), "a duplicate is not a collision and must not be reported as one")
	assert.Equal(t, []string{"github__read_file"}, cat.DisplayNames(),
		"the tool must appear exactly once in the listing")
}
