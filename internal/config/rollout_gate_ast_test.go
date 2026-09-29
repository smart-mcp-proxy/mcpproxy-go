package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyGateAST_IsCompileTimeEnabledAndHasNoTestOverride(t *testing.T) {
	root := latentGuardRepoRoot(t)
	path := filepath.Join(root, "internal", "config", "profiles.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse profiles.go: %v", err)
	}
	constFound := false
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if name.Name != "policyEnforcementReadyBase" {
					continue
				}
				constFound = true
				if i >= len(value.Values) {
					t.Errorf("policyEnforcementReadyBase must be the compile-time constant true")
					continue
				}
				actual, isIdent := value.Values[i].(*ast.Ident)
				if !isIdent || actual.Name != "true" {
					t.Errorf("policyEnforcementReadyBase must be the compile-time constant true")
				}
			}
		}
	}
	if !constFound {
		t.Fatal("policyEnforcementReadyBase const not found")
	}

	var gate *ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "PolicyEnforcementReady" {
			gate = fn
			break
		}
	}
	if gate == nil || gate.Body == nil || len(gate.Body.List) != 1 {
		t.Fatal("PolicyEnforcementReady must be a single-return gate")
	}
	ret, ok := gate.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		t.Fatal("PolicyEnforcementReady must return only its compile-time constant")
	}
	id, ok := ret.Results[0].(*ast.Ident)
	if !ok || id.Name != "policyEnforcementReadyBase" {
		t.Fatal("PolicyEnforcementReady must return policyEnforcementReadyBase")
	}

	forbidden := map[string]bool{
		strings.Join([]string{"EnablePolicy", "ForTest"}, ""):           true,
		strings.Join([]string{"policyEnforcement", "TestOverride"}, ""): true,
	}
	var violations []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "frontend", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok && forbidden[ident.Name] {
				violations = append(violations, latentRelPath(root, path)+":"+ident.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk production Go files: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("test-only rollout override identifiers remain in production Go files: %v", violations)
	}
}
