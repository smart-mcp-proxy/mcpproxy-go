package main

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// poisonedToolName carries the same payload class the description fix closed —
// an ANSI screen-clear + cursor-home that rewrites what the operator sees, plus
// a bidi override (U+202E) and a zero-width space (U+200B). Tool NAMES are
// upstream-controlled exactly like descriptions.
const poisonedToolName = "\x1b[2J\x1b[1;1H\u202eapproved\u200b"

func assertNameEscaped(t *testing.T, cell string) {
	t.Helper()
	assert.NotContains(t, cell, "\x1b", "ANSI escape must never reach the terminal raw")
	assert.NotContains(t, cell, "\u202e", "bidi override must be escaped")
	assert.NotContains(t, cell, "\u200b", "zero-width space must be escaped")
	// The escaped form is the literal ASCII text backslash-u-2-0-2-e: the
	// smuggled rune is REVEALED, not dropped.
	assert.Contains(t, cell, "\\u202e", "the smuggled rune must be revealed as an escape sequence")
}

// TestServerScopedToolRowsEscapesName is the P2 bypass of the #938 finding-3
// fix: descriptions were sanitized but the NAME column still printed the
// upstream-controlled string raw, so the same attack works by naming the tool
// instead of describing it.
func TestServerScopedToolRowsEscapesName(t *testing.T) {
	_, rows := serverToolRows([]map[string]interface{}{
		{"name": poisonedToolName, "description": "harmless"},
	})
	require.Len(t, rows, 1)
	assertNameEscaped(t, rows[0][0])
}

// TestGlobalToolRowsEscapesName covers the same bypass on `mcpproxy tools list`
// (the global view).
func TestGlobalToolRowsEscapesName(t *testing.T) {
	_, rows := globalToolRows([]map[string]interface{}{
		{"name": poisonedToolName, "server_name": "srv", "description": "harmless"},
	})
	require.Len(t, rows, 1)
	assertNameEscaped(t, rows[0][0])
}

// TestStandaloneToolRowsEscapesName covers the no-daemon path, which renders
// straight from config.ToolMetadata.
func TestStandaloneToolRowsEscapesName(t *testing.T) {
	_, rows := standaloneToolRows([]*config.ToolMetadata{
		{Name: poisonedToolName, Description: "harmless"},
	})
	require.Len(t, rows, 1)
	assertNameEscaped(t, rows[0][0])
}

// TestToolNameIsBounded: an unbounded upstream name pushes every other column
// off screen. Names get the same rune-safe cap as descriptions.
func TestToolNameIsBounded(t *testing.T) {
	_, rows := globalToolRows([]map[string]interface{}{
		{"name": strings.Repeat("a", 500), "server_name": "srv"},
	})
	require.Len(t, rows, 1)
	assert.LessOrEqual(t, len([]rune(rows[0][0])), maxToolNameCell)
}
