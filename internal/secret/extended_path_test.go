package secret

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindowsExtendedLengthPath(t *testing.T) {
	long := `C:\` + strings.Repeat(`abcdefghij\`, 30) + `mcp_config.json`
	cases := []struct{ in, want string }{
		{`C:\Users\me\.mcpproxy\mcp_config.json`, `\\?\C:\Users\me\.mcpproxy\mcp_config.json`},
		{`c:/Users/me/mcp_config.json`, `\\?\c:\Users\me\mcp_config.json`},
		{`C:\a\\b\.\c\..\mcp_config.json`, `\\?\C:\a\b\mcp_config.json`},
		{`C:\..\..\x`, `\\?\C:\x`},
		{`C:\`, `\\?\C:\`},
		{`\\server\share\dir\mcp_config.json`, `\\?\UNC\server\share\dir\mcp_config.json`},
		{`//server/share/dir/../cfg.json`, `\\?\UNC\server\share\cfg.json`},
		{`\\server\share\..\..\cfg.json`, `\\?\UNC\server\share\cfg.json`},
		{`\\?\C:\already\cfg.json`, `\\?\C:\already\cfg.json`},
		{`\\?\UNC\server\share\cfg.json`, `\\?\UNC\server\share\cfg.json`},
		{`\\.\pipe\mcpproxy`, `\\.\pipe\mcpproxy`},
		{`relative\cfg.json`, `relative\cfg.json`},
		{`\rooted\no-drive`, `\rooted\no-drive`},
		{`\\server`, `\\server`},
		{long, `\\?\` + long},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, windowsExtendedLengthPath(c.in), c.in)
	}
}
