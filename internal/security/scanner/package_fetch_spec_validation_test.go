package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestIsBareRegistrySpec is the MCP-2442 (CRITICAL/P0) guard. The package-fetch
// scan path must only ever hand the package manager a BARE registry name.
// pip/uv/npm execute the package's build backend (setup.py / PEP 517) for
// local-path, git+/VCS, URL, and file: specs even with --only-binary /
// --ignore-scripts — arbitrary code execution during a supposedly static scan.
func TestIsBareRegistrySpec(t *testing.T) {
	cases := []struct {
		spec      string
		ecosystem string
		want      bool
	}{
		// Accepted: bare registry names (+ optional version pin).
		{"requests", "python", true},
		{"requests==2.31.0", "python", true},
		{"Flask>=2.0", "python", true},
		{"mcp-server-git", "python", true},
		{"package_with_underscores", "python", true},
		{"left-pad", "npm", true},
		{"left-pad@1.3.0", "npm", true},
		{"@modelcontextprotocol/server-everything", "npm", true},
		{"@scope/name@1.2.3", "npm", true},

		// Rejected (Python): paths, VCS, URLs, file:, direct refs.
		{"./local-pkg", "python", false},
		{"../evil", "python", false},
		{"/abs/path/pkg", "python", false},
		{"~user/pkg", "python", false},
		{"git+https://github.com/evil/repo.git", "python", false},
		{"git+ssh://git@github.com/evil/repo.git", "python", false},
		{"hg+https://example.com/repo", "python", false},
		{"https://evil.example.com/pkg.tar.gz", "python", false},
		{"file:./local", "python", false},
		{"file:///abs/pkg", "python", false},
		{"requests @ https://evil.example.com/x.whl", "python", false},
		{"pkg; rm -rf /", "python", false},
		{"a\\b", "python", false},
		{"", "python", false},
		{"  spaced  ", "python", false},

		// Rejected (npm): the slash-name path must not let a real filesystem path through.
		{"./local-pkg", "npm", false},
		{"/abs/path", "npm", false},
		{"git+https://github.com/evil/repo.git", "npm", false},
		{"file:../local", "npm", false},
		{"https://evil.example.com/x.tgz", "npm", false},
		{"@scope/../escape", "npm", false},
	}
	for _, c := range cases {
		t.Run(c.ecosystem+"/"+c.spec, func(t *testing.T) {
			if got := isBareRegistrySpec(c.spec, c.ecosystem); got != c.want {
				t.Errorf("isBareRegistrySpec(%q, %q) = %v, want %v", c.spec, c.ecosystem, got, c.want)
			}
		})
	}
}

// TestResolveFromPackageFetch_RejectsNonRegistrySpec proves the validation
// short-circuits BEFORE any download command runs, for both ecosystems.
func TestResolveFromPackageFetch_RejectsNonRegistrySpec(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	cases := []ServerInfo{
		{Name: "py-git", Command: "uvx", Args: []string{"--from", "git+https://github.com/evil/repo.git", "evil"}},
		{Name: "py-local", Command: "uvx", Args: []string{"--from", "/tmp/evil-pkg", "evil"}},
		{Name: "npm-url", Command: "npx", Args: []string{"https://evil.example.com/x.tgz"}},
	}
	for _, info := range cases {
		t.Run(info.Name, func(t *testing.T) {
			_, err := r.resolveFromPackageFetch(context.Background(), info)
			if err == nil {
				t.Fatalf("expected rejection for non-registry spec, got nil error")
			}
			if !strings.Contains(err.Error(), "non-registry") {
				t.Errorf("expected a non-registry rejection, got: %v", err)
			}
		})
	}
}

// TestResolveFromPackageFetch_LocalPathSpecDoesNotExecute is the execution-marker
// proof requested by MCP-2442. A uvx server configured with a LOCAL PATH whose
// setup.py writes a marker file must never have that marker appear — the spec is
// rejected at validation, so pip/uv is never invoked. This holds regardless of
// whether uv/pip is installed on the host (validation fires first).
func TestResolveFromPackageFetch_LocalPathSpecDoesNotExecute(t *testing.T) {
	pkgDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "EXECUTED")
	// A malicious setup.py that, if built/executed, would create the marker.
	setupPy := "import pathlib; pathlib.Path(" + strconv.Quote(marker) + ").write_text('pwned')\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "setup.py"), []byte(setupPy), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewSourceResolver(zap.NewNop())
	info := ServerInfo{Name: "evil-local", Command: "uvx", Args: []string{"--from", pkgDir, "evil"}}

	if _, err := r.resolveFromPackageFetch(context.Background(), info); err == nil {
		t.Fatalf("expected local-path spec to be rejected")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("setup.py was EXECUTED during scan — marker file %q exists (arbitrary code execution)", marker)
	}
}
