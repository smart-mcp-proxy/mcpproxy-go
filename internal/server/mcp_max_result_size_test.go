package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnnotateToolsWithMaxResultSize exercises the shared helper that
// injects the `anthropic/maxResultSizeChars` annotation into every tool's
// _meta field. Claude Code raises its inline-response ceiling from 50k to
// up to 500k chars when it sees this annotation (otherwise large responses
// are spilled to disk and replaced with a 2KB preview).
func TestAnnotateToolsWithMaxResultSize(t *testing.T) {
	t.Run("adds _meta on every tool", func(t *testing.T) {
		tools := []mcp.Tool{
			mcp.NewTool("foo"),
			mcp.NewTool("bar"),
		}
		annotateToolsWithMaxResultSize(tools, 500000)
		for i := range tools {
			require.NotNil(t, tools[i].Meta, "tool %d missing Meta", i)
			require.NotNil(t, tools[i].Meta.AdditionalFields)
			assert.Equal(t, 500000,
				tools[i].Meta.AdditionalFields[maxResultSizeCharsMetaKey],
				"tool %d missing annotation", i)
		}
	})

	t.Run("no-op when maxChars is 0", func(t *testing.T) {
		tools := []mcp.Tool{mcp.NewTool("foo")}
		annotateToolsWithMaxResultSize(tools, 0)
		assert.Nil(t, tools[0].Meta, "Meta should remain unset when disabled")
	})

	t.Run("no-op when maxChars is negative", func(t *testing.T) {
		tools := []mcp.Tool{mcp.NewTool("foo")}
		annotateToolsWithMaxResultSize(tools, -1)
		assert.Nil(t, tools[0].Meta)
	})

	t.Run("preserves existing Meta fields", func(t *testing.T) {
		tools := []mcp.Tool{mcp.NewTool("foo")}
		tools[0].Meta = &mcp.Meta{
			AdditionalFields: map[string]any{"custom/key": "value"},
		}
		annotateToolsWithMaxResultSize(tools, 500000)
		require.NotNil(t, tools[0].Meta)
		assert.Equal(t, "value", tools[0].Meta.AdditionalFields["custom/key"])
		assert.Equal(t, 500000, tools[0].Meta.AdditionalFields[maxResultSizeCharsMetaKey])
	})

	t.Run("emits _meta in marshaled tool JSON", func(t *testing.T) {
		tools := []mcp.Tool{mcp.NewTool("foo")}
		annotateToolsWithMaxResultSize(tools, 500000)

		data, err := json.Marshal(tools[0])
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(data, &decoded))

		meta, ok := decoded["_meta"].(map[string]any)
		require.True(t, ok, "tool JSON must contain _meta field")
		// JSON numbers decode to float64.
		assert.Equal(t, float64(500000), meta[maxResultSizeCharsMetaKey])
	})

	t.Run("does not mutate the server's internal tool pointer", func(t *testing.T) {
		// Simulate two successive tools/list calls: the hook must be
		// idempotent so repeated runs don't leak Meta state back onto
		// the source tool.
		tools := []mcp.Tool{mcp.NewTool("foo")}
		annotateToolsWithMaxResultSize(tools, 500000)
		meta1 := tools[0].Meta

		// Second pass on a fresh copy of the same underlying tool.
		tools2 := []mcp.Tool{mcp.NewTool("foo")}
		annotateToolsWithMaxResultSize(tools2, 500000)

		// Each invocation must produce its own Meta instance.
		assert.NotSame(t, meta1, tools2[0].Meta,
			"helper must not reuse Meta pointers across invocations")
	})
}

// TestAnnotateToolsWithMaxResultSize_DefaultConstant asserts the default
// ceiling matches Claude Code's documented maximum (500k chars).
func TestAnnotateToolsWithMaxResultSize_DefaultConstant(t *testing.T) {
	assert.Equal(t, 500000, defaultMaxResultSizeChars,
		"default must match Claude Code's IU6=500000 ceiling")
	assert.Equal(t, "anthropic/maxResultSizeChars", maxResultSizeCharsMetaKey)
}

// TestRegisterMaxResultSizeHook_WiresIntoOnAfterListTools verifies the
// hook registration helper installs a functional OnAfterListTools callback
// that mutates the tools/list result.
func TestRegisterMaxResultSizeHook_WiresIntoOnAfterListTools(t *testing.T) {
	t.Run("installs a hook and annotates tools", func(t *testing.T) {
		hooks := &mcpserver.Hooks{}
		registerMaxResultSizeHook(hooks, 500000)

		require.Len(t, hooks.OnAfterListTools, 1,
			"exactly one AfterListTools hook should be installed")

		result := &mcp.ListToolsResult{
			Tools: []mcp.Tool{
				mcp.NewTool("alpha"),
				mcp.NewTool("beta"),
			},
		}
		hooks.OnAfterListTools[0](context.Background(), nil, &mcp.ListToolsRequest{}, result)

		for i, tool := range result.Tools {
			require.NotNil(t, tool.Meta, "tool %d missing Meta after hook", i)
			assert.Equal(t, 500000,
				tool.Meta.AdditionalFields[maxResultSizeCharsMetaKey])
		}
	})

	t.Run("no-op registration when maxChars is 0", func(t *testing.T) {
		hooks := &mcpserver.Hooks{}
		registerMaxResultSizeHook(hooks, 0)
		assert.Empty(t, hooks.OnAfterListTools,
			"no hook should be registered when annotation is disabled")
	})

	t.Run("tolerates nil result", func(t *testing.T) {
		hooks := &mcpserver.Hooks{}
		registerMaxResultSizeHook(hooks, 500000)
		require.NotPanics(t, func() {
			hooks.OnAfterListTools[0](context.Background(), nil, &mcp.ListToolsRequest{}, nil)
		})
	})
}
