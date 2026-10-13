package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Redirections are removed by the shell before argv is built, so a redirection
// between a sensitive flag and its value must not be taken for the value, and a
// subshell parenthesis ends a word so an assignment inside it is still one.
func TestSpawnArgv_RedirectionsAndSubshellParens(t *testing.T) {
	cases := []struct{ cmd, secret string }{
		{"exec npx srv --password 2>/dev/null hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password 2> /dev/null hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password >/dev/null hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password </dev/null hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password 2>&1 hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password >>'/tmp/a b' hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password &>/dev/null hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password 2>/dev/null 2>&1 hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password hunter2xyz>/dev/null", "hunter2xyz"},
		{"(API_KEY='hunter2xyz and more' exec npx srv)", "hunter2xyz"},
		{"(API_KEY='hunter2xyz and more' exec npx srv)", "more"},
		{"( API_KEY='hunter2xyz and more' exec npx srv )", "more"},
		{"((API_KEY=\"hunter2xyz and more\" exec npx srv))", "more"},
		{"exec npx srv --password 2>/dev/null 'hunter2xyz and more'", "more"},
		// unbalanced quote forces the whitespace-only fallback splitter
		{"cd /o'brien; (API_KEY=hunter2xyz exec npx srv)", "hunter2xyz"},
		{"cd /o'brien; exec npx srv --password 2>/dev/null hunter2xyz", "hunter2xyz"},
	}
	for _, c := range cases {
		argv := []string{"sh", "-c", c.cmd}
		orig := append([]string(nil), argv...)
		srv := contracts.Server{Name: "x", Protocol: "stdio", Command: "sh", Args: []string{"-c", c.cmd}}
		RedactServerSecretFields(&srv)
		for name, out := range map[string]string{
			"argv":  strings.Join(AuditRedaction.SpawnArgv(argv), " "),
			"audit": AuditRedaction.CommandString(c.cmd),
			"live":  LiveRedaction.CommandString(c.cmd),
			"spawn": AuditRedaction.SpawnCommandString(c.cmd),
		} {
			assert.NotContains(t, out, c.secret, name+": "+c.cmd)
			if !strings.Contains(c.cmd, "o'brien") {
				assert.Contains(t, out, "npx srv", name+": "+c.cmd)
			}
		}
		assert.Equal(t, orig, argv, "stored args must be unchanged")
	}
}

// Redirections and parentheses that carry no secret must survive verbatim.
func TestCommandString_RedirectionsAndParensRemainReadable(t *testing.T) {
	for _, cmd := range []string{
		"(cd /srv && exec npx srv --port 8080) 2>/dev/null",
		"exec npx srv --verbose 2>&1 >/tmp/log",
	} {
		assert.Equal(t, cmd, LiveRedaction.CommandString(cmd))
	}
}
