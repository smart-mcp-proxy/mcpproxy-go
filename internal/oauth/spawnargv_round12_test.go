package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Round 12: an apostrophe inside double quotes is literal (it must not open a
// single-quoted region that hides a following substitution), and a
// redirect-only command body is still redacted.
func TestSpawnArgv_ApostropheAndRedirectOnlyBodies(t *testing.T) {
	cases := []struct{ cmd, secret string }{
		{`exec npx srv --name "it's $(helper --password hunter2xyz)"`, "hunter2xyz"},
		{"exec npx srv --name \"it's `helper --password hunter2xyz`\"", "hunter2xyz"},
		{`exec npx srv --name "it's" "$(helper --password hunter2xyz)"`, "hunter2xyz"},
		{`exec npx srv --name "$(<$(helper --password hunter2xyz))"`, "hunter2xyz"},
		{`exec npx srv --name "$(<<<token=hunter2xyz)"`, "hunter2xyz"},
		{`exec npx srv --name "$(<$(<<<token=hunter2xyz))"`, "hunter2xyz"},
	}
	for _, c := range cases {
		argv := []string{"sh", "-c", c.cmd}
		orig := append([]string(nil), argv...)
		for name, out := range map[string]string{
			"argv":   strings.Join(AuditRedaction.SpawnArgv(argv), " "),
			"audit":  AuditRedaction.CommandString(c.cmd),
			"live":   LiveRedaction.CommandString(c.cmd),
			"spawn":  AuditRedaction.SpawnCommandString(c.cmd),
			"livesp": LiveRedaction.SpawnCommandString(c.cmd),
		} {
			assert.NotContains(t, out, c.secret, name+": "+c.cmd)
		}
		assert.Equal(t, orig, argv)
	}
}

func TestCommandString_BenignRedirectOnlyBodiesStayReadable(t *testing.T) {
	for _, cmd := range []string{
		`exec npx srv --name "$(</etc/hostname)"`,
		`exec npx srv --name "it's $(echo ok)"`,
	} {
		assert.Equal(t, cmd, LiveRedaction.CommandString(cmd))
	}
}
