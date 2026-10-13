package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSingleRejectedRowEmitter guards the spec-093 de-duplication invariant from
// the producing side. Every shed already produces one canonical "rejected"
// tool_call record at the limiter, which is what makes the record
// origin-independent (FR-012). The MCP variant handler adds one MCP-flavoured
// internal_tool_call row on top, and the default activity filter hides exactly
// that one (see storage.ActivityFilter).
//
// If another dispatch path in this package starts emitting a rejected activity
// row, the filter can no longer collapse the pair and summaries double-count
// sheds again — so the emitter count is pinned here.
func TestSingleRejectedRowEmitter(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	require.NoError(t, err)

	emitters := map[string]int{}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "emitActivityInternalToolCall") &&
				strings.Contains(line, "ActivityStatusRejected") {
				emitters[path]++
			}
		}
	}

	assert.Equal(t, map[string]int{"mcp.go": 1}, emitters,
		"exactly one rejected internal_tool_call emitter may exist (the MCP variant handler); "+
			"code_execution and replay must rely on the limiter's canonical row alone")
}
