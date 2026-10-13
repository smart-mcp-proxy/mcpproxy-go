package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Round 16: recursion into a nested command must not overwrite a value the
// flat pass already masked whole (a flag's value that merely looks like a command).
func TestSpawnArgv_Round16(t *testing.T) {
	cases := []string{
		`exec npx srv --password 'hunter2xyz --token=othersecret and more'`,
		`exec sh -c "exec npx srv --password 'hunter2xyz --token=othersecret and more'"`,
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
			assert.NotContains(t, out, "hunter2xyz", name+": "+cmd+" -> "+out)
			assert.NotContains(t, out, "othersecret", name+": "+cmd+" -> "+out)
			assert.NotContains(t, out, "and more", name+": "+cmd+" -> "+out)
			assert.Contains(t, out, "npx srv", name+": "+cmd+" -> "+out)
		}
		assert.Equal(t, orig, argv)
	}
}
