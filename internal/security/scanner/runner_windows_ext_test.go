package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Windows configs name package runners with their executable suffix (npx.cmd,
// uvx.exe). Missing them skips the package-cache-first path, so a data-dir
// argument can be scanned as server source, and the published-package fetch
// never runs.
func TestPackageRunner_WindowsExecutableSuffix(t *testing.T) {
	fs := []string{"-y", "@modelcontextprotocol/server-filesystem", "/data"}

	assert.True(t, isPackageRunnerCommand("npx.cmd", fs))
	assert.True(t, isPackageRunnerCommand("NPX.CMD", fs))
	assert.True(t, isPackageRunnerCommand("uvx.exe", []string{"mcp-server-fetch"}))
	assert.True(t, isPackageRunnerCommand("pnpm.cmd", []string{"dlx", "some-mcp"}))
	assert.False(t, isPackageRunnerCommand("node.exe", []string{"server.js"}))

	assert.Equal(t, "@modelcontextprotocol/server-filesystem",
		npxTargetPackage(ServerInfo{Command: "npx.cmd", Args: fs}))
	assert.Equal(t, "mcp-server-fetch",
		uvxTargetPackage(ServerInfo{Command: "uvx.exe", Args: []string{"mcp-server-fetch"}}))
}
