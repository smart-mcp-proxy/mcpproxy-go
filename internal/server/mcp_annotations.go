package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// SessionRisk holds the result of analyzing all connected servers' tool annotations
// for the "lethal trifecta" risk combination (Spec 035 F2).
type SessionRisk struct {
	Level          string `json:"level"`           // "high", "medium", "low"
	HasOpenWorld   bool   `json:"has_open_world"`  // Any tool with openWorldHint=true or nil
	HasDestructive bool   `json:"has_destructive"` // Any tool with destructiveHint=true or nil
	HasWrite       bool   `json:"has_write"`       // Any tool with readOnlyHint=false or nil
	LethalTrifecta bool   `json:"lethal_trifecta"` // All three categories present
	Warning        string `json:"warning,omitempty"`
}

// analyzeSessionRisk examines all connected servers' tool annotations to detect
// the "lethal trifecta" risk: open-world access + destructive capabilities + write access.
// Per MCP spec, nil annotation hints default to the most permissive interpretation:
//   - openWorldHint nil → true (assumes open world)
//   - destructiveHint nil → true (assumes destructive)
//   - readOnlyHint nil → false (assumes not read-only, i.e., can write)
func analyzeSessionRisk(snapshot *stateview.ServerStatusSnapshot) SessionRisk {
	var hasOpenWorld, hasDestructive, hasWrite bool

	for _, server := range snapshot.Servers {
		if !server.Connected {
			continue
		}

		for _, tool := range server.Tools {
			classifyToolRisk(tool.Annotations, &hasOpenWorld, &hasDestructive, &hasWrite)
		}
	}

	// Count how many risk categories are present
	riskCount := 0
	if hasOpenWorld {
		riskCount++
	}
	if hasDestructive {
		riskCount++
	}
	if hasWrite {
		riskCount++
	}

	risk := SessionRisk{
		HasOpenWorld:   hasOpenWorld,
		HasDestructive: hasDestructive,
		HasWrite:       hasWrite,
	}

	switch {
	case riskCount >= 3:
		risk.Level = "high"
		risk.LethalTrifecta = true
		risk.Warning = "LETHAL TRIFECTA DETECTED: This session combines open-world access, " +
			"destructive capabilities, and write access across connected servers. " +
			"A prompt injection attack could chain these to cause significant damage. " +
			"Consider using annotation filters (read_only_only, exclude_destructive, exclude_open_world) " +
			"to restrict tool discovery."
	case riskCount == 2:
		risk.Level = "medium"
	default:
		risk.Level = "low"
	}

	return risk
}

// buildSessionRiskResponse converts a SessionRisk into the map shape returned
// in the `session_risk` field of `retrieve_tools` responses. The structured
// fields are always populated; the prose `warning` is included only when
// includeWarning is true. See issue #406 — most tools lack annotations and
// trigger the trifecta by default, so the prose warning is opt-in.
func buildSessionRiskResponse(risk SessionRisk, includeWarning bool) map[string]interface{} {
	out := map[string]interface{}{
		"level":                 risk.Level,
		"has_open_world_tools":  risk.HasOpenWorld,
		"has_destructive_tools": risk.HasDestructive,
		"has_write_tools":       risk.HasWrite,
		"lethal_trifecta":       risk.LethalTrifecta,
	}
	if includeWarning && risk.Warning != "" {
		out["warning"] = risk.Warning
	}
	return out
}

// classifyToolRisk updates the risk flags based on a single tool's annotations.
// Nil hints are treated as their MCP spec defaults (most permissive).
func classifyToolRisk(annotations *config.ToolAnnotations, hasOpenWorld, hasDestructive, hasWrite *bool) {
	if annotations == nil {
		// No annotations at all — apply MCP spec defaults (all permissive)
		*hasOpenWorld = true
		*hasDestructive = true
		*hasWrite = true
		return
	}

	// openWorldHint: nil or true → open world
	if annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
		*hasOpenWorld = true
	}

	// destructiveHint: nil or true → destructive
	if annotations.DestructiveHint == nil || *annotations.DestructiveHint {
		*hasDestructive = true
	}

	// readOnlyHint: nil or false → not read-only (write capable)
	if annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint {
		*hasWrite = true
	}
}

// annotatedSearchResult pairs a search result with its resolved annotations
// for use in annotation-based filtering (Spec 035 F4).
type annotatedSearchResult struct {
	serverName  string
	toolName    string
	annotations *config.ToolAnnotations
	resultIndex int // Index into the original search results slice
}

// filterByAnnotations filters annotated search results based on annotation criteria.
// Returns only the results that pass all active filters.
//
// Filter semantics (per MCP spec, nil hints default to most permissive):
//   - readOnlyOnly: keep only tools with readOnlyHint=true (explicit)
//   - excludeDestructive: exclude tools with destructiveHint=true or nil
//   - excludeOpenWorld: exclude tools with openWorldHint=true or nil
func filterByAnnotations(tools []annotatedSearchResult, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) []annotatedSearchResult {
	// Fast path: no filters active
	if !readOnlyOnly && !excludeDestructive && !excludeOpenWorld {
		return tools
	}

	var filtered []annotatedSearchResult
	for _, tool := range tools {
		if shouldExclude(tool.annotations, readOnlyOnly, excludeDestructive, excludeOpenWorld) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// shouldExclude returns true if a tool should be excluded based on its annotations and active filters.
func shouldExclude(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) bool {
	if readOnlyOnly {
		// Must have explicit readOnlyHint=true to pass
		if annotations == nil || annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint {
			return true
		}
	}

	if excludeDestructive {
		// Exclude if destructiveHint is true or nil (default is true per spec).
		// However, a tool with readOnlyHint=true is inherently non-destructive,
		// so treat destructiveHint as false when readOnlyHint is explicitly true.
		isReadOnly := annotations != nil && annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint
		if !isReadOnly {
			if annotations == nil || annotations.DestructiveHint == nil || *annotations.DestructiveHint {
				return true
			}
		}
	}

	if excludeOpenWorld {
		// Exclude if openWorldHint is true or nil (default is true per spec)
		if annotations == nil || annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
			return true
		}
	}

	return false
}
