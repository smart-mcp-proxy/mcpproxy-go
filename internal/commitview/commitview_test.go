package commitview

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCommitviewIsLeaf pins review R7.2: the package shared by configsvc and
// storage must import nothing from this module, or the two packages could end
// up importing each other through it.
func TestCommitviewIsLeaf(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/smart-mcp-proxy/mcpproxy-go/") &&
			dep != "github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview" {
			t.Errorf("commitview must stay a leaf package, but depends on %s", dep)
		}
	}
}

func TestServerGenIsZero(t *testing.T) {
	if !(ServerGen{}).IsZero() {
		t.Fatal("zero value must report IsZero")
	}
	if (ServerGen{Gen: 1}).IsZero() {
		t.Fatal("a stamped value must not report IsZero")
	}
}
