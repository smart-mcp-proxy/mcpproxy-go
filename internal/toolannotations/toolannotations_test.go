package toolannotations

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// --- Spec 094 T001 parity oracle, moved here with the classifier (Spec 098 T005) ---
//
// ShouldExclude delegates to ExcludeReason, so comparing the two live functions
// would be circular. legacyShouldExcludeOracle is the verbatim body of
// shouldExclude as it stood before the spec-094 attribution refactor
// (internal/server/mcp_annotations.go:153-181 at 8ed4e4689) — an independent
// oracle that catches semantic drift the delegation cannot.
//
// Filter semantics are FROZEN by the spec: this file must never be "fixed" to
// match a changed implementation.
func legacyShouldExcludeOracle(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) bool {
	if readOnlyOnly {
		if annotations == nil || annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint {
			return true
		}
	}

	if excludeDestructive {
		isReadOnly := annotations != nil && annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint
		if !isReadOnly {
			if annotations == nil || annotations.DestructiveHint == nil || *annotations.DestructiveHint {
				return true
			}
		}
	}

	if excludeOpenWorld {
		if annotations == nil || annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
			return true
		}
	}

	return false
}

// expectedAttribution derives the first-failure filter key and reason class
// straight from spec 094 FR-004 / data-model.md, independently of both the
// implementation and the oracle above.
func expectedAttribution(a *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) (filterKey string, explicit, excluded bool) {
	hint := func(p *bool) (set, val bool) {
		if a == nil || p == nil {
			return false, false
		}
		return true, *p
	}
	var roPtr, destPtr, owPtr *bool
	if a != nil {
		roPtr, destPtr, owPtr = a.ReadOnlyHint, a.DestructiveHint, a.OpenWorldHint
	}
	roSet, roVal := hint(roPtr)
	destSet, destVal := hint(destPtr)
	owSet, owVal := hint(owPtr)

	if readOnlyOnly {
		switch {
		case !roSet:
			return "read_only_only", false, true
		case !roVal:
			return "read_only_only", true, true
		}
	}

	explicitlyReadOnly := roSet && roVal
	if excludeDestructive && !explicitlyReadOnly {
		switch {
		case !destSet:
			return "exclude_destructive", false, true
		case destVal:
			return "exclude_destructive", true, true
		}
	}

	if excludeOpenWorld {
		switch {
		case !owSet:
			return "exclude_open_world", false, true
		case owVal:
			return "exclude_open_world", true, true
		}
	}

	return "", false, false
}

var hintStates = []struct {
	label string
	value *bool
}{
	{"unset", nil},
	{"true", boolPtr(true)},
	{"false", boolPtr(false)},
}

