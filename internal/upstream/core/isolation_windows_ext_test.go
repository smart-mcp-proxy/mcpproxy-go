package core

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Windows configs name launchers with their executable suffix (uvx.exe,
// npx.cmd). Treating those as an unknown "binary" picks the generic image and
// runs "npx.cmd" inside a Linux container, where it does not exist.
func TestDetectRuntimeType_WindowsExecutableSuffix(t *testing.T) {
	im := NewIsolationManager(config.DefaultDockerIsolationConfig())
	cases := map[string]string{
		"uvx.exe":  cmdUvx,
		"npx.cmd":  cmdNpx,
		"NPX.CMD":  cmdNpx,
		"npm.cmd":  cmdNpm,
		"yarn.cmd": cmdYarn,
		"pip.exe":  cmdPip,
		"go.exe":   cmdGo,
		"uvx":      cmdUvx,
		"my-tool":  "binary",
	}
	for cmd, want := range cases {
		assert.Equal(t, want, im.DetectRuntimeType(cmd), cmd)
	}
}
