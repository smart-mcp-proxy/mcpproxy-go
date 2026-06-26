package detect

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImports enforces the offline guarantee (FR-001): the detect package
// (engine + checks) must never reach the network, spawn processes, touch the
// filesystem, or talk to Docker. This is the standing offline gate — extend the
// denylist, never relax it.
//
// Exact-match paths and prefixes are both checked. Test files (_test.go) are
// excluded from the scan because tests legitimately need os/parser to inspect
// the source tree.
var (
	forbiddenExact = map[string]struct{}{
		"net":           {},
		"net/http":      {},
		"net/url":       {},
		"os":            {},
		"os/exec":       {},
		"io/ioutil":     {},
		"path/filepath": {},
	}
	forbiddenPrefixes = []string{
		"github.com/docker/",
		"github.com/moby/",
		"google.golang.org/grpc",
		// detect MUST NOT import scanner: the scanner wiring (T012) imports
		// detect to delegate tpa-descriptions, so a back-import here would form
		// a cycle. detect stays self-contained (its own Finding + vocab
		// constants); scanner converts detect.Finding → scanner.ScanFinding.
		"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner",
	}
)

func TestNoForbiddenImports_OfflineGate(t *testing.T) {
	root := "." // the detect package directory, scanned recursively for checks/
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if _, bad := forbiddenExact[p]; bad {
				t.Errorf("%s imports forbidden package %q (violates offline FR-001)", path, p)
				continue
			}
			for _, pre := range forbiddenPrefixes {
				if strings.HasPrefix(p, pre) {
					t.Errorf("%s imports forbidden package %q (violates offline FR-001)", path, p)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
