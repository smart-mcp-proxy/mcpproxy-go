package server

import (
	"encoding/json"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolsig"
)

// compactModeHint is the single deterministic hint line attached (top-level
// "hint") to every compact-mode retrieve_tools response (Spec 085 FR-009).
// It explains the lossy marker and the describe_tool second stage; being part
// of the serialized payload it counts toward every measured response size.
const compactModeHint = "sig legend: name*:type = required param, ~ = collapsed/lossy (details omitted), (~) = schema unavailable. Before calling a tool whose entry is lossy:true, call describe_tool with its id to get the full input schema."

// toolEntryOpts carries the per-request options that shape one retrieve_tools
// result entry (Spec 085 entry-builder seam, research.md R5).
type toolEntryOpts struct {
	// includeStats appends usage_count/last_used to full entries when the
	// caller passed include_stats:true.
	includeStats bool
}

// buildToolEntry renders ONE search result as a response entry for the given
// tool_response_mode (Spec 085). It is the full/compact serialization seam:
//
//   - mode full (or anything else): the exact per-entry map the handler
//     built inline before the extraction — byte-identical under
//     json.Marshal, guarded by the T011 golden test (FR-006/SC-003).
//   - mode compact (US1, FR-002): {id, score, sig, desc, lossy} rendered
//     from the shared p.sigCache — no inputSchema, no full description, no
//     annotations (those move to describe_tool / self-healing errors).
//
// Cross-cutting response sections (usage_instructions, disabled/remediation,
// notice, session_risk, debug, usage_summary) intentionally stay in
// handleRetrieveToolsWithMode — this builds entries only.
func (p *MCPProxyServer) buildToolEntry(result *config.SearchResult, mode string, opts toolEntryOpts) map[string]interface{} {
	if mode == config.ToolResponseModeCompact {
		return p.buildCompactToolEntry(result)
	}
	return p.buildFullToolEntry(result, opts)
}

// buildCompactToolEntry renders one search result as a compact entry (Spec
// 085 FR-002/003/004, grammar: specs/085-compact-router/contracts/
// signature-grammar.md). The id equals the full-mode "name" (result.Tool.Name,
// canonicalized to "server:tool" at the index read seam — #871) so ranked
// identity between modes holds by construction (FR-007), and the id round-trips
// straight back into describe_tool / call_tool_* — entries are never re-sorted
// here.
//
// include_stats is intentionally ignored in compact mode: the compact entry
// shape is fixed at exactly {id, score, sig, desc, lossy} (data-model §4);
// the top-level usage_summary section still renders in the handler.
func (p *MCPProxyServer) buildCompactToolEntry(result *config.SearchResult) map[string]interface{} {
	tool := result.Tool

	var sig toolsig.Signature
	if tool.Hash != "" {
		// FR-008: the shared Runtime-owned cache, keyed by the Spec-032 tool
		// hash and warmed at index time — a pure read on this path.
		sig = p.sigCache.Get(tool.Hash, tool.ParamsJSON, tool.Description)
	} else {
		// Defensive: indexed tools always carry a hash, but a hashless tool
		// must not memoize under a shared "" key (distinct schemas would
		// collide on first-write-wins). Render directly instead; Render is
		// fail-soft, so the error is log-only (mirrors the tolerant
		// inputSchema fallback in the full path).
		var err error
		sig, err = toolsig.Render(tool.ParamsJSON, tool.Description)
		if err != nil {
			p.logger.Debug("compact signature rendered via unparseable-schema fallback",
				zap.String("tool", tool.Name),
				zap.Error(err))
		}
	}

	return map[string]interface{}{
		"id":    tool.Name,
		"score": result.Score,
		"sig":   sig.Sig,
		"desc":  sig.Desc,
		"lossy": sig.Lossy,
	}
}

// buildFullToolEntry reproduces today's full-mode entry exactly
// (extracted verbatim from handleRetrieveToolsWithMode).
func (p *MCPProxyServer) buildFullToolEntry(result *config.SearchResult, opts toolEntryOpts) map[string]interface{} {
	// Parse the input schema from ParamsJSON
	var inputSchema map[string]interface{}
	if result.Tool.ParamsJSON != "" {
		if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err != nil {
			p.logger.Warn("Failed to parse tool params JSON",
				zap.String("tool_name", result.Tool.Name),
				zap.Error(err))
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
	} else {
		inputSchema = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	// Create MCP-compatible tool representation
	mcpTool := map[string]interface{}{
		"name":        result.Tool.Name,
		"description": result.Tool.Description,
		"inputSchema": inputSchema,
		"score":       result.Score,
		"server":      result.Tool.ServerName,
	}

	// Look up tool annotations and derive recommended call_with variant (Spec 018).
	// result.Tool.Name is the canonical "server:tool" id (#871); lookupToolAnnotations
	// normalizes the prefix against the bare StateView names, so the prefixed toolName
	// passes through cleanly (Issue #306 guard). ServerName is authoritative when set.
	serverName := result.Tool.ServerName
	toolName := result.Tool.Name
	if serverName == "" {
		// Fallback: try to extract from "server:tool" format
		if parts := strings.SplitN(result.Tool.Name, ":", 2); len(parts) == 2 {
			serverName = parts[0]
			toolName = parts[1]
		}
	}

	if serverName != "" {
		annotations := p.lookupToolAnnotations(serverName, toolName)
		if annotations != nil {
			mcpTool["annotations"] = annotations
		}
		// Add call_with recommendation based on annotations
		mcpTool["call_with"] = contracts.DeriveCallWith(annotations)
	} else {
		mcpTool["call_with"] = contracts.ToolVariantRead // Default to read - safest option
	}

	// Add usage statistics if requested
	if opts.includeStats {
		if stats, err := p.storage.GetToolUsage(result.Tool.Name); err == nil {
			mcpTool["usage_count"] = stats.Count
			mcpTool["last_used"] = stats.LastUsed
		}
	}

	return mcpTool
}
