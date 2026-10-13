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
	// FR-031 names "CredentialResolver with its interfaces/errors": the
	// resolver's collaborator interfaces, its not-connected error type, the
	// policy hook and the three sentinel errors (credential_resolver.go at
	// origin/main b39800a89). Scoped to package broker.
	{Pkg: "broker", Name: "Exchanger", Why: "resolver collaborator interface deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "Connector", Why: "resolver collaborator interface deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "NotConnectedError", Why: "resolver error type deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "PolicyHook", Why: "resolver policy hook deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "PolicyHookFunc", Why: "resolver policy hook deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "ErrUnauthenticated", Why: "resolver sentinel error deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "ErrNoCredential", Why: "resolver sentinel error deleted with the resolver (FR-031)"},
	{Pkg: "broker", Name: "ErrBrokerNotConfigured", Why: "resolver sentinel error deleted with the resolver (FR-031)"},
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
	{Name: "persistIDPSubjectToken", Why: "IdP subject-token writer deleted (FR-033, T015)"},
	{Recv: "OAuthHandler", Name: "SetCredentialStore", Why: "the login handler no longer holds a credential store to write IdP tokens into (FR-033, T015)"},
	{Recv: "OAuthHandler", Name: "credStore", Why: "the login handler's credential-store field left with the IdP subject-token writer (FR-033, T015)"},
	{Name: "OfflineAuthParams", Why: "offline-access authorization parameters deleted; login never requests a refresh token (FR-033, T015)"},
	{Name: "OfflineAccessScopes", Why: "offline_access scope set deleted; login never requests a refresh token (FR-033, T015)"},
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
	// FR-031 deletes "every brokered branch" in transport/http.go:178-192 and
	// upstream/core/connection_http.go:171-192; these are the helpers those
	// branches were (origin/main b39800a89), not just the exported seam.
	{Pkg: "transport", Recv: "HTTPTransportConfig", Name: "effectiveHeaders", Why: "transport brokered header merge deleted (FR-031, T016)"},
	{Pkg: "core", Recv: "Client", Name: "canUseHeadersStrategy", Why: "brokered strategy gate deleted (FR-031, T016)"},
	{Pkg: "core", Recv: "Client", Name: "brokeredHTTPConfig", Why: "brokered transport-config builder deleted (FR-031, T016)"},
	// FR-032: dead config knobs.
	{Pkg: "config", Recv: "ServerEditionConfig", Name: "MaxUserServers", Why: "dead knob removed (FR-032)"},
	{Pkg: "config", Recv: "ServerEditionConfig", Name: "WorkspaceIdleTimeout", Why: "dead knob removed (FR-032)"},
	{Pkg: "config", Recv: "AuthBrokerConfig", Name: "Header", Why: "injection header removed (FR-032)"},
	{Pkg: "config", Recv: "AuthBrokerConfig", Name: "HeaderFormat", Why: "injection header format removed (FR-032)"},
}

// removedFile is one non-test Go file FR-031 / FR-033 delete outright, with
// EVERY top-level declaration it held (types, funcs, methods, consts, vars)
// at the merge base. FR-035 asks for "every Go symbol FR-031 and FR-033
// delete ... checked by name over the non-test AST", so the table is the
// mechanical dump of each file's AST at origin/main b39800a89, not a curated
// highlight list: restoring any one of those files, or a fragment of one,
// under any file name trips the guard. Methods are scoped to their receiver
// and every entry to its package, so an unrelated `Error` method or a helper
// of the same name elsewhere in the walked set is not matched.
type removedFile struct {
	File  string
	FR    string
	Decls []removedDecl
}

