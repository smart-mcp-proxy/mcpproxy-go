package server

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 105 PR H1, T113: gap-map.md's FR01x-G7 entry (narrowed claim, §3)
// names FOUR tests that pinned pre-105 (insecure) behaviour as their
// assertion, not merely "referenced" it in prose — the ones SC-007 counts as
// the gate the parallel branches merged ahead of the spec left behind:
//
//  1. internal/server/mcp_direct_skew_test.go — the forward-flip seam's
//     listing used to assert the OLD owner's definition was "still listed"
//     (FR-008 G1). Inverted in Spec 105 PR F (#1326 / e33d6a139); the exact
//     assertion and its literal message ("so it is still listed") were
//     removed, not merely negated.
//  2. internal/server/mcp_call_tool_target_tier_test.go — an undiscovered
//     ("ghost") tool on a known server used to be granted the destructive
//     tier and reach dispatch, asserted by `assert.Contains(t, text, "No
//     client found")` inside what is now
//     TestCallToolRead_UndiscoveredTool_RefusedForEveryCaller (renamed from
//     TestCallToolRead_UndiscoveredTool_RequiresDestructiveTier). Inverted in
//     Spec 105 PR A (#1279 / bac8f6b0d, research D4).
//  3. internal/server/mcp_read_cache_authz_test.go — a revoked/narrower
//     redemption used to get its own disclosing body, asserted by
//     `assert.Contains(t, resultText(t, result), "not readable with this
//     credential")`. Inverted in Spec 105 PR B (#1282 / 46de8038c): the body
//     now collapses into the same "cache key not found" a nonexistent key
//     produces; the old wording survives only as an explanatory comment.
//  4. internal/cache/authorization_test.go — TestAuthorization_CouldHaveProduced
//     used to deny a URL/session-profile-bound administrator reading an
//     unscoped administrator entry ({"admin bound to a URL profile cannot
//     read an unscoped admin entry", admin, adminInProfile, false}).
//     Inverted in Spec 105 PR B (research D5, caller-kind-first): the case
//     was renamed to end "(kind first)" and its verdict flipped to true.
//
// This file scans the actual test SOURCE TEXT (not the compiled package —
// #4 lives in a different Go package and cannot be imported/called from
// here) for the literal markers that would only be present if the assertion
// reverted to its pre-105 form. Comments that merely DOCUMENT the old
// wording (as #3's does) are not a violation; only a live assertion is.

// mustReadSourceFile reads a test source file for guard scanning. It uses a
// path relative to this package's directory (internal/server), so `t.Run`
// working-directory quirks under `go test ./...` (always the package dir)
// are exactly what this relies on.
func mustReadSourceFile(t *testing.T, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(relPath)
	require.NoError(t, err, "guard fixture: %s must exist", relPath)
	return string(data)
}

