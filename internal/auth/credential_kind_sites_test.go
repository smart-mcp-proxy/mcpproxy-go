package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// credentialKindAgentTokenSites classifies every use of CredentialKindAgentToken
// in non-test Go code under internal/ and cmd/ (Spec 132 R3, T025). A browser
// login carries CredentialKindOAuth, so any code that asked "is this the agent
// token kind?" to mean "scoped caller" would silently treat a login as
// something else. Each site is therefore either:
//
//   - also_oauth: the decision must also hold for CredentialKindOAuth. The code
//     goes through CredentialKind.IsScopedClientCredential (or an equivalent
//     that includes oauth) instead of comparing with the constant.
//   - api_key_only: the site is specific to a stored mcp_agt_/mcp_cli_ token.
//
// Key: "<path relative to the module root>#<enclosing function>".
var credentialKindAgentTokenSites = map[string]struct{ class, reason string }{
	"internal/auth/context.go#IsScopedClientCredential": {"also_oauth",
		"the helper every scoped-caller decision goes through; it matches agent_token AND oauth"},
	"internal/auth/agent_token.go#AuthContext": {"api_key_only",
		"builds the context of a STORED token; a browser login is built by ClientAuthContext with CredentialKindOAuth"},
	"internal/runtime/clients_service.go#ActorFromContext": {"api_key_only",
		"fallback label for a REST context that never stamped a credential kind; the browser-login context always stamps oauth, so this branch is unreachable for it"},
}

func TestCredentialKindAgentTokenSitesClassified(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		_ = filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Errorf("parse %s: %v", path, perr)
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.SelectorExpr:
						if x.Sel.Name == "CredentialKindAgentToken" {
							found[rel+"#"+fn.Name.Name] = true
						}
					case *ast.Ident:
						if x.Name == "CredentialKindAgentToken" {
							found[rel+"#"+fn.Name.Name] = true
						}
					}
					return true
				})
			}
			return nil
		})
	}

	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry, ok := credentialKindAgentTokenSites[k]
		if !ok {
			t.Errorf("unclassified use of CredentialKindAgentToken at %s: classify it in credentialKindAgentTokenSites as also_oauth or api_key_only", k)
			continue
		}
		if entry.class != "also_oauth" && entry.class != "api_key_only" {
			t.Errorf("%s: class %q must be also_oauth or api_key_only", k, entry.class)
		}
		if strings.TrimSpace(entry.reason) == "" {
			t.Errorf("%s: a classification needs a one-line reason", k)
		}
	}
	for k := range credentialKindAgentTokenSites {
		if !found[k] {
			t.Errorf("stale entry %s: the site no longer uses CredentialKindAgentToken", k)
		}
	}
}
