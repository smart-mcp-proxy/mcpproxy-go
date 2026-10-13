package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A redirection is not an argument, but its target text still reaches the log:
// a here-string or filename carrying a credential must be redacted by value.
// Command substitutions are ONE word: the value of a sensitive flag is the whole
// `$(...)` / backtick run, not just its opening fragment.
func TestSpawnArgv_RedirectTargetsAndSubstitutions(t *testing.T) {
	cases := []struct{ cmd, secret string }{
		{"exec npx srv <<<token=hunter2xyz", "hunter2xyz"},
		{"exec npx srv <<< token=hunter2xyz", "hunter2xyz"},
		{"exec npx srv <<< 'ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789'", "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"},
		{"exec npx srv <<< 'PASSWORD=hunter2xyz more'", "hunter2xyz"},
		{"exec npx srv >/tmp/token=hunter2xyz", "hunter2xyz"},
		{"exec npx srv 2>ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789", "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"},
		{"cd /o'brien; exec npx srv <<<token=hunter2xyz", "hunter2xyz"},
		{"exec npx srv --password $(printf %s hunter2xyz)", "hunter2xyz"},
		{"exec npx srv --password=$(printf %s hunter2xyz)", "hunter2xyz"},
		{"exec npx srv --password `printf %s hunter2xyz`", "hunter2xyz"},
		{"exec npx srv --password=`printf %s hunter2xyz`", "hunter2xyz"},
		{"exec npx srv --password \"$(printf %s hunter2xyz)\"", "hunter2xyz"},
		{"exec npx srv --password $(printf '%s' \"hunter2xyz more\")", "more"},
		{"exec npx srv --password $(echo $(printf %s hunter2xyz))", "hunter2xyz"},
		{"exec npx srv --password $(printf ')' ; echo hunter2xyz)", "hunter2xyz"},
		{"cd /o'brien; exec npx srv --password $(printf %s hunter2xyz)", "hunter2xyz"},
		{"exec npx srv --password $(printf %s hunter2xyz", "hunter2xyz"},
	}
	for _, c := range cases {
		argv := []string{"sh", "-c", c.cmd}
		orig := append([]string(nil), argv...)
		for name, out := range map[string]string{
			"argv":  strings.Join(AuditRedaction.SpawnArgv(argv), " "),
			"audit": AuditRedaction.CommandString(c.cmd),
			"live":  LiveRedaction.CommandString(c.cmd),
			"spawn": AuditRedaction.SpawnCommandString(c.cmd),
		} {
			assert.NotContains(t, out, c.secret, name+": "+c.cmd)
		}
		assert.Equal(t, orig, argv, "stored args must be unchanged")
	}
}

func TestCommandString_BenignRedirectsAndSubstitutionsReadable(t *testing.T) {
	for _, cmd := range []string{
		"exec npx srv <<< hello",
		"exec npx srv 2>/tmp/a.log >>/tmp/b.log",
		"exec npx srv --port $(echo 8080) --name `hostname`",
	} {
		assert.Equal(t, cmd, LiveRedaction.CommandString(cmd))
	}
}