var removedFiles = []removedFile{
	{File: "internal/serveredition/auth/idp_subject_token.go", FR: "FR-033", Decls: []removedDecl{
		{Pkg: "auth", Name: "idpSubjectTokenType"},
		{Pkg: "auth", Name: "idpRefreshSkew"},
		{Pkg: "auth", Name: "ErrReauthRequired"},
		{Pkg: "auth", Recv: "OAuthHandler", Name: "persistIDPSubjectToken"},
		{Pkg: "auth", Recv: "OAuthHandler", Name: "GetValidIDPSubjectToken"},
		{Pkg: "auth", Name: "expiryFromExpiresIn"},
		{Pkg: "auth", Name: "splitScopes"},
		{Pkg: "auth", Name: "chooseScopes"},
		{Pkg: "auth", Name: "firstNonEmpty"},
	}},
	{File: "internal/serveredition/broker/credential_resolver.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "broker", Name: "defaultRefreshThreshold"},
		{Pkg: "broker", Name: "ErrUnauthenticated"},
		{Pkg: "broker", Name: "ErrNoCredential"},
		{Pkg: "broker", Name: "ErrBrokerNotConfigured"},
		{Pkg: "broker", Name: "Exchanger"},
		{Pkg: "broker", Name: "Connector"},
		{Pkg: "broker", Name: "ConnectorProvider"},
		{Pkg: "broker", Name: "NotConnectedError"},
		{Pkg: "broker", Recv: "NotConnectedError", Name: "Error"},
		{Pkg: "broker", Name: "PolicyDecision"},
		{Pkg: "broker", Name: "PolicyInput"},
		{Pkg: "broker", Name: "PolicyHook"},
		{Pkg: "broker", Name: "PolicyHookFunc"},
		{Pkg: "broker", Recv: "PolicyHookFunc", Name: "Evaluate"},
		{Pkg: "broker", Name: "allowAllPolicy"},
		{Pkg: "broker", Recv: "allowAllPolicy", Name: "Evaluate"},
		{Pkg: "broker", Name: "PolicyDeniedError"},
		{Pkg: "broker", Recv: "PolicyDeniedError", Name: "Error"},
		{Pkg: "broker", Name: "ResolverDeps"},
		{Pkg: "broker", Name: "CredentialResolver"},
		{Pkg: "broker", Name: "acquisition"},
		{Pkg: "broker", Name: "NewCredentialResolver"},
		{Pkg: "broker", Recv: "CredentialResolver", Name: "Resolve"},
		{Pkg: "broker", Recv: "CredentialResolver", Name: "emitAudit"},
		{Pkg: "broker", Name: "auditReason"},
		{Pkg: "broker", Recv: "CredentialResolver", Name: "acquire"},
		{Pkg: "broker", Recv: "CredentialResolver", Name: "notConnected"},
		{Pkg: "broker", Recv: "CredentialResolver", Name: "connectorFor"},
	}},
	{File: "internal/serveredition/broker/injector.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "broker", Name: "ErrBrokerStdioUnsupported"},
		{Pkg: "broker", Name: "fallbackBrokerHeader"},
		{Pkg: "broker", Name: "fallbackBrokerHeaderFormat"},
		{Pkg: "broker", Name: "resolver"},
		{Pkg: "broker", Name: "HeaderInjector"},
		{Pkg: "broker", Name: "NewHeaderInjector"},
		{Pkg: "broker", Recv: "HeaderInjector", Name: "InjectFor"},
		{Pkg: "broker", Name: "ConnectionKey"},
	}},
	{File: "internal/serveredition/broker/token_exchanger.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "broker", Name: "grantTypeTokenExchange"},
		{Pkg: "broker", Name: "grantTypeJWTBearer"},
		{Pkg: "broker", Name: "tokenTypeAccessToken"},
		{Pkg: "broker", Name: "entraRequestedTokenUse"},
		{Pkg: "broker", Name: "defaultExchangeTimeout"},
		{Pkg: "broker", Name: "TokenExchanger"},
		{Pkg: "broker", Name: "NewTokenExchanger"},
		{Pkg: "broker", Name: "tokenResponse"},
		// tokenErrorResponse is NOT listed: oauth_connector.go kept its own copy of the RFC 6749 error body.
		{Pkg: "broker", Recv: "TokenExchanger", Name: "Exchange"},
		{Pkg: "broker", Name: "buildExchangeForm"},
		{Pkg: "broker", Recv: "TokenExchanger", Name: "post"},
		{Pkg: "broker", Recv: "TokenExchanger", Name: "sanitizedError"},
		{Pkg: "broker", Name: "credentialFromResponse"},
	}},
	{File: "internal/serveredition/multiuser/router.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "multiuser", Name: "ServerOwnership"},
		{Pkg: "multiuser", Name: "OwnershipShared"},
		{Pkg: "multiuser", Name: "OwnershipPersonal"},
		{Pkg: "multiuser", Name: "ServerInfo"},
		{Pkg: "multiuser", Name: "Router"},
		{Pkg: "multiuser", Name: "NewRouter"},
		{Pkg: "multiuser", Recv: "Router", Name: "GetUserServers"},
		{Pkg: "multiuser", Recv: "Router", Name: "GetServerForUser"},
		{Pkg: "multiuser", Recv: "Router", Name: "BrokeredConnectionKey"},
		{Pkg: "multiuser", Name: "nonUserPoolPrefix"},
		{Pkg: "multiuser", Name: "brokerPoolIdentity"},
		{Pkg: "multiuser", Recv: "Router", Name: "IsServerAccessible"},
		{Pkg: "multiuser", Recv: "Router", Name: "UpdateSharedServers"},
		{Pkg: "multiuser", Recv: "Router", Name: "GetSharedServerNames"},
		{Pkg: "multiuser", Recv: "Router", Name: "isSharedServer"},
	}},
	{File: "internal/serveredition/multiuser/tool_filter.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "multiuser", Name: "ToolInfo"},
		{Pkg: "multiuser", Name: "ToolFilter"},
		{Pkg: "multiuser", Name: "NewToolFilter"},
		{Pkg: "multiuser", Recv: "ToolFilter", Name: "FilterToolsByUser"},
		{Pkg: "multiuser", Recv: "ToolFilter", Name: "GetAccessibleServerNames"},
		{Pkg: "multiuser", Recv: "ToolFilter", Name: "IsToolAccessible"},
	}},
	{File: "internal/serveredition/workspace/manager.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "workspace", Name: "Manager"},
		{Pkg: "workspace", Name: "NewManager"},
		{Pkg: "workspace", Recv: "Manager", Name: "GetOrCreateWorkspace"},
		{Pkg: "workspace", Recv: "Manager", Name: "GetWorkspace"},
		{Pkg: "workspace", Recv: "Manager", Name: "RemoveWorkspace"},
		{Pkg: "workspace", Recv: "Manager", Name: "ActiveWorkspaceCount"},
		{Pkg: "workspace", Recv: "Manager", Name: "StartCleanup"},
		{Pkg: "workspace", Recv: "Manager", Name: "Stop"},
		{Pkg: "workspace", Recv: "Manager", Name: "cleanupIdle"},
	}},
	{File: "internal/serveredition/workspace/workspace.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "workspace", Name: "UserWorkspace"},
		{Pkg: "workspace", Name: "NewUserWorkspace"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "LoadServers"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "GetServers"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "GetServer"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "AddServer"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "RemoveServer"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "UpdateServer"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "ServerNames"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "Touch"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "LastAccess"},
		{Pkg: "workspace", Recv: "UserWorkspace", Name: "Shutdown"},
	}},
	{File: "internal/transport/broker_auth.go", FR: "FR-031", Decls: []removedDecl{
		{Pkg: "transport", Name: "BrokeredAuth"},
		{Pkg: "transport", Name: "tokenPlaceholder"},
		{Pkg: "transport", Recv: "BrokeredAuth", Name: "HeaderValue"},
		{Pkg: "transport", Name: "EffectiveHeaders"},
	}},
}

