package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Round 13: a sensitive NAME=value assignment is masked by its NAME even when
// shell decoding changes nothing, and an escaped apostrophe inside $'...' does
// not close the region early and hide a following substitution.
func TestSpawnArgv_PlainEnvAssignAndANSICEscapedQuote(t *testing.T) {
	cases := []string{
		`API_KEY=@hunter2xyz exec npx srv`,
		`CLIENT_SECRET=@hunter2xyz exec npx srv`,
		`exec npx srv --name $'it\'s '$(helper --password hunter2xyz)`,
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
		}
		assert.Equal(t, orig, argv)
	}
}
