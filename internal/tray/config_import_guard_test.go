package tray

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// configPkg is the import path of the config package.
const configPkg = "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// forbiddenConfigIO are the config-package entry points that touch mcp_config.json.
//
// The architectural rule (CLAUDE.md) is that the tray holds no state and talks
// to the core only over socket/REST + SSE. Loading or saving the config file
// makes the tray a second, competing source of truth — the class of bug that
// produced the dead OAuth-path config read this guard was added alongside.
//
// The rule bans config *file I/O*, not the config package outright: referencing
// a pure type such as config.LogConfig (the tray's own logger settings, built
// in memory — see cmd/mcpproxy-tray/main.go) reads nothing and is fine.
var forbiddenConfigIO = map[string]bool{
	"LoadFromFile":        true,
	"Load":                true,
	"LoadOrCreateConfig":  true,
	"SaveConfig":          true,
	"SaveConfigToDataDir": true,
}

// trayDirs are the tray-side packages the rule applies to.
//
// Bootstrap reads are deliberately out of scope: resolving the socket path, the
// config *path* (without parsing it), or the CA cert are how the tray finds the
// core in the first place, and none of them call the functions above.
var trayDirs = []string{
	".",
	filepath.Join("..", "..", "cmd", "mcpproxy-tray"),
}

// TestTrayDoesNotReadConfigFile fails if any tray-side source file calls a
// config-file loader or saver. It parses the files on disk rather than relying
// on the compiler, so a violation cannot hide behind a build tag that happens
// not to be active on the machine running the test.
func TestTrayDoesNotReadConfigFile(t *testing.T) {
	fset := token.NewFileSet()

	for _, dir := range trayDirs {
		// parser.ParseDir ignores build constraints — every file is checked on
		// every platform, which is the point.
		pkgs, err := parser.ParseDir(fset, dir, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}

		scanned := 0
		for _, pkg := range pkgs {
			for path, file := range pkg.Files {
				if strings.HasSuffix(path, "_test.go") {
					continue // this guard names the symbols itself
				}
				scanned++

				// Find the local alias for the config package in this file, if
				// it is imported at all. Without it, a `config.Load` selector
				// cannot refer to the config package.
				alias := configAlias(file)
				if alias == "" {
					continue
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
					if !forbiddenConfigIO[sel.Sel.Name] {
						return true
					}
					t.Errorf(
						"%s calls config.%s.\n\n"+
							"The tray must not read or write mcp_config.json — it holds no state and "+
							"talks to the core over socket/REST + SSE (CLAUDE.md). Fetch what you need "+
							"from the core API instead.",
						fset.Position(sel.Pos()), sel.Sel.Name,
					)
					return true
				})
			}
		}

		// Guard the guard: a typo'd path would silently scan nothing and pass.
		if scanned == 0 {
			t.Fatalf("no non-test Go files found in %s — the guard is not scanning anything", dir)
		}
	}
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
				// Blank/dot imports cannot produce a `config.Load` selector we
				// could attribute; nothing to check.
				return ""
			}
			return imp.Name.Name
		}
		return "config"
	}
	return ""
}
