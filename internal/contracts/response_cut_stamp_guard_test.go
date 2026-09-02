package contracts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The guard that is supposed to make a sixth round impossible.
//
// There are two enforcement layers, and this is the SECOND one.
//
// The first — and the one that actually matters — is the COMPILER. Setting
// response_truncated is only reachable through two emit helpers, and both take
// a contracts.ResponseCut rather than a bool:
//
//	Runtime.EmitActivityToolCallCompleted(..., responseCut contracts.ResponseCut, ...)
//	Runtime.EmitActivityInternalToolCallTruncated(..., responseCut contracts.ResponseCut)
//
// They derive the boolean from the stamp (ResponseCut.Cuts), so a new emitter
// cannot flag a truncation without naming which copies the cut shortened: not
// by convention, by type error. `true` does not compile there.
//
// This test closes the remaining hole — code that bypasses the emitters and
// writes storage.ActivityRecord.ResponseTruncated (or the contracts mirror)
// directly, which a converter, a projection or a future migration might do. The
// rule is simple and mechanical: wherever ResponseTruncated is written,
// ResponseTruncationCut must be written too. Neither may travel alone.
func TestEveryResponseTruncatedWriteAlsoWritesTheStamp(t *testing.T) {
	root := repoRoot(t)
	checked := 0

	for _, dir := range []string{"internal", "cmd", "bench"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			lines := sourceLines(t, path)
			ast.Inspect(file, func(n ast.Node) bool {
				if exempt(lines, fset, n) {
					return true
				}
				switch node := n.(type) {
				case *ast.CompositeLit:
					// A literal `false` is exempt: it constructs a record with
					// no cut, where the stamp's own zero value already says the
					// same thing and cannot contradict it. Anything else — a
					// literal true, or a variable whose value this file cannot
					// see — is a claim that a cut happened, and a claim about a
					// cut without a direction is the defect.
					if !setsFieldToSomethingOtherThanFalse(node, fieldTruncated) {
						return true
					}
					checked++
					require.True(t, hasKey(node, fieldStamp),
						"%s: a composite literal sets %s without %s. The direction of a "+
							"response cut is a property of the emitter that made it, never of the "+
							"record type — set both, or emit through "+
							"Runtime.EmitActivityToolCallCompleted / "+
							"EmitActivityInternalToolCallTruncated, which derive the flag from the stamp.",
						pos(fset, node), fieldTruncated, fieldStamp)
				case *ast.FuncDecl:
					if node.Body == nil {
						return true
					}
					if !assignsField(node.Body, fieldTruncated) {
						return true
					}
					checked++
					require.True(t, assignsField(node.Body, fieldStamp),
						"%s: %s assigns .%s without assigning .%s anywhere in the same function. "+
							"The two must move together: a flag with no direction is what five "+
							"review rounds were spent guessing at.",
						pos(fset, node), node.Name.Name, fieldTruncated, fieldStamp)
				}
				return true
			})
		})
	}

	// A guard that inspects nothing passes vacuously. The production writers
	// are the two activity_service.go record literals, the two httpapi
	// converters and the exclude_payloads projection; tests add more.
	require.GreaterOrEqual(t, checked, 5,
		"the guard found almost no %s writers — it is probably not walking the tree it thinks it is",
		fieldTruncated)
}

const (
	fieldTruncated = "ResponseTruncated"
	fieldStamp     = "ResponseTruncationCut"

	// exemptMarker is the ONE way out of this guard, and it exists for exactly
	// one thing: constructing a record that models an older core's output, so a
	// test can prove the direction-free path is reached. It is a comment rather
	// than a filename allowlist so the reason sits beside the code, and so
	// `grep` finds every deliberate unstamped record in the tree.
	exemptMarker = "unstamped-legacy-record-on-purpose"
)

// exempt reports whether the marker comment appears on, or just above, this
// node. The window is the node's own lines plus the two above it, which covers
// both a trailing comment and a short explanatory comment over the literal.
func exempt(lines []string, fset *token.FileSet, n ast.Node) bool {
	// ast.Inspect calls the visitor with nil when it finishes a subtree.
	if n == nil {
		return false
	}
	start := fset.Position(n.Pos()).Line - 2
	end := fset.Position(n.End()).Line
	if start < 1 {
		start = 1
	}
	for i := start; i <= end && i <= len(lines); i++ {
		if strings.Contains(lines[i-1], exemptMarker) {
			return true
		}
	}
	return false
}

func sourceLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Split(string(data), "\n")
}

func hasKey(lit *ast.CompositeLit, name string) bool {
	return keyValue(lit, name) != nil
}

// setsFieldToSomethingOtherThanFalse is hasKey minus the one exempt case: a
// bare `false`, which asserts no cut and so needs no direction.
func setsFieldToSomethingOtherThanFalse(lit *ast.CompositeLit, name string) bool {
	v := keyValue(lit, name)
	if v == nil {
		return false
	}
	ident, ok := v.(*ast.Ident)
	return !ok || ident.Name != "false"
}

func keyValue(lit *ast.CompositeLit, name string) ast.Expr {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == name {
			return kv.Value
		}
	}
	return nil
}

// assignsField reports whether the body contains `<expr>.<name> = ...`. Only
// assignment counts: reading the field (a converter's right-hand side, a
// predicate) says nothing about whether a record is being constructed.
//
// Unlike the composite-literal rule there is no `false` exemption here, and the
// difference is deliberate. A literal builds a fresh record where the stamp's
// zero value already agrees with a false flag; an assignment MUTATES a record
// that may already carry a stamp, so clearing one field and not the other
// leaves the record self-contradictory — which is exactly what the
// exclude_payloads projection would have done.
func assignsField(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				found = true
			}
		}
		return true
	})
	return found
}

func pos(fset *token.FileSet, n ast.Node) string {
	return fset.Position(n.Pos()).String()
}

func walkGoFiles(t *testing.T, dir string, fn func(string, *ast.File, *token.FileSet)) {
	t.Helper()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		// Tests are walked too, on purpose: a test that builds a record with a
		// bare truncated flag is a test asserting a state production cannot
		// produce, and it is how a stale expectation outlives the code.
		parsed, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil // not our business to police unparsable files
		}
		fn(path, parsed, fset)
		return nil
	})
	require.NoError(t, err)
}

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the module root from " + dir)
	return ""
}
