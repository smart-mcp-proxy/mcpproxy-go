package shellwrap

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A word that starts with '#' begins a comment in `sh -c`, so an unquoted
// "#general" would silently drop it and every argument after it.
func TestShellescape_HashSurvivesShell(t *testing.T) {
	args := []string{"#general", "--port", "3000", "a#b"}
	assert.Equal(t, "'#general'", shellescapeFor("#general", false))

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on PATH")
	}
	parts := []string{"printf", "'%s\\n'"}
	for _, a := range args {
		parts = append(parts, shellescapeFor(a, false))
	}
	out, err := exec.Command(sh, "-c", strings.Join(parts, " ")).Output()
	require.NoError(t, err)
	assert.Equal(t, args, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"))
}