// TestExcludeReason_ParityWithFrozenOracle exhausts the full domain: 27
// non-nil hint combinations + the nil-annotations state, times the 8 filter
// combinations = 224 cases. Every case asserts all three outputs, plus the
// Filters-typed wrapper added for the preflight evaluator.
func TestExcludeReason_ParityWithFrozenOracle(t *testing.T) {
	type annState struct {
		label       string
		annotations *config.ToolAnnotations
	}
	states := []annState{{"nil-annotations", nil}}
	for _, ro := range hintStates {
		for _, dest := range hintStates {
			for _, ow := range hintStates {
				states = append(states, annState{
					label: "readOnly=" + ro.label + "/destructive=" + dest.label + "/openWorld=" + ow.label,
					annotations: &config.ToolAnnotations{
						ReadOnlyHint:    ro.value,
						DestructiveHint: dest.value,
						OpenWorldHint:   ow.value,
					},
				})
			}
		}
	}
	require.Len(t, states, 28, "28 annotation states: 3^3 hint combos + nil annotations")

	filterCombos := []Filters{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{false, false, true},
		{true, true, false},
		{true, false, true},
		{false, true, true},
		{true, true, true},
	}

	cases := 0
	for _, st := range states {
		for _, fc := range filterCombos {
			cases++
			name := fmt.Sprintf("%s|ro=%t,xd=%t,xow=%t", st.label, fc.ReadOnlyOnly, fc.ExcludeDestructive, fc.ExcludeOpenWorld)
			t.Run(name, func(t *testing.T) {
				gotKey, gotExplicit, gotExcluded := ExcludeReason(st.annotations, fc.ReadOnlyOnly, fc.ExcludeDestructive, fc.ExcludeOpenWorld)

				wantExcluded := legacyShouldExcludeOracle(st.annotations, fc.ReadOnlyOnly, fc.ExcludeDestructive, fc.ExcludeOpenWorld)
				assert.Equal(t, wantExcluded, gotExcluded, "excluded must match the frozen pre-refactor oracle")

				assert.Equal(t, wantExcluded, ShouldExclude(st.annotations, fc.ReadOnlyOnly, fc.ExcludeDestructive, fc.ExcludeOpenWorld),
					"ShouldExclude must stay semantically frozen")

				// The Filters-typed wrapper must be a pure restatement.
				wKey, wExplicit, wExcluded := ExcludeReasonFor(st.annotations, fc)
				assert.Equal(t, gotKey, wKey)
				assert.Equal(t, gotExplicit, wExplicit)
				assert.Equal(t, gotExcluded, wExcluded)

				wantKey, wantExplicit, wantExcludedAttr := expectedAttribution(st.annotations, fc.ReadOnlyOnly, fc.ExcludeDestructive, fc.ExcludeOpenWorld)
				require.Equal(t, wantExcludedAttr, wantExcluded, "test oracles disagree — fix the test, not the code")
				assert.Equal(t, wantKey, gotKey, "first-failure filter key (read-only -> destructive -> open-world)")
				assert.Equal(t, wantExplicit, gotExplicit, "reason class (missing vs explicit)")

				if !gotExcluded {
					assert.Empty(t, gotKey, "a kept tool has no responsible filter")
					assert.False(t, gotExplicit, "a kept tool has no reason class")
				}
			})
		}
	}
	assert.Equal(t, 224, cases, "exhaustive domain is 28 annotation states x 8 filter combos")
}

// The read-only shortcut is the subtlest frozen semantic: an explicitly
// read-only tool passes exclude_destructive even with destructiveHint=true,
// so it can only ever be attributed to read_only_only or exclude_open_world.
func TestExcludeReason_ReadOnlyShortcutAttribution(t *testing.T) {
	readOnlyDestructive := &config.ToolAnnotations{
		ReadOnlyHint:    boolPtr(true),
		DestructiveHint: boolPtr(true),
	}

	key, explicit, excluded := ExcludeReason(readOnlyDestructive, false, true, false)
	assert.False(t, excluded, "explicit readOnlyHint=true passes exclude_destructive (frozen shortcut)")
	assert.Empty(t, key)
	assert.False(t, explicit)

	key, explicit, excluded = ExcludeReason(readOnlyDestructive, false, true, true)
	assert.True(t, excluded)
	assert.Equal(t, FilterKeyExcludeOpenWorld, key)
	assert.False(t, explicit, "openWorldHint unset is a missing-annotation omission")
}

// TestFilterKeys_FrozenWireValues pins the three keys: they are diagnostics map
// keys and preflight `detail` text, i.e. a wire contract.
func TestFilterKeys_FrozenWireValues(t *testing.T) {
	assert.Equal(t, "read_only_only", FilterKeyReadOnlyOnly)
	assert.Equal(t, "exclude_destructive", FilterKeyExcludeDestruct)
	assert.Equal(t, "exclude_open_world", FilterKeyExcludeOpenWorld)
}

func TestFilters_Any(t *testing.T) {
	assert.False(t, Filters{}.Any())
	assert.True(t, Filters{ReadOnlyOnly: true}.Any())
	assert.True(t, Filters{ExcludeDestructive: true}.Any())
	assert.True(t, Filters{ExcludeOpenWorld: true}.Any())
}
