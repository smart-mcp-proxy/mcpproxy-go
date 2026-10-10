package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Round 11: a double-quoted command substitution has its own quote context, and
// a sensitive flag INSIDE a substitution is masked even when the substitution
// is grouped as the value of a benign outer flag.
func TestSpawnArgv_QuotedAndNestedSubstitutions(t *testing.T) {
	cases := []struct{ cmd, secret string }{
		{`exec npx srv --password "$(printf '%s' "hunter2xyz more")"`, "more"},
		{`exec npx srv --password "$(printf '%s' "hunter2xyz more")"`, "hunter2xyz"},
		{`exec npx srv --password "$(echo "$(printf '%s' "hunter2xyz more")")"`, "more"},
		{"exec npx srv --password \"`printf '%s' \"hunter2xyz more\"`\"", "more"},
		{`exec npx srv --name $(helper --password hunter2xyz)`, "hunter2xyz"},
		{"exec npx srv --name `helper --password hunter2xyz`", "hunter2xyz"},
		{`exec npx srv --name "$(helper --password hunter2xyz)"`, "hunter2xyz"},
		{`exec npx srv positional$(helper --api-key hunter2xyz)tail`, "hunter2xyz"},
		{`exec npx srv --name $(a $(helper --password hunter2xyz))`, "hunter2xyz"},
		{`exec npx srv >$(helper --password hunter2xyz)`, "hunter2xyz"},
		{`exec npx srv --name=$(helper --password hunter2xyz)`, "hunter2xyz"},
		{`cd /o'brien; exec npx srv --name $(helper --password hunter2xyz)`, "hunter2xyz"},
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

func TestCommandString_BenignSubstitutionsStayReadable(t *testing.T) {
	for _, cmd := range []string{
		`exec npx srv --name "$(echo "a b")" --port $(echo 8080)`,
	} {
		assert.Equal(t, cmd, LiveRedaction.CommandString(cmd))
	}
	// The log-only spawn relaxation applies inside substitutions too.
	cmd := "exec npx srv --name $(helper --max-tokens 4096)"
	assert.Equal(t, cmd, LiveRedaction.SpawnCommandString(cmd))
}
