package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-k / contracts/cli.md: 'mcpproxy status' prints a "Token savings:"
// line directly after "Servers:", suffixed " (estimate)" while
// contracts.ServerTokenMetrics.Estimated is true.

func captureStatusTable(info *StatusInfo) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printStatusTable(info)

	w.Close()
	os.Stdout = old

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func TestStatusTable_TokenSavingsEstimated(t *testing.T) {
	info := &StatusInfo{
		State:        "Running",
		Servers:      &ServerCounts{Connected: 2, Quarantined: 0, Total: 2},
		TokenSavings: &StatusTokenSavings{SavedTokens: 12000, Estimated: true},
	}
	output := captureStatusTable(info)
	assert.Contains(t, output, "Token savings:")
	assert.Contains(t, output, "~12000 tokens/request (estimate)")

	// Directly after Servers: (contracts/cli.md line order).
	serversIdx := strings.Index(output, "Servers:")
	tokenIdx := strings.Index(output, "Token savings:")
	require.NotEqual(t, -1, serversIdx)
	require.NotEqual(t, -1, tokenIdx)
	assert.Less(t, serversIdx, tokenIdx)
}

func TestStatusTable_TokenSavingsRealNoEstimateSuffix(t *testing.T) {
	info := &StatusInfo{
		State:        "Running",
		TokenSavings: &StatusTokenSavings{SavedTokens: 9000, Estimated: false},
	}
	output := captureStatusTable(info)
	assert.Contains(t, output, "~9000 tokens/request")
	assert.NotContains(t, output, "(estimate)")
}

func TestStatusTable_TokenSavingsOmittedWhenNil(t *testing.T) {
	info := &StatusInfo{State: "Running"}
	output := captureStatusTable(info)
	assert.NotContains(t, output, "Token savings:")
}

func TestExtractStatusTokenSavings(t *testing.T) {
	got := extractStatusTokenSavings(map[string]interface{}{
		"saved_tokens": float64(4200),
		"estimated":    true,
	})
	require.NotNil(t, got)
	assert.Equal(t, 4200, got.SavedTokens)
	assert.True(t, got.Estimated)

	assert.Nil(t, extractStatusTokenSavings(nil))
}

func TestStatusJSON_IncludesTokenSavings(t *testing.T) {
	info := &StatusInfo{
		State:        "Running",
		TokenSavings: &StatusTokenSavings{SavedTokens: 500, Estimated: true},
	}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := printStatusJSON(info)
	w.Close()
	os.Stdout = old
	require.NoError(t, err)

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	assert.Contains(t, output, `"saved_tokens": 500`)
	assert.Contains(t, output, `"estimated": true`)
}
