package tray

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// configPkg is the import path of the config package.
const configPkg = "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// allowedConfigSymbols are the ONLY config-package symbols tray-side code may
// reference.
//
// The architectural rule (CLAUDE.md) is that the tray holds no state and talks
// to the core only over socket/REST + SSE. Loading or saving mcp_config.json
// makes the tray a second, competing source of truth — the class of bug that
// produced the dead OAuth-path config read this guard was added alongside.
//
// This is an ALLOWLIST, not a denylist, and that is deliberate: it fails CLOSED.
// A file-I/O helper added to the config package tomorrow is caught here by
// default, whereas a denylist of today's loader names would silently miss it.
// (An earlier denylist did miss config.CreateSampleConfig, which writes a
// config file via SaveConfig.)
//
// LogConfig is a plain struct type describing the tray's OWN logger, built in
// memory from logs.DefaultLogConfig() — see cmd/mcpproxy-tray/main.go. It reads
// nothing from disk, so it is allowed.
var allowedConfigSymbols = map[string]bool{
	"LogConfig": true,
}

// trayRoots are the tray-side trees the rule applies to. Both are walked
// RECURSIVELY: cmd/mcpproxy-tray has subpackages (internal/api, internal/monitor,
// internal/state) that are every bit as tray-side as its root.
//
// Bootstrap reads stay out of scope by construction: resolving the socket path,
// the config *path* (without parsing it), or the CA cert touches none of the
// config package.
var trayRoots = []string{
	".",
	filepath.Join("..", "..", "cmd", "mcpproxy-tray"),
}

// TestTrayDoesNotReadConfigFile fails if any tray-side source file uses the
// config package for anything but the allowlisted pure types.
//
// It parses sources on disk rather than relying on the compiler, so a violation
// cannot hide behind a build tag that happens to be inactive on the machine
// running the test.
func TestTrayDoesNotReadConfigFile(t *testing.T) {
	fset := token.NewFileSet()
	scanned := 0

	for _, root := range trayRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil // this guard names the symbols itself
			}

			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			scanned++

			// Without an import of the config package, a `config.X` selector in
			// this file cannot refer to it — it is a local variable named config
			// (cmd/mcpproxy-tray/internal/monitor has several).
			alias := configAlias(file)
			if alias == "" {
				return nil
			}

			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != alias {
					return true
				}
				if allowedConfigSymbols[sel.Sel.Name] {
					return true
				}
				t.Errorf(
					"%s uses config.%s.\n\n"+
						"Tray-side code must not read or write mcp_config.json — the tray holds no "+
						"state and talks to the core over socket/REST + SSE (CLAUDE.md). Fetch what "+
						"you need from the core API instead.\n"+
						"If config.%s is genuinely a pure in-memory type that touches no file, add it "+
						"to allowedConfigSymbols with a comment saying why.",
					fset.Position(sel.Pos()), sel.Sel.Name, sel.Sel.Name,
				)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	// Guard the guard: a typo'd root would silently scan nothing and pass.
	if scanned == 0 {
		t.Fatal("no non-test Go files found — the guard is not scanning anything")
	}
	t.Logf("scanned %d tray-side source files", scanned)
}

// configAlias returns the name the config package is bound to in this file
// ("config", or an explicit alias), or "" if the file does not import it.
func configAlias(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != configPkg {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				// Blank/dot imports produce no attributable `config.X` selector.
				return ""
			}
			return imp.Name.Name
		}
		return "config"
	}
	return ""
}
