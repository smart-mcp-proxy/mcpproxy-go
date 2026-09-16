package config

// Spec 107 FR-035 latent-symbols guard (task T008).
//
// FR-031/FR-033 delete the never-wired credential-brokering code (router,
// tool filter, workspace manager, token exchanger, credential resolver, header
// injector, IdP subject-token capture, the brokered transport seam) and FR-032
// removes the dead config knobs and the never-implemented auth_broker modes.
// This test walks the NON-TEST Go AST of the packages that hosted that code
// and fails while any of the removed declarations still exists, so the cut
// cannot silently regress.
//
// The walk uses go/parser directly, so build tags are ignored: the
// //go:build server files are inspected under both `go test ./internal/config`
// and `go test -tags server ./internal/config`, and the verdict is the same in
// both editions.
//
// Compatibility literals the same FRs REQUIRE to exist are exempt by name and
// must never be flagged here (they are proven by the SC-005 load/write-back
// fixtures instead): the retained `StoreIDPTokens` decoder field and its
// deprecation warning (FR-033), and the key/mode strings inside the
// server-build normaliser and its warnings (FR-032). The mode-literal check
// is therefore confined to the validator functions that carry the accepted
// mode set, never applied package-wide.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// latentGuardRoots are the directories the guard walks, relative to the repo
// root. Recursive entries end in "/...".
var latentGuardRoots = []string{
	"internal/serveredition/...",
	"internal/transport",
	"internal/upstream/core",
	"internal/config",
}

// removedDecl names one declaration FR-031/FR-032/FR-033 delete. An empty Pkg
// or Recv matches any package / any receiver (or struct) in the walked set.
// Recv scopes methods to their receiver type and struct fields to their
// struct; the task text names the scoped ones explicitly
// (`workspace.Manager`, `(*OAuthConnector).Refresh`, "the fields ...").
type removedDecl struct {
	Pkg  string
	Recv string
	Name string
	Why  string
}

var removedDecls = []removedDecl{
	// FR-031 class A: multi-user router / tool filter / workspaces. Scoped to
	// package multiuser: the live `serveredition.Dependencies.Router` field is
	// the chi.Router every feature mounts on (registry.go) and must stay.
	{Pkg: "multiuser", Name: "Router", Why: "multiuser.Router never wired (FR-031)"},
	{Pkg: "multiuser", Name: "NewRouter", Why: "multiuser.NewRouter never wired (FR-031)"},
	{Pkg: "multiuser", Name: "ToolFilter", Why: "multiuser.ToolFilter never wired (FR-031)"},
	{Pkg: "multiuser", Name: "NewToolFilter", Why: "multiuser.NewToolFilter never wired (FR-031)"},
	{Pkg: "workspace", Name: "Manager", Why: "package internal/serveredition/workspace deleted (FR-031)"},
	// FR-031 class A: broker exchange / resolve / inject chain.
	{Name: "TokenExchanger", Why: "broker.TokenExchanger deleted (FR-031)"},
	{Name: "CredentialResolver", Why: "broker.CredentialResolver deleted (FR-031)"},
	{Name: "HeaderInjector", Why: "broker.HeaderInjector deleted (FR-031)"},
	{Name: "ConnectionKey", Why: "broker.ConnectionKey deleted (FR-031)"},
	{Name: "AuditActionInject", Why: "dead audit constant (FR-031)"},
	{Name: "AuditActionAcquire", Why: "dead audit constant (FR-031)"},
	{Name: "AuditActionRefresh", Why: "dead audit constant (FR-031)"},
	{Name: "AuditMethodTokenExchange", Why: "dead audit constant (FR-031)"},
	{Name: "AuditMethodEntraOBO", Why: "dead audit constant (FR-031)"},
	{Name: "auditMethodForMode", Why: "dead audit mapping (FR-031)"},
	// FR-031/FR-033: IdP subject-token capture and refresh.
	{Name: "GetValidIDPSubjectToken", Why: "IdP subject-token reader deleted (FR-033)"},
	{Name: "ErrReauthRequired", Why: "IdP subject-token reader deleted (FR-033)"},
	{Name: "RefreshAccessToken", Why: "OAuthProvider.RefreshAccessToken deleted (FR-031)"},
	{Recv: "OAuthConnector", Name: "Refresh", Why: "(*OAuthConnector).Refresh deleted (FR-031)"},
	// FR-031: resolver-only seams on the credential handlers.
	{Name: "ConnectorProvider", Why: "broker.ConnectorProvider interface + (*CredentialHandlers).ConnectorProvider deleted (FR-031)"},
	{Name: "ConnectorFor", Why: "connectorProvider.ConnectorFor deleted (FR-031)"},
	// FR-031: brokered transport seam compiled into the personal binary.
	{Name: "SetBrokeredAuth", Why: "core.(*Client).SetBrokeredAuth deleted (FR-031)"},
	{Name: "brokeredAuth", Why: "core.Client.brokeredAuth field and its branches deleted (FR-031)"},
	{Name: "BrokeredAuth", Why: "transport.BrokeredAuth type + HTTPTransportConfig.BrokeredAuth deleted (FR-031)"},
	{Name: "EffectiveHeaders", Why: "transport.EffectiveHeaders deleted (FR-031)"},
	{Name: "refuseBrokeredOAuth", Why: "transport brokered branch deleted (FR-031)"},
	// FR-032: dead config knobs.
	{Pkg: "config", Recv: "ServerEditionConfig", Name: "MaxUserServers", Why: "dead knob removed (FR-032)"},
	{Pkg: "config", Recv: "ServerEditionConfig", Name: "WorkspaceIdleTimeout", Why: "dead knob removed (FR-032)"},
	{Pkg: "config", Recv: "AuthBrokerConfig", Name: "Header", Why: "injection header removed (FR-032)"},
	{Pkg: "config", Recv: "AuthBrokerConfig", Name: "HeaderFormat", Why: "injection header format removed (FR-032)"},
}

