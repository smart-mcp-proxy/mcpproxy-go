package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// EffectiveAnnotationsForTool — PLAN §3.1 (BDD, mutation killing)
// ---------------------------------------------------------------------------

func TestEffectiveAnnotations_WildcardThenExact(t *testing.T) {
	// Given upstream destructive:true, overrides {"*":{destructive:false}, "act":{destructive:true}}
	// When EffectiveAnnotationsForTool is called for "act" and for "navigate"
	// Then "act" must be true (exact wins over wildcard), "navigate" false (wildcard applied)
	bTrue := true
	bFalse := false
	upstream := &ToolAnnotations{DestructiveHint: &bTrue}
	overrides := map[string]*ToolAnnotations{
		"*":   {DestructiveHint: &bFalse},
		"act": {DestructiveHint: &bTrue},
	}
	// act: exact overrides wildcard
	effAct := EffectiveAnnotationsForTool(overrides, "act", upstream)
	require.NotNil(t, effAct)
	require.NotNil(t, effAct.DestructiveHint)
	assert.True(t, *effAct.DestructiveHint, "exact must win over wildcard for act")
	// navigate: only wildcard applies
	effNav := EffectiveAnnotationsForTool(overrides, "navigate", upstream)
	require.NotNil(t, effNav)
	require.NotNil(t, effNav.DestructiveHint)
	assert.False(t, *effNav.DestructiveHint, "wildcard must apply to navigate")
}

func TestEffectiveAnnotations_NilUpstream(t *testing.T) {
	// Given nil upstream and overrides "*":{readOnly:true}
	// When EffectiveAnnotationsForTool is called
	// Then result must have readOnly:true (created from empty base)
	bTrue := true
	overrides := map[string]*ToolAnnotations{"*": {ReadOnlyHint: &bTrue}}
	eff := EffectiveAnnotationsForTool(overrides, "any_tool", nil)
	require.NotNil(t, eff)
	require.NotNil(t, eff.ReadOnlyHint)
	assert.True(t, *eff.ReadOnlyHint)
	// no override at all with nil upstream → must return nil upstream (preserve nil-default semantics)
	eff2 := EffectiveAnnotationsForTool(nil, "any_tool", nil)
	assert.Nil(t, eff2, "nil upstream + no overrides must stay nil")
}

func TestEffectiveAnnotations_NoOverrideReturnsUpstream(t *testing.T) {
	// Given upstream readOnly:true, no overrides
	// When EffectiveAnnotationsForTool is called
	// Then it must return upstream pointer unchanged (no copy)
	bTrue := true
	upstream := &ToolAnnotations{ReadOnlyHint: &bTrue}
	eff := EffectiveAnnotationsForTool(nil, "tool", upstream)
	assert.Same(t, upstream, eff, "no overrides must return upstream directly")
}

func TestEffectiveAnnotations_PerHintMerge(t *testing.T) {
	// Given upstream with readOnly:true, wildcard {destructive:false}, exact {openWorld:true}
	// When merged per hint
	// Then all three hints must be present (readOnly from upstream, destructive from wild, openWorld from exact)
	bTrue := true
	bFalse := false
	upstream := &ToolAnnotations{ReadOnlyHint: &bTrue}
	overrides := map[string]*ToolAnnotations{
		"*":   {DestructiveHint: &bFalse},
		"tool": {OpenWorldHint: &bTrue},
	}
	eff := EffectiveAnnotationsForTool(overrides, "tool", upstream)
	require.NotNil(t, eff)
	require.NotNil(t, eff.ReadOnlyHint)
	assert.True(t, *eff.ReadOnlyHint, "readOnly from upstream must survive")
	require.NotNil(t, eff.DestructiveHint)
	assert.False(t, *eff.DestructiveHint, "destructive from wildcard")
	require.NotNil(t, eff.OpenWorldHint)
	assert.True(t, *eff.OpenWorldHint, "openWorld from exact")
}

func TestEffectiveAnnotations_DeepCopyDoesNotAlias(t *testing.T) {
	// Given overrides with pointer hints
	// When EffectiveAnnotationsForTool is called and result is mutated
	// Then upstream and overrides must not be mutated (deep copy)
	bTrue := true
	bFalse := false
	upstream := &ToolAnnotations{ReadOnlyHint: &bTrue}
	overrides := map[string]*ToolAnnotations{"*": {DestructiveHint: &bFalse}}
	eff := EffectiveAnnotationsForTool(overrides, "tool", upstream)
	require.NotNil(t, eff)
	*eff.DestructiveHint = true
	*eff.ReadOnlyHint = false
	assert.False(t, *overrides["*"].DestructiveHint, "override must not be aliased")
	assert.True(t, *upstream.ReadOnlyHint, "upstream must not be aliased")
}

func TestEffectiveAnnotations_EmptyResultIsNil(t *testing.T) {
	// Given upstream nil and overrides entry is nil (should not happen via validation, but guard)
	// Then helper must return nil (empty result preserves nil-default semantics)
	overrides := map[string]*ToolAnnotations{"*": nil}
	eff := EffectiveAnnotationsForTool(overrides, "tool", nil)
	assert.Nil(t, eff)
}
