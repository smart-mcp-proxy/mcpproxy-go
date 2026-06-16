package core

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestExtractMissingCommand(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"zsh:1: command not found: docker", "docker"},
		{"bash: docker: command not found", "docker"},
		{"bash: line 1: npx: command not found", "npx"},
		{"sh: 1: uvx: not found", ""}, // dash form not matched; not a regression target
		{"some unrelated stderr line", ""},
		{"", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, extractMissingCommand(tc.line), "line=%q", tc.line)
	}
}

func TestCommandNotFoundHintDocker(t *testing.T) {
	hint := commandNotFoundHint([]string{
		"zsh:1: command not found: docker",
		"zsh:1: command not found: docker",
	})
	assert.Contains(t, hint, "Docker CLI not found on PATH")
	assert.Contains(t, hint, "/Applications/Docker.app/Contents/Resources/bin/docker")
}

func TestCommandNotFoundHintGeneric(t *testing.T) {
	hint := commandNotFoundHint([]string{"bash: line 1: npx: command not found"})
	assert.Contains(t, hint, "npx")
	assert.NotContains(t, hint, "Docker CLI not found")
}

func TestCommandNotFoundHintNone(t *testing.T) {
	assert.Equal(t, "", commandNotFoundHint([]string{"server listening on stdio", "ready"}))
}

func TestCollapseRepeatedLines(t *testing.T) {
	in := []string{"a", "a", "a", "b", "c", "c"}
	got := collapseRepeatedLines(in)
	assert.Equal(t, []string{"a (repeated 3×)", "b", "c (repeated 2×)"}, got)

	// Non-consecutive duplicates are not collapsed.
	assert.Equal(t, []string{"a", "b", "a"}, collapseRepeatedLines([]string{"a", "b", "a"}))
	assert.Nil(t, collapseRepeatedLines(nil))
}

// TestFormatRecentStderrDockerWall verifies the end-to-end transform: a wall of
// ~20 identical "command not found: docker" lines becomes a single actionable
// hint plus one collapsed line instead of a 20-line wall (#696).
func TestFormatRecentStderrDockerWall(t *testing.T) {
	c := &Client{
		config: &config.ServerConfig{Name: "iso"},
		logger: zap.NewNop(),
	}
	for i := 0; i < 21; i++ {
		c.recordRecentStderr("zsh:1: command not found: docker")
	}

	out := c.formatRecentStderr()
	assert.Contains(t, out, "Docker CLI not found on PATH")
	assert.Contains(t, out, "(repeated")
	// The raw line must not appear 20 separate times.
	assert.LessOrEqual(t, strings.Count(out, "command not found: docker"), 2,
		"repeated identical lines must be collapsed, got:\n%s", out)
}
