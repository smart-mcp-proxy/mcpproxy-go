package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestUnifiedDiff_EqualIsEmpty(t *testing.T) {
	require.Equal(t, "", unifiedDiff("a", "b", "x\ny\n", "x\ny\n"))
}

func TestUnifiedDiff_SingleChangeWithThreeLinesOfContext(t *testing.T) {
	old := numbered(20)
	cur := strings.Replace(old, "line 10\n", "line ten\n", 1)
	want := "--- approved\n+++ current\n" +
		"@@ -7,7 +7,7 @@\n" +
		" line 7\n line 8\n line 9\n-line 10\n+line ten\n line 11\n line 12\n line 13\n"
	require.Equal(t, want, unifiedDiff("approved", "current", old, cur))
}

func TestUnifiedDiff_ChangesFarApartMakeTwoHunks(t *testing.T) {
	old := numbered(30)
	cur := strings.Replace(old, "line 3\n", "THREE\n", 1)
	cur = strings.Replace(cur, "line 28\n", "TWENTY-EIGHT\n", 1)
	got := unifiedDiff("a", "b", old, cur)
	require.Equal(t, 2, strings.Count(got, "@@ -"), got)
	require.Contains(t, got, "@@ -1,6 +1,6 @@")
	require.Contains(t, got, "@@ -25,6 +25,6 @@")
}

func TestUnifiedDiff_NearbyChangesMergeIntoOneHunk(t *testing.T) {
	old := numbered(20)
	cur := strings.Replace(old, "line 5\n", "FIVE\n", 1)
	cur = strings.Replace(cur, "line 11\n", "ELEVEN\n", 1) // 5 unchanged lines between
	got := unifiedDiff("a", "b", old, cur)
	require.Equal(t, 1, strings.Count(got, "@@ -"), got)
}

func TestUnifiedDiff_AddedAndRemovedFiles(t *testing.T) {
	got := unifiedDiff("/dev/null", "current", "", "one\ntwo\n")
	require.Equal(t, "--- /dev/null\n+++ current\n@@ -0,0 +1,2 @@\n+one\n+two\n", got)
	got = unifiedDiff("approved", "/dev/null", "one\n", "")
	require.Equal(t, "--- approved\n+++ /dev/null\n@@ -1 +0,0 @@\n-one\n", got)
}

func TestUnifiedDiff_InsertionAndDeletionAreMinimal(t *testing.T) {
	got := unifiedDiff("a", "b", "a\nb\nc\n", "a\nb\nX\nc\n")
	require.Contains(t, got, "+X\n")
	require.NotContains(t, got, "-c")
	got = unifiedDiff("a", "b", "a\nb\nc\nd\n", "a\nd\n")
	require.Contains(t, got, "-b\n-c\n")
	require.NotContains(t, got, "-d")
}

func TestUnifiedDiff_OnlyFinalNewlineDiffers(t *testing.T) {
	got := unifiedDiff("a", "b", "x\ny", "x\ny\n")
	require.Contains(t, got, "only the final line feed differs")
}

// Applying the script must reproduce the new text, for many random pairs.
func TestDiffLines_ReplayReproducesNewText(t *testing.T) {
	seed := uint32(7)
	next := func(n uint32) uint32 {
		seed = seed*1664525 + 1013904223
		return (seed >> 8) % n
	}
	for trial := 0; trial < 200; trial++ {
		mk := func() []string {
			out := make([]string, next(12))
			for i := range out {
				out[i] = string(rune('a' + next(4)))
			}
			return out
		}
		a, b := mk(), mk()
		var gotA, gotB []string
		for _, op := range diffLines(a, b) {
			if op.kind != '+' {
				gotA = append(gotA, op.text)
			}
			if op.kind != '-' {
				gotB = append(gotB, op.text)
			}
		}
		require.Equal(t, strings.Join(a, "|"), strings.Join(gotA, "|"), "old side, trial %d", trial)
		require.Equal(t, strings.Join(b, "|"), strings.Join(gotB, "|"), "new side, trial %d", trial)
	}
}

func TestDiffLines_HugeInputsFallBackToABlockReplace(t *testing.T) {
	n := 2500 // (n+1)^2 > diffMaxCells with a changed first and last line
	a := make([]string, n)
	b := make([]string, n)
	for i := range a {
		a[i], b[i] = fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
	}
	ops := diffLines(a, b)
	require.Len(t, ops, 2*n)
	require.Equal(t, byte('-'), ops[0].kind)
}
