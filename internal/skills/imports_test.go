package skills

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The package is a leaf so storage, runtime and the fixture can all import it
// without pulling in the upstream client.
func TestPackageStaysALeaf(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	require.NoError(t, err)

	var self, module string
	for _, p := range strings.Fields(string(out)) {
		if strings.HasSuffix(p, "/internal/skills") {
			self = p
			module = strings.TrimSuffix(p, "/internal/skills")
		}
	}
	require.NotEmpty(t, self, "go list did not report the package itself")

	for _, p := range strings.Fields(string(out)) {
		if p != self && strings.HasPrefix(p, module+"/") {
			t.Errorf("internal/skills must not import another package of this module, found %s", p)
		}
	}
}
