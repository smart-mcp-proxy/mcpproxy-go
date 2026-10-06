package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeResultsFile(t *testing.T, dir, name string, lines map[string]int64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	for op, ns := range lines {
		_, err := f.WriteString(op + "=" + itoa64(ns) + "\n")
		require.NoError(t, err)
	}
	return path
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestReadResults_ParsesOperationNanosecondPairs(t *testing.T) {
	dir := t.TempDir()
	path := writeResultsFile(t, dir, "results.txt", map[string]int64{
		"retrieve_tools": 1_000_000,
		"read_cache":     2_000_000,
	})
	got, err := readResults(path)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000_000), got["retrieve_tools"])
	assert.Equal(t, int64(2_000_000), got["read_cache"])
}

func TestReadResults_EmptyFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	_, err := readResults(path)
	assert.Error(t, err, "an empty results file means the test run produced no measurements — that must fail closed, not silently no-op")
}

func TestReadResults_MalformedLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.txt")
	require.NoError(t, os.WriteFile(path, []byte("not-a-valid-line\n"), 0o644))
	_, err := readResults(path)
	assert.Error(t, err)
}

// TestCompare_WithinBudget_NoRegression is the OK case: HEAD is faster than
// base, well within the relative-or-5ms budget.
func TestCompare_WithinBudget_NoRegression(t *testing.T) {
	base := map[string]int64{
		"retrieve_tools": int64(10 * time.Millisecond),
		"read_cache":     int64(10 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	head := map[string]int64{
		"retrieve_tools": int64(10*time.Millisecond + 500*time.Microsecond), // +0.5ms, well under 5ms
		"read_cache":     int64(9 * time.Millisecond),                       // faster
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	lines, failures := compare(base, head, "base.txt", "head.txt")
	assert.Empty(t, failures, "no operation should exceed the max(10%%, 5ms) budget")
	assert.Len(t, lines, 4)
}

// TestCompare_RelativeBudgetGovernsLargeBaselines: a base of 100ms allows up
// to +10ms (10% > 5ms absolute floor) before it is a regression.
func TestCompare_RelativeBudgetGovernsLargeBaselines(t *testing.T) {
	base := map[string]int64{
		"retrieve_tools": int64(100 * time.Millisecond),
		"read_cache":     int64(100 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	withinBudget := map[string]int64{
		"retrieve_tools": int64(109 * time.Millisecond), // +9ms < +10ms (10%)
		"read_cache":     int64(100 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	_, failures := compare(base, withinBudget, "base.txt", "head.txt")
	assert.Empty(t, failures)

	overBudget := map[string]int64{
		"retrieve_tools": int64(112 * time.Millisecond), // +12ms > +10ms (10%)
		"read_cache":     int64(100 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	_, failures = compare(base, overBudget, "base.txt", "head.txt")
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0], "retrieve_tools")
}

// TestCompare_AbsoluteFloorGovernsSmallBaselines: a base of 1ms allows only
// +5ms (the absolute floor, since 10% of 1ms is far below it) — a 5.1ms
// regression on a sub-millisecond operation must still fail.
func TestCompare_AbsoluteFloorGovernsSmallBaselines(t *testing.T) {
	base := map[string]int64{
		"retrieve_tools": int64(1 * time.Millisecond),
		"read_cache":     int64(1 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	head := map[string]int64{
		"retrieve_tools": int64(1 * time.Millisecond),
		"read_cache":     int64(1 * time.Millisecond),
		"prompts/list":   int64(6*time.Millisecond + 200*time.Microsecond), // +5.2ms > 5ms floor
		"tools/list":     int64(1 * time.Millisecond),
	}
	_, failures := compare(base, head, "base.txt", "head.txt")
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0], "prompts/list")
}

// TestCompare_MissingMeasurementFailsClosed: FR-011's "fails if either side
// yields no measurement for any operation" — a required operation absent
// from either file is a failure, never a skip.
func TestCompare_MissingMeasurementFailsClosed(t *testing.T) {
	full := map[string]int64{
		"retrieve_tools": int64(1 * time.Millisecond),
		"read_cache":     int64(1 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		"tools/list":     int64(1 * time.Millisecond),
	}
	missingOnHead := map[string]int64{
		"retrieve_tools": int64(1 * time.Millisecond),
		"read_cache":     int64(1 * time.Millisecond),
		"prompts/list":   int64(1 * time.Millisecond),
		// tools/list intentionally absent.
	}
	_, failures := compare(full, missingOnHead, "base.txt", "head.txt")
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0], "tools/list")
	assert.Contains(t, failures[0], "HEAD side")

	_, failures = compare(missingOnHead, full, "base.txt", "head.txt")
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0], "tools/list")
	assert.Contains(t, failures[0], "merge-base side")
}
