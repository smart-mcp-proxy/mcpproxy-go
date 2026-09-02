package contracts

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The THIRD enforcement layer, and the one that catches what neither of the
// other two can. (The first is the compiler; the second is
// TestEveryResponseTruncatedWriteAlsoWritesTheStamp, in this package.)
//
// The compiler forces a direction onto the two emit helpers that CAN flag a
// cut. It cannot force anything onto a handler that cuts its own text and then
// reports through emitActivityInternalToolCall — the whole-response wrapper,
// which correctly hardcodes contracts.CutNone. That call compiles, type-checks,
// and records a genuine cut as no cut at all. handleReadCache
// (internal/server/mcp.go) did exactly that, and survived the entire
// typed-stamp conversion because the conversion was keyed off the EMITTERS: the
// type system can say "you flagged a cut without naming it", never "you cut the
// text and then called the emitter that cannot name it".
//
// So this guard is keyed off the TRUNCATION CALL SITES instead — the closed set
// of things in the tree that shorten a response. Once a function has called one
// of them, every activity-completion record it emits AFTERWARDS must name a
// direction. Truncate-then-emit-unstamped fails the build.
//
// Only non-test files are walked. A test may legitimately truncate a fixture
// and then emit an unstamped record — that is how the direction-free legacy
// path gets exercised — and the rule here is about what PRODUCTION records.
func TestTruncatingEmittersNameTheirCut(t *testing.T) {
	root := repoRoot(t)

	seenTruncator := map[string]bool{}
	truncatingFuncs := 0
	stampedEmitsAfterTruncation := 0

	for _, dir := range []string{"internal", "cmd", "bench"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				// A function NAMED for a call site is that call site, not a
				// caller of it: skip the declarations themselves, including
				// maybeTruncateAndCacheText's delegation to the Pinned form and
				// EmitActivityInternalToolCall's to the Truncated one.
				if truncationCallSites[fn.Name.Name] || unstampedCompletionEmitters[fn.Name.Name] || stampedCompletionEmitters[fn.Name.Name] {
					continue
				}

				calls := calledFunctions(fn.Body)
				firstCut := token.NoPos
				for _, c := range calls {
					if !truncationCallSites[c.name] {
						continue
					}
					seenTruncator[c.name] = true
					if firstCut == token.NoPos || c.pos < firstCut {
						firstCut = c.pos
					}
				}
				if firstCut == token.NoPos {
					continue
				}
				truncatingFuncs++

				for _, c := range calls {
					if c.pos < firstCut {
						// Emitted before anything was cut — a validation or
						// error path returning above the truncator. There is no
						// cut yet, so there is no direction to name.
						continue
					}
					if stampedCompletionEmitters[c.name] {
						stampedEmitsAfterTruncation++
						continue
					}
					require.False(t, unstampedCompletionEmitters[c.name],
						"%s: %s truncates its response (via %s) and then records it through %s, "+
							"which hardcodes contracts.CutNone — so a real cut is stored as no cut. "+
							"Emit through emitActivityInternalToolCallTruncated / "+
							"emitActivityToolCallCompleted and name which copies the cut shortened "+
							"(agentOnlyCut is the stamp every built-in truncation carries). "+
							"The direction is a property of the emitter that made the cut; no "+
							"consumer can recover it from the record type.",
						fset.Position(c.pos), fn.Name.Name, cutNameAt(calls, firstCut), c.name)
				}
			}
		})
	}

	// Three anti-vacuity checks, because a guard keyed off NAMES dies silently
	// when one of them is renamed and nothing else changes.
	for name := range truncationCallSites {
		require.True(t, seenTruncator[name],
			"%q is in this guard's closed set of truncating call sites but is never called "+
				"in non-test code. Either it was renamed — update the set, or the guard stops "+
				"watching that path — or it is dead and should leave the set.", name)
	}
	require.GreaterOrEqual(t, truncatingFuncs, 5,
		"the guard found almost no truncating functions — it is probably not walking the tree it thinks it is")
	require.Positive(t, stampedEmitsAfterTruncation,
		"no stamped emit was found after any truncation, so the after-the-cut half of this walk proves nothing")
}

// truncationCallSites is the closed set of things that SHORTEN a response.
// Everything that cuts response text in this tree goes through one of them:
//
//	currentTruncator                — the live truncate.Truncator snapshot (#861)
//	forwardContentResult            — upstream tool_call forward truncation
//	maybeTruncateAndCacheText       — a built-in's own response, cut and cached
//	maybeTruncateAndCacheTextPinned — the same, with a pinned record path
//	subCallActivityOutcome          — a sandbox sub-call, cut for the log only
//	safeTruncateBytes               — the rune-safe byte cut used where there is
//	                                  no Truncator: the direct-routing handler
//	                                  (makeDirectModeHandler, mcp_routing.go)
//	                                  cuts its content blocks inline with it
//
// safeTruncateBytes is the reason this set is not simply "the Truncator helpers".
// A handler that shortens text by hand is still a truncating emitter, and the
// direct surface does exactly that; watching only the Truncator would leave that
// whole routing mode outside the guard. Its other callers cut something that is
// not a response (an upstream error message) and emit nothing, so they cost
// nothing here.
//
// Matched by NAME, not by type: this test lives in contracts and deliberately
// does not import internal/server, which imports contracts. The anti-vacuity
// loop above is what keeps a rename from quietly emptying the set.
var truncationCallSites = map[string]bool{
	"currentTruncator":                true,
	"forwardContentResult":            true,
	"maybeTruncateAndCacheText":       true,
	"maybeTruncateAndCacheTextPinned": true,
	"subCallActivityOutcome":          true,
	"safeTruncateBytes":               true,
}

// unstampedCompletionEmitters write a completion record whose response cut they
// cannot describe: they pass contracts.CutNone and offer no way to say anything
// else. Correct for a handler that returned exactly what it recorded, and a
// silent understatement for one that did not.
var unstampedCompletionEmitters = map[string]bool{
	"emitActivityInternalToolCall": true,
	"EmitActivityInternalToolCall": true,
}

// stampedCompletionEmitters take a contracts.ResponseCut, so the compiler
// already forces the direction onto them.
var stampedCompletionEmitters = map[string]bool{
	"emitActivityInternalToolCallTruncated": true,
	"EmitActivityInternalToolCallTruncated": true,
	"emitActivityToolCallCompleted":         true,
	"EmitActivityToolCallCompleted":         true,
}

type calledFunc struct {
	name string
	pos  token.Pos
}

// calledFunctions lists every call in the body by the identifier being called —
// `f(...)` and `x.f(...)` alike — with the position of its opening paren, which
// is what orders "before the cut" against "after the cut".
func calledFunctions(body *ast.BlockStmt) []calledFunc {
	var out []calledFunc
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			out = append(out, calledFunc{fun.Name, call.Lparen})
		case *ast.SelectorExpr:
			out = append(out, calledFunc{fun.Sel.Name, call.Lparen})
		}
		return true
	})
	return out
}

// cutNameAt reports which truncating call site sits at pos, for the failure
// message — "it truncates" is far easier to act on when it says with what.
func cutNameAt(calls []calledFunc, pos token.Pos) string {
	for _, c := range calls {
		if c.pos == pos {
			return c.name
		}
	}
	return "a truncator"
}
