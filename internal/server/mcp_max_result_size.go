package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// maxResultSizeCharsMetaKey is the tool `_meta` key that Claude Code reads
// to decide whether a tool's response should be inlined or persisted to disk.
// Without this annotation Claude Code caps inline tool results at ~50,000
// chars (hardcoded `Vb_=50000` in the product binary) and spills anything
// larger onto disk as a 2KB preview — which forces the agent to waste 3-5
// extra calls recovering the content. Tools that declare this annotation
// raise the ceiling up to the declared value (Claude Code's own hard max
// is 500,000 chars, i.e. `IU6=500000`).
//
// See the luutuankiet/mcp-proxy-shim README ("Response Size Annotation")
// for the background write-up that surfaced this behavior.
const maxResultSizeCharsMetaKey = "anthropic/maxResultSizeChars"

// defaultMaxResultSizeChars is the default ceiling we advertise on every
// tool exposed by mcpproxy. Matches Claude Code's documented maximum so
// large upstream responses (e.g. multi-file reads, command output, query
// results) can flow through inline. Override via config
// `max_result_size_chars`; set to 0 to disable the annotation entirely.
const defaultMaxResultSizeChars = 500000

// annotateToolsWithMaxResultSize injects the maxResultSizeChars annotation
// into every tool's `_meta` field in place. It is a no-op when maxChars is
// zero or negative so operators can fully opt out via config.
//
// Existing `_meta` entries are preserved by building a fresh *mcp.Meta per
// tool (and never sharing pointers across calls), which keeps the hook
// idempotent and safe to run on every tools/list response.
func annotateToolsWithMaxResultSize(tools []mcp.Tool, maxChars int) {
	if maxChars <= 0 {
		return
	}
	for i := range tools {
		existing := tools[i].Meta
		fields := make(map[string]any, len(existingFields(existing))+1)
		for k, v := range existingFields(existing) {
			fields[k] = v
		}
		fields[maxResultSizeCharsMetaKey] = maxChars

		newMeta := &mcp.Meta{AdditionalFields: fields}
		if existing != nil {
			newMeta.ProgressToken = existing.ProgressToken
		}
		tools[i].Meta = newMeta
	}
}

func existingFields(m *mcp.Meta) map[string]any {
	if m == nil {
		return nil
	}
	return m.AdditionalFields
}

// registerMaxResultSizeHook installs an OnAfterListTools hook that annotates
// every tool in a tools/list response with the maxResultSizeChars _meta
// field. No-op when maxChars <= 0.
func registerMaxResultSizeHook(hooks *mcpserver.Hooks, maxChars int) {
	if hooks == nil || maxChars <= 0 {
		return
	}
	hooks.AddAfterListTools(func(_ context.Context, _ any, _ *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		if result == nil {
			return
		}
		annotateToolsWithMaxResultSize(result.Tools, maxChars)
	})
}
