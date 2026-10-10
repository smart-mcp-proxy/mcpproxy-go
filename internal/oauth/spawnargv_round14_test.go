package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Round 14: an escaped apostrophe inside $'...' within a substitution, and a
// quoted nested `sh -c` operand, must not hide a credential.
func TestSpawnArgv_Round14(t *testing.T) {
	cases := []string{
		`exec npx srv --name "$(printf %s $'it\'s ')" --password 'two hunter2xyz'`,
		`exec sh -c 'exec npx srv --password hunter2xyz'`,
		`exec bash -lc "exec npx srv --password hunter2xyz"`,
	}
	for _, cmd := range cases {
		argv := []string{"sh", "-c", cmd}
		orig := append([]string(nil), argv...)
		for name, out := range map[string]string{
			"argv":   strings.Join(AuditRedaction.SpawnArgv(argv), " "),
			"audit":  AuditRedaction.CommandString(cmd),
			"live":   LiveRedaction.CommandString(cmd),
			"spawn":  AuditRedaction.SpawnCommandString(cmd),
			"livesp": LiveRedaction.SpawnCommandString(cmd),
		} {
			assert.NotContains(t, out, "hunter2xyz", name+": "+cmd)
			assert.NotContains(t, out, "two", name+": "+cmd+" -> "+out)
		}
		assert.Equal(t, orig, argv)
	}
	out := LiveRedaction.CommandString(`exec sh -c 'exec npx srv --password hunter2xyz'`)
	assert.Contains(t, out, "npx srv")
}