// removedAuthBrokerModes are the never-implemented modes FR-032 removes from
// the validator's accepted set. They are only forbidden INSIDE the validator
// functions below; the server-build normaliser (T018) legitimately carries
// the same strings in its key/mode table and warnings and is exempt.
var removedAuthBrokerModes = []string{"token_exchange", "entra_obo"}

// removedAuthBrokerModeIdents are the constant identifiers the validator uses
// today to spell the accepted set (auth_broker.go). Their presence inside a
// validator body is the same violation as the literal.
var removedAuthBrokerModeIdents = []string{"AuthBrokerModeTokenExchange", "AuthBrokerModeEntraOBO"}

// authBrokerValidatorFuncs are the functions whose bodies define the accepted
// mode set. The task text names validateServerAuthBroker; on HEAD that
// function delegates to (*AuthBrokerConfig).Validate, where the switch
// actually lives, so both are inspected.
var authBrokerValidatorFuncs = []removedDecl{
	{Pkg: "config", Name: "validateServerAuthBroker"},
	{Pkg: "config", Recv: "AuthBrokerConfig", Name: "Validate"},
}

// exemptDeclNames are compatibility declarations the guard must never flag
// (FR-033 retained decoder field). Listed by name so a future edit to the
// forbidden table cannot accidentally catch them.
var exemptDeclNames = map[string]string{
	"StoreIDPTokens": "retained decoder field; `true` logs one deprecation warning (FR-033)",
}

// latentDecl is one declaration found by the AST walk.
type latentDecl struct {
	Pkg  string
	Kind string // type, func, method, field, const, var
	Recv string // receiver type for methods, struct/interface name for fields
	Name string
	Pos  token.Position
}

func (d latentDecl) String() string {
	owner := d.Name
	if d.Recv != "" {
		owner = d.Recv + "." + d.Name
	}
	return fmt.Sprintf("%s %s.%s at %s:%d", d.Kind, d.Pkg, owner, d.Pos.Filename, d.Pos.Line)
}

func latentGuardRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}

// latentGuardFiles lists every non-test .go file under the guard roots.
func latentGuardFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, entry := range latentGuardRoots {
		recursive := strings.HasSuffix(entry, "/...")
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(entry, "/...")))
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("guard root %s is not a directory: %v", dir, err)
		}
		err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path == dir {
					return nil
				}
				if !recursive || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("guard walked no files; roots are wrong")
	}
	return files
}

func latentTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return latentTypeName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr: // generic receiver T[P]
		return latentTypeName(e.X)
	case *ast.IndexListExpr:
		return latentTypeName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