// removedPackages are whole Go packages FR-031 deletes. Any non-test file in
// the walked set that declares one of these package names is a violation on
// its own, whatever it contains.
var removedPackages = map[string]string{
	"workspace": "package internal/serveredition/workspace deleted (FR-031)",
}

// allRemovedDecls is the curated table plus every declaration of every
// deleted file, flattened once for the walk.
func allRemovedDecls() []removedDecl {
	out := append([]removedDecl(nil), removedDecls...)
	for _, rf := range removedFiles {
		for _, d := range rf.Decls {
			d.Why = fmt.Sprintf("declared in %s at origin/main b39800a89, deleted by %s", rf.File, rf.FR)
			out = append(out, d)
		}
	}
	return out
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
	// The mode-literal scan is only meaningful if every validator it targets
	// was actually found and inspected; a renamed validator must fail the
	// guard, never pass it vacuously.
	seenValidators := map[string]bool{}
	removed := allRemovedDecls()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if why, gone := removedPackages[f.Name.Name]; gone {
			violations = append(violations, fmt.Sprintf("file %s declares package %s — %s", latentRelPath(root, path), f.Name.Name, why))
		}
		decls, funcs := latentCollectDecls(fset, f)

		for _, d := range decls {
			if _, exempt := exemptDeclNames[d.Name]; exempt {
				continue
			}
			// One violation per declaration: the curated table and the
			// per-file dump overlap on the headline symbols by design.
			for _, r := range removed {
				if r.matches(d) {
					violations = append(violations, fmt.Sprintf("%s — %s", latentRel(root, d), r.Why))
					break
				}
			}
		}

		for _, vf := range authBrokerValidatorFuncs {
			if f.Name.Name != vf.Pkg {
				continue
			}
			fn, ok := funcs[vf.Recv+"."+vf.Name]
			if !ok {
				// A validator that is not in THIS file may live in a sibling;
				// seenValidators proves each one was inspected somewhere.
				continue
			}
			if fn.Body == nil {
				continue
			}
			seenValidators[latentFuncLabel(vf)] = true
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

	for _, vf := range authBrokerValidatorFuncs {
		if !seenValidators[latentFuncLabel(vf)] {
			t.Errorf("auth_broker validator %s (package %s) was not found in the walked files; the accepted-mode scan cannot run without it — update authBrokerValidatorFuncs if it moved", latentFuncLabel(vf), vf.Pkg)
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
		for _, r := range allRemovedDecls() {
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

// TestLatentSymbolsGuard_DeletedFilesAbsent pins the file-level half of the
// cut: every source file the removedFiles table was dumped from is gone, and
// the deleted workspace package directory with it. The declaration walk above
// catches a fragment restored under a new name; this catches the whole file
// coming back verbatim, before anyone reads the longer report.
func TestLatentSymbolsGuard_DeletedFilesAbsent(t *testing.T) {
	root := latentGuardRepoRoot(t)
	seen := map[string]bool{}
	for _, rf := range removedFiles {
		if seen[rf.File] {
			t.Errorf("removedFiles lists %s twice; fix the table", rf.File)
		}
		seen[rf.File] = true
		if len(rf.Decls) == 0 {
			t.Errorf("removedFiles entry %s has no declarations; the dump is incomplete", rf.File)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rf.File))); err == nil {
			t.Errorf("%s exists again; FR-031/FR-033 delete it (%s)", rf.File, rf.FR)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "serveredition", "workspace")); err == nil {
		t.Errorf("internal/serveredition/workspace exists again; FR-031 deletes the package")
	}
}
