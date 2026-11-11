package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Spec 123 T014 (FR-016): the boot sweep collects every (key, value) pair
// first and writes after the walk. TestOAuthSweep_SealsEveryPlaintextRow
// checks the result only, and an overwrite-only rewrite inside a ForEach
// happens to produce the same result in bbolt, so this source test pins the
// shape: every write in SealPlaintextOAuthTokens sits in a range over the
// slice collectOAuthRows returned, the sweep opens no cursor of its own, and
// collectOAuthRows itself never writes.

func parseStorageFuncs(t *testing.T, files ...string) map[string]*ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]*ast.FuncDecl{}
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				out[fd.Name.Name] = fd
			}
		}
	}
	return out
}

// sweepCallName returns the called function or method name of a call.
func sweepCallName(c *ast.CallExpr) string {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// The sweep body lives in SealOAuthTokens (oauth_tools.go, which
// SealPlaintextOAuthTokens calls); the PR-3 decrypt rewrite follows the same
// rule, so both are pinned.
func TestOAuthSweep_CollectsBeforeWriting(t *testing.T) {
	funcs := parseStorageFuncs(t, "bbolt.go", "oauth_binding.go", "oauth_tools.go")
	for _, name := range []string{"SealOAuthTokens", "DecryptOAuthTokens"} {
		t.Run(name, func(t *testing.T) { checkCollectBeforeWriting(t, funcs, name) })
	}
}

func checkCollectBeforeWriting(t *testing.T, funcs map[string]*ast.FuncDecl, fname string) {
	sweep := funcs[fname]
	if sweep == nil {
		t.Fatalf("%s not found", fname)
	}
	collect := funcs["collectOAuthRows"]
	if collect == nil {
		t.Fatal("collectOAuthRows not found in oauth_binding.go")
	}

	// The identifiers assigned from collectOAuthRows(...).
	collected := map[string]bool{}
	ast.Inspect(sweep.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 {
			return true
		}
		if c, ok := as.Rhs[0].(*ast.CallExpr); ok && sweepCallName(c) == "collectOAuthRows" {
			if id, ok := as.Lhs[0].(*ast.Ident); ok {
				collected[id.Name] = true
			}
		}
		return true
	})
	if len(collected) == 0 {
		t.Fatalf("%s must collect the rows with collectOAuthRows before writing", fname)
	}

	writes := 0
	var stack []ast.Node
	ast.Inspect(sweep.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := sweepCallName(c); name {
		case "ForEach", "Cursor", "First", "Next", "Seek", "Last", "Prev":
			t.Errorf("%s walks the bucket itself (.%s); collect with collectOAuthRows and write after", fname, name)
		case "putOAuthRecord", "putOAuthRecordKeeping", "Put", "Delete":
			writes++
			// Walk outwards: the nearest enclosing loop must be a range over
			// the collected slice, with no closure (a walk callback) between
			// the write and that loop.
			inCollectedRange := false
		outer:
			for i := len(stack) - 2; i >= 0; i-- {
				switch s := stack[i].(type) {
				case *ast.FuncLit:
					break outer
				case *ast.RangeStmt:
					if id, ok := s.X.(*ast.Ident); ok && collected[id.Name] {
						inCollectedRange = true
					}
					break outer
				case *ast.ForStmt:
					break outer
				}
			}
			if !inCollectedRange {
				t.Errorf("%s writes (%s) outside a range over the collected rows", fname, name)
			}
		}
		return true
	})
	if writes == 0 {
		t.Fatalf("%s has no write; the test premise is stale", fname)
	}

	ast.Inspect(collect.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			switch name := sweepCallName(c); name {
			case "putOAuthRecord", "Put", "Delete", "CreateBucket", "CreateBucketIfNotExists", "DeleteBucket":
				t.Errorf("collectOAuthRows must only read; it calls %s", name)
			}
		}
		return true
	})
}
