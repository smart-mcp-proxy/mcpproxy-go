package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #935: an upstream that answers with isError:true has FAILED. The
// transport call succeeded, so err == nil and every post-dispatch emit site
// used to hardcode status="success" — which silently hid the single most
// common real failure (bad argument value, unknown tool, server-side
// validation) from the tray glance, the 24h error counts and the activity log.
func TestActivityStatusForResult(t *testing.T) {
	tests := []struct {
		name       string
		result     interface{}
		wantStatus string
		wantErrMsg string
	}{
		{
			name:       "nil result is a success",
			result:     nil,
			wantStatus: "success",
			wantErrMsg: "",
		},
		{
			name:       "ordinary result is a success",
			result:     mcp.NewToolResultText("42"),
			wantStatus: "success",
			wantErrMsg: "",
		},
		{
			name:       "non-CallToolResult value is a success",
			result:     map[string]any{"ok": true},
			wantStatus: "success",
			wantErrMsg: "",
		},
		{
			name:       "isError result is an error carrying the upstream text",
			result:     mcp.NewToolResultError("Invalid timezone: 'Mars/Olympus'"),
			wantStatus: "error",
			wantErrMsg: "Invalid timezone: 'Mars/Olympus'",
		},
		{
			name: "isError result by value is classified too",
			result: mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{mcp.NewTextContent("unknown tool: nope")},
			},
			wantStatus: "error",
			wantErrMsg: "unknown tool: nope",
		},
		{
			name: "multiple text blocks are joined",
			result: &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					mcp.NewTextContent("validation failed:"),
					mcp.NewTextContent("field 'timezone' is required"),
				},
			},
			wantStatus: "error",
			wantErrMsg: "validation failed:\nfield 'timezone' is required",
		},
		{
			name: "isError with no usable content falls back to a fixed message",
			result: &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{mcp.NewImageContent("Zm9v", "image/png")},
			},
			wantStatus: "error",
			wantErrMsg: upstreamErrorResultMessage,
		},
		{
			name: "isError with only structured content serialises it",
			result: &mcp.CallToolResult{
				IsError:           true,
				StructuredContent: map[string]any{"code": "E_BAD_ARG"},
			},
			wantStatus: "error",
			wantErrMsg: `{"code":"E_BAD_ARG"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, errMsg := activityStatusForResult(tt.result)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantErrMsg, errMsg)
		})
	}
}

// The activity log is not a place to dump a multi-megabyte upstream error
// body: every record is persisted and streamed to the UI. The message is
// capped, and the cap must never split a multi-byte rune.
func TestActivityStatusForResult_TruncatesLongErrors(t *testing.T) {
	long := strings.Repeat("é", activityErrorMessageLimit)
	status, errMsg := activityStatusForResult(mcp.NewToolResultError(long))

	require.Equal(t, "error", status)
	assert.LessOrEqual(t, len(errMsg), activityErrorMessageLimit+len(activityErrorMessageEllipsis))
	assert.True(t, strings.HasSuffix(errMsg, activityErrorMessageEllipsis),
		"a truncated message must be marked as truncated, got %q", errMsg)
	assert.True(t, strings.HasPrefix(long, strings.TrimSuffix(errMsg, activityErrorMessageEllipsis)),
		"truncation must keep a valid prefix of the original text")
}

// Guard against the regression coming back through a new call path (same
// technique as TestActivityIDsAreNeverMintedInline). A post-dispatch emit site
// that hardcodes the literal "success" cannot have consulted the result, so it
// will mis-classify an isError answer no matter how good the classifier is.
//
// statusArgIndex maps an emit helper to the 0-based position of its `status`
// parameter.
//
// Only emitActivityToolCallCompleted is covered: every one of its call sites
// reports an UPSTREAM dispatch, so every one of them must consult the result.
// emitActivityInternalToolCall is deliberately excluded — most of its call
// sites report on mcpproxy's OWN handlers (retrieve_tools, describe_tool,
// upstream_servers …), where a literal "success" is correct. The one site that
// mirrors an upstream dispatch (call_tool_*) is covered by the end-to-end test
// TestE2E_UpstreamIsErrorRecordedAsActivityError instead.
var statusArgIndex = map[string]int{
	// (serverName, toolName, sessionID, requestID, source, status, ...)
	"emitActivityToolCallCompleted": 5,
}

func TestActivityCompletionNeverHardcodesSuccess(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoErrorf(t, err, "parsing %s", name)

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			idx, tracked := statusArgIndex[sel.Sel.Name]
			if !tracked || len(call.Args) <= idx {
				return true
			}
			lit, ok := call.Args[idx].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || lit.Value != `"success"` {
				return true
			}
			offenders = append(offenders,
				fset.Position(lit.Pos()).String()+" ("+sel.Sel.Name+")")
			return true
		})
	}

	require.Emptyf(t, offenders,
		"these emit a hardcoded success status and therefore record an "+
			"isError:true upstream answer as a success (issue #935); classify "+
			"the dispatched result with activityStatusForResult instead:\n%s",
		strings.Join(offenders, "\n"))
}
