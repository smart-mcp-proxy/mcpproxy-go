package runtime

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// Spec 116 PR2 review round 1 (Sol): a server view pairs the displayed
// configuration with the incarnation of the SAME committed server. Each test
// replaces the server (remove + re-add with a different command) right after
// the view read its server data; the view must not show the old command
// with the replacement's incarnation, which the Web UI would echo back as
// expected_incarnation (FR-017).

func replacementWithCommand(name, command string) func(t *testing.T, rt *Runtime) {
	return func(t *testing.T, rt *Runtime) {
		sc := manualStdio(name, false)
		sc.Command = command
		removeAndReAdd(t, rt, sc)
	}
}

func TestServerReview_R1_ConfigAndIncarnationOneSnapshot(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	var once sync.Once
	rt.testHooks.serverViewAfterRead = func() {
		once.Do(func() { replacementWithCommand("acme", "replacement-cmd")(t, rt) })
	}
	review, err := rt.GetServerReview(context.Background(), "acme")
	require.NoError(t, err)
	cur, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	require.NotEqual(t, old.Incarnation, cur.Incarnation)
	switch review.Server.Incarnation {
	case old.Incarnation:
		assert.Equal(t, "true", review.Server.Command, "old incarnation pairs with the old command")
	case cur.Incarnation:
		assert.Equal(t, "replacement-cmd", review.Server.Command, "new incarnation pairs with the new command")
	default:
		t.Fatalf("unexpected incarnation %d", review.Server.Incarnation)
	}
}

func TestGetAllServers_R1_ConfigAndIncarnationOneSnapshot(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, manualStdio("acme", false))
	old, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	// The supervisor's StateView lags the commit: it still carries the old
	// server's config when the replacement has been committed.
	require.NotNil(t, rt.Supervisor())
	oldCfg := rt.ConfigSnapshot().GetServer("acme")
	rt.Supervisor().StateView().UpdateServer("acme", func(st *stateview.ServerStatus) {
		st.Name = "acme"
		st.Config = oldCfg
	})
	var once sync.Once
	rt.testHooks.serverViewAfterRead = func() {
		once.Do(func() { replacementWithCommand("acme", "replacement-cmd")(t, rt) })
	}
	rows, err := rt.GetAllServers()
	require.NoError(t, err)
	cur, ok := rt.ConfigSnapshot().ServerGeneration("acme")
	require.True(t, ok)
	var row map[string]interface{}
	for _, m := range rows {
		if m["name"] == "acme" {
			row = m
		}
	}
	require.NotNil(t, row, "rows: %v", rows)
	inc, has := row["incarnation"]
	if !has {
		return // no identity offered: nothing to echo back
	}
	switch inc {
	case old.Incarnation:
		assert.Equal(t, "true", row["command"], "old incarnation pairs with the old command")
	case cur.Incarnation:
		assert.Equal(t, "replacement-cmd", row["command"], "new incarnation pairs with the new command")
	default:
		t.Fatalf("unexpected incarnation %v", inc)
	}
}

// Review round 1 (Sol): replay binds its history write to the incarnation of
// the server it resolves the client for. The incarnation is captured BEFORE
// the client is resolved (a removal or replacement in between then drops the
// write instead of recording the old client's call under the new identity),
// and a missing identity is rejected instead of being ignored (an ignored
// false made the write unconditional).
func TestReplayToolCall_R1_IncarnationCapturedBeforeClient(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runtime.go", nil, 0)
	require.NoError(t, err)
	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ReplayToolCall" || fn.Body == nil {
			return true
		}
		body = fn.Body
		return false
	})
	require.NotNil(t, body)
	var incPos, clientPos token.Pos
	blankOK := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Rhs) == 1 && len(node.Lhs) == 2 {
				if call, ok := node.Rhs[0].(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ServerIncarnation" {
						incPos = node.Pos()
						if id, ok := node.Lhs[1].(*ast.Ident); ok && id.Name == "_" {
							blankOK = true
						}
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "GetClient" && !clientPos.IsValid() {
				clientPos = node.Pos()
			}
		}
		return true
	})
	require.True(t, incPos.IsValid(), "ReplayToolCall captures the server incarnation")
	require.True(t, clientPos.IsValid())
	assert.False(t, blankOK, "a missing server identity must be rejected, not ignored")
	assert.Less(t, int(incPos), int(clientPos), "the incarnation is captured before the client is resolved")
}
