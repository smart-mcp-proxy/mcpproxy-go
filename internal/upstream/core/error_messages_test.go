package core

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStdioConfigRejectsPackageRunnerWithNoArgs(t *testing.T) {
	// Reproduces the obsidian-pilot misconfiguration: `uvx` with no args
	// would previously launch a subprocess that prints help to stderr
	// and then time out MCP initialize ~60s later with an opaque
	// "context deadline exceeded". We should fail fast instead.
	cases := []struct {
		command        string
		wantHintSubstr string
	}{
		{"uvx", "Python package"},
		{"npx", "npm package"},
		{"pipx", "subcommand and package"},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			err := validateStdioConfig(&config.ServerConfig{
				Name:    "obsidian-pilot",
				Command: tc.command,
				// Args intentionally nil.
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "has no args")
			assert.Contains(t, err.Error(), tc.wantHintSubstr)
			assert.Contains(t, err.Error(), "obsidian-pilot",
				"error should name the offending server")
		})
	}
}

func TestValidateStdioConfigAllowsKnownRunnerWithArgs(t *testing.T) {
	// Positive control: uvx WITH args must pass the pre-flight guard.
	err := validateStdioConfig(&config.ServerConfig{
		Name:    "ok",
		Command: "uvx",
		Args:    []string{"some-package"},
	})
	assert.NoError(t, err)
}

func TestValidateStdioConfigAllowsGenericCommandWithNoArgs(t *testing.T) {
	// A raw binary path with no args is legitimate — some servers are
	// self-contained binaries. Only the known package-runner commands
	// should trigger the no-args guard.
	err := validateStdioConfig(&config.ServerConfig{
		Name:    "ok",
		Command: "/usr/local/bin/my-mcp-server",
	})
	assert.NoError(t, err)
}

func TestValidateStdioConfigRejectsEmptyCommand(t *testing.T) {
	err := validateStdioConfig(&config.ServerConfig{Name: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command specified")
}

func TestRecordRecentStderrRingBuffer(t *testing.T) {
	c := &Client{}

	// Push more than the cap and verify we retain the last N in order.
	total := maxRecentStderrLines + 5
	for i := 0; i < total; i++ {
		c.recordRecentStderr(lineN(i))
	}

	snap := c.RecentStderrSnapshot()
	require.Len(t, snap, maxRecentStderrLines)
	assert.Equal(t, lineN(total-maxRecentStderrLines), snap[0],
		"oldest retained line should be total-max")
	assert.Equal(t, lineN(total-1), snap[len(snap)-1],
		"newest line should be the last appended")
}

func TestRecordRecentStderrTruncatesLongLines(t *testing.T) {
	c := &Client{}
	huge := strings.Repeat("x", maxStderrLineLen*3)
	c.recordRecentStderr(huge)

	snap := c.RecentStderrSnapshot()
	require.Len(t, snap, 1)
	// Truncated lines get a trailing ellipsis rune.
	assert.LessOrEqual(t, len(snap[0]), maxStderrLineLen+len("…"))
	assert.True(t, strings.HasSuffix(snap[0], "…"),
		"truncated line should end with ellipsis")
}

func TestRecordRecentStderrIgnoresEmpty(t *testing.T) {
	c := &Client{}
	c.recordRecentStderr("")
	assert.Nil(t, c.RecentStderrSnapshot())
}

func TestFormatRecentStderrEmpty(t *testing.T) {
	c := &Client{}
	assert.Equal(t, "", c.formatRecentStderr())
}

func TestFormatRecentStderrIndented(t *testing.T) {
	c := &Client{}
	c.recordRecentStderr("first line")
	c.recordRecentStderr("second line")

	got := c.formatRecentStderr()
	// Each line prefixed with "  | " so the block is visually separated
	// in wrapped error messages.
	assert.Equal(t, "  | first line\n  | second line", got)
}

func lineN(i int) string {
	return "line-" + itoa(i)
}

// Minimal int→string helper to avoid pulling strconv just for tests.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	buf := make([]byte, 0, 8)
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