// latentCollectDecls collects top-level declarations plus struct fields and
// interface methods from one parsed file.
func latentCollectDecls(fset *token.FileSet, f *ast.File) ([]latentDecl, map[string]*ast.FuncDecl) {
	pkg := f.Name.Name
	var out []latentDecl
	funcs := map[string]*ast.FuncDecl{}
	add := func(kind, recv, name string, pos token.Pos) {
		out = append(out, latentDecl{Pkg: pkg, Kind: kind, Recv: recv, Name: name, Pos: fset.Position(pos)})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			recv := ""
			kind := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recv = latentTypeName(d.Recv.List[0].Type)
				kind = "method"
			}
			add(kind, recv, d.Name.Name, d.Name.Pos())
			funcs[recv+"."+d.Name.Name] = d
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					add("type", "", s.Name.Name, s.Name.Pos())
					switch tt := s.Type.(type) {
					case *ast.StructType:
						for _, field := range tt.Fields.List {
							for _, n := range field.Names {
								add("field", s.Name.Name, n.Name, n.Pos())
							}
						}
					case *ast.InterfaceType:
						for _, field := range tt.Methods.List {
							for _, n := range field.Names {
								add("method", s.Name.Name, n.Name, n.Pos())
							}
						}
					}
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, n := range s.Names {
						add(kind, "", n.Name, n.Pos())
					}
				}
			}
		}
	}
	return out, funcs
}

func (r removedDecl) matches(d latentDecl) bool {
	if r.Name != d.Name {
		return false
	}
	if r.Pkg != "" && r.Pkg != d.Pkg {
		return false
	}
	if r.Recv != "" && r.Recv != d.Recv {
		return false
	}
	return true
}

// TestLatentSymbolsGuard_RemovedDeclarationsAbsent fails while any FR-031 /
// FR-032 / FR-033 declaration still exists in the walked packages, or while
// the auth_broker validator still accepts token_exchange / entra_obo.
func TestLatentSymbolsGuard_RemovedDeclarationsAbsent(t *testing.T) {
	root := latentGuardRepoRoot(t)
	files := latentGuardFiles(t, root)
	fset := token.NewFileSet()

	var violations []string
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		decls, funcs := latentCollectDecls(fset, f)

		for _, d := range decls {
			if _, exempt := exemptDeclNames[d.Name]; exempt {
				continue
			}
			for _, r := range removedDecls {
				if r.matches(d) {
					violations = append(violations, fmt.Sprintf("%s — %s", latentRel(root, d), r.Why))
				}
			}
		}

		for _, vf := range authBrokerValidatorFuncs {
			if f.Name.Name != vf.Pkg {
				continue
			}
			fn, ok := funcs[vf.Recv+"."+vf.Name]
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind != token.STRING {
						return true
					}
					val, err := strconv.Unquote(x.Value)
					if err != nil {
						val = x.Value
					}
					for _, mode := range removedAuthBrokerModes {
						if strings.Contains(val, mode) {
							pos := fset.Position(x.Pos())
							violations = append(violations, fmt.Sprintf("literal %q in %s at %s:%d — mode %q removed from the accepted set (FR-032)",
								val, latentFuncLabel(vf), latentRelPath(root, pos.Filename), pos.Line, mode))
						}
					}
				case *ast.Ident:
					for _, id := range removedAuthBrokerModeIdents {
						if x.Name == id {
							pos := fset.Position(x.Pos())
							violations = append(violations, fmt.Sprintf("identifier %s in %s at %s:%d — mode constant removed from the accepted set (FR-032)",
								id, latentFuncLabel(vf), latentRelPath(root, pos.Filename), pos.Line))
						}
					}
				}
				return true
			})
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("Spec 107 FR-035: %d removed declaration(s)/branch(es) still present (FR-031/FR-032/FR-033):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// TestLatentSymbolsGuard_CompatibilityDeclarationsRetained pins the exemption:
// the `store_idp_tokens` decoder field stays (FR-033) so old configs load, and
// the guard above must never list it.
func TestLatentSymbolsGuard_CompatibilityDeclarationsRetained(t *testing.T) {
	root := latentGuardRepoRoot(t)
	fset := token.NewFileSet()
	path := filepath.Join(root, "internal", "config", "server_edition_config.go")
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	decls, _ := latentCollectDecls(fset, f)
	found := false
	for _, d := range decls {
		if d.Kind == "field" && d.Recv == "ServerEditionConfig" && d.Name == "StoreIDPTokens" {
			found = true
		}
		for _, r := range removedDecls {
			if r.Name == d.Name {
				if _, exempt := exemptDeclNames[d.Name]; exempt {
					t.Errorf("exempt declaration %s is also in the removed table; fix the table", d.Name)
				}
			}
		}
	}
	if !found {
		t.Errorf("ServerEditionConfig.StoreIDPTokens must remain as a retained decoder field (FR-033); it is missing from %s", latentRelPath(root, path))
	}
}

func latentRel(root string, d latentDecl) string {
	d.Pos.Filename = latentRelPath(root, d.Pos.Filename)
	return d.String()
}

func latentRelPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

func latentFuncLabel(vf removedDecl) string {
	if vf.Recv != "" {
		return "(*" + vf.Recv + ")." + vf.Name
	}
	return vf.Name
}