// nonCommentLinesContain reports whether any line of src that is not a `//`
// comment (leading whitespace then `//`) contains needle. To resist the
// simplest evasion (splitting a literal across a Go string-concatenation:
// `"not readable " + "with this credential"`), it first strips `" + "`/`"+"`
// sequences between adjacent quoted segments so a split literal collapses
// back to its concatenated form before matching. This is deliberately NOT
// an AST-based check (cross-model review round 1 asked for one; a full
// token-stream/AST guard is a bigger lift than this PR's remaining budget
// justifies for a regression guard whose realistic threat model is
// ACCIDENTAL reintroduction via a bad merge/rebase/cherry-pick, not a
// deliberately adversarial rewrite designed to evade grep).
func nonCommentLinesContain(src, needle string) bool {
	collapsed := stringConcatCollapseRE.ReplaceAllString(src, "")
	for _, line := range strings.Split(collapsed, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

// stringConcatCollapseRE matches a closing quote, optional whitespace, a `+`,
// optional whitespace, and an opening quote — the shape of two adjacent Go
// string literals being concatenated — so nonCommentLinesContain can undo a
// literal deliberately split to dodge a plain substring search.
var stringConcatCollapseRE = regexp.MustCompile(`"\s*\+\s*"`)

// extractFunc returns the source of a single top-level Go function by name,
// from its `func <name>(` line up to (but not including) the next top-level
// `func ` line, or the end of the file.
func extractFunc(t *testing.T, src, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
	loc := re.FindStringIndex(src)
	require.NotNil(t, loc, "guard fixture: function %s must still exist in this file", name)
	rest := src[loc[1]:]
	nextRe := regexp.MustCompile(`(?m)^func `)
	if end := nextRe.FindStringIndex(rest); end != nil {
		return rest[:end[0]]
	}
	return rest
}

// TestScopePinnedReversalsStayInverted is T113's grep guard: it fails if any
// of the four assertions the doc comment above names has reverted to its
// original, pre-105 form.
func TestScopePinnedReversalsStayInverted(t *testing.T) {
	t.Run("mcp_direct_skew_test.go: forward-flip seam no longer lists the stale owner's definition", func(t *testing.T) {
		src := mustReadSourceFile(t, "mcp_direct_skew_test.go")
		assert.NotContains(t, src, "so it is still listed",
			"the pre-105 assertion that the forward-flip seam still lists the OLD owner's definition must not reappear (FR-008 G1, Spec 105 PR F)")
		// Structural companion (not text-message-based): the pre-105
		// assertion's SHAPE was `require.Contains(t, f.listed(oldOnly),
		// display, ...)` — a LIVE (non-Not) Contains against the
		// forward-flip fixture's listing. Requiring the fixed file to
		// instead use NotContains against that same listing call resists a
		// reversal that keeps the assertion live but drops or rewords its
		// message.
		liveAdmit := regexp.MustCompile(`\b(?:require|assert)\.Contains\(\s*t,\s*f\.listed\(oldOnly\)`)
		assert.False(t, liveAdmit.MatchString(src),
			"a live (non-Not) Contains against f.listed(oldOnly) must not reappear regardless of its message (FR-008 G1, Spec 105 PR F)")
		notAdmit := regexp.MustCompile(`\b(?:require|assert)\.NotContains\(\s*t,\s*f\.listed\(oldOnly\)`)
		assert.True(t, notAdmit.MatchString(src),
			"guard fixture: the inverted NotContains assertion against f.listed(oldOnly) must still exist somewhere in this file")
	})

	t.Run("mcp_call_tool_target_tier_test.go: undiscovered tool never reaches dispatch", func(t *testing.T) {
		src := mustReadSourceFile(t, "mcp_call_tool_target_tier_test.go")
		require.Contains(t, src, "func TestCallToolRead_UndiscoveredTool_RefusedForEveryCaller(",
			"the inverted test must still exist under its post-105 name (was TestCallToolRead_UndiscoveredTool_RequiresDestructiveTier)")
		body := extractFunc(t, src, "TestCallToolRead_UndiscoveredTool_RefusedForEveryCaller")
		// Whitespace-tolerant (a reformatted `assert.Contains(\n\tt, text,\n\t"No client found")`
		// must still be caught), but still requires the literal "No client
		// found" quoted string next to a live (non-Not) Contains call — see
		// the file-level doc comment for why this stops short of an AST
		// check.
		liveAdmit := regexp.MustCompile(`(?s)\bassert\.Contains\(\s*t,\s*text,\s*"No client found"\s*\)`)
		assert.False(t, liveAdmit.MatchString(body),
			"the pre-105 assertion that an undiscovered tool's call reaches dispatch (\"No client found\") must not reappear (FR-009 G4, Spec 105 PR A, research D4)")
	})

	t.Run("mcp_read_cache_authz_test.go: revoked redemption collapses into the nonexistent-key body", func(t *testing.T) {
		src := mustReadSourceFile(t, "mcp_read_cache_authz_test.go")
		assert.False(t, nonCommentLinesContain(src, `"not readable with this credential"`),
			"the pre-105 disclosing body \"not readable with this credential\" must not be asserted again outside of an explanatory comment (FR-001 G5, Spec 105 PR B)")
		assert.Contains(t, src, "cache key not found",
			"the collapsed non-disclosing body must still be asserted somewhere in this file")
	})

	t.Run("internal/cache/authorization_test.go: caller-kind-first administrator redemption", func(t *testing.T) {
		src := mustReadSourceFile(t, "../cache/authorization_test.go")
		// Structural, not text-label-based (cross-model review round 1: the
		// original check matched one exact case-name string and would miss
		// a relabeled row asserting the same wrong verdict): find the table
		// row whose PRODUCER is `admin` and whose READER is `adminInProfile`
		// — {producer=admin, reader=X, want=bool} is TestAuthorization_CouldHaveProduced's
		// case-literal shape — and require its verdict to be `true`
		// (caller-kind-first: an administrator qualifies for ANY snapshot
		// regardless of its own profile binding), whatever the case NAME
		// says.
		rowRE := regexp.MustCompile(`\{"[^"]*",\s*admin,\s*adminInProfile,\s*(true|false)\}`)
		match := rowRE.FindStringSubmatch(src)
		require.NotNil(t, match, "guard fixture: the {producer=admin, reader=adminInProfile} table row must still exist in TestAuthorization_CouldHaveProduced")
		assert.Equal(t, "true", match[1],
			"the pre-D5 verdict (a profile-bound administrator refused an unscoped administrator entry, verdict=false) must not reappear under ANY case-name label (FR-001 G6, Spec 105 PR B, research D5)")
	})
}
