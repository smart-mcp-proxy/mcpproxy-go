package httpapi

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// This file holds the two caller-aware predicates every REST read door reads
// from, plus the payload shapers built on top of them. Issues #1166 and #1167
// were the same defect twice: five-plus doors each re-derived the answer to
// "what may this caller see?" from a config flag alone, so the MCP surface and
// the REST surface drifted apart and a scoped, read-only agent token received
// both the full server inventory and every server's plaintext credentials.
//
// Rule for anyone adding a read route under /api/v1: do not read
// cfg.RevealSecretHeaders and do not call CanAccessServer directly. Call
// s.revealSecrets(ctx) for values and canSeeServer / visibleServers for
// existence, so the two surfaces answer from one place.

// canSeeServer reports whether the caller carried by ctx may learn that a
// server named `name` exists. Thin wrapper over the shared predicate so
// handlers in this package never reach for the AuthContext themselves.
func canSeeServer(ctx context.Context, name string) bool {
	return auth.CanEnumerateServer(ctx, name)
}

// visibleServers returns the subset of servers the caller carried by ctx is
// entitled to enumerate.
//
// It ALWAYS returns a freshly allocated slice for a scoped caller and never
// compacts in place: the backing array reaching this function is shared with
// the management service (and, on the SSE path, with every other subscriber),
// so an in-place compaction would corrupt an admin's view of the same data.
// The allocation is `make(..., 0, n)` so an empty result marshals as `[]` and
// not `null` — the Swift tray decodes contracts.GetServersResponse strictly.
//
// An admin, or a request with no AuthContext at all, gets the input slice back
// untouched (see auth.CanEnumerateServer for why absence is unrestricted).
func visibleServers(ctx context.Context, servers []contracts.Server) []contracts.Server {
	if !auth.IsScopedCaller(ctx) {
		return servers
	}
	out := make([]contracts.Server, 0, len(servers))
	for i := range servers {
		if canSeeServer(ctx, servers[i].Name) {
			out = append(out, servers[i])
		}
	}
	return out
}

// upstreamStatsScalars are the aggregate counters that sit BESIDE the
// per-server map in an `upstream_stats` payload. They are computed over the
// unfiltered inventory by both producers (internal/server.Server.GetUpstreamStats
// and upstream.Manager.GetStats), so filtering only the "servers" sub-map would
// leave `total_servers` as an exact oracle for how many servers were hidden.
var upstreamStatsScalars = []string{
	"total_servers",
	"connected_servers",
	"connecting_servers",
	"quarantined_servers",
	"total_tools",
}

// filterUpstreamStatsServers narrows an `upstream_stats` map to the servers the
// caller may enumerate, recomputing the sibling scalar counters from the
// surviving entries.
//
// The returned map is a fresh shallow copy for a scoped caller. That is
// load-bearing rather than tidy: this same map is handed to the SSE writer,
// which runs one goroutine per connection over payloads the event bus and the
// controller share, and a mutation here would be an unsynchronised map write
// concurrent with another goroutine's json.Marshal — a fatal, unrecoverable
// runtime error that takes the whole core down for every client.
func filterUpstreamStatsServers(ctx context.Context, stats map[string]interface{}) map[string]interface{} {
	if stats == nil || !auth.IsScopedCaller(ctx) {
		return stats
	}
	servers, ok := stats["servers"].(map[string]interface{})
	if !ok {
		return stats
	}

	filtered := make(map[string]interface{}, len(servers))
	connected, connecting, quarantined, totalTools := 0, 0, 0, 0
	for name, raw := range servers {
		if !canSeeServer(ctx, name) {
			continue
		}
		filtered[name] = raw
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := entry["connected"].(bool); ok && v {
			connected++
		}
		if v, ok := entry["connecting"].(bool); ok && v {
			connecting++
		}
		if v, ok := entry["quarantined"].(bool); ok && v {
			quarantined++
		}
		totalTools += genericToolCount(entry["tool_count"])
	}

	out := make(map[string]interface{}, len(stats))
	for k, v := range stats {
		out[k] = v
	}
	out["servers"] = filtered
	// Only rewrite a scalar the producer actually emitted, so this cannot
	// invent a key a client would then treat as authoritative.
	recomputed := map[string]int{
		"total_servers":       len(filtered),
		"connected_servers":   connected,
		"connecting_servers":  connecting,
		"quarantined_servers": quarantined,
		"total_tools":         totalTools,
	}
	for _, key := range upstreamStatsScalars {
		if _, present := stats[key]; present {
			out[key] = recomputed[key]
		}
	}
	return out
}

// genericToolCount coerces a tool_count leaf into an int. The map reaches this
// function both straight from Go (int) and after a JSON round-trip (float64),
// so both encodings must be accepted — the same reason
// contracts.genericInt exists.
func genericToolCount(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// recomputeServerStats rebuilds the aggregate counters of a GetServersResponse
// over the filtered slice. Without it the array shrinks while
// stats.total_servers keeps reporting the true inventory size — a precise
// count oracle for exactly what the filter just hid.
//
// DockerContainers and TokenMetrics are deployment-wide aggregates that cannot
// be re-derived per server; they are dropped for a scoped caller rather than
// reported wrongly.
func recomputeServerStats(ctx context.Context, filtered []contracts.Server, full contracts.ServerStats) contracts.ServerStats {
	if !auth.IsScopedCaller(ctx) {
		return full
	}
	out := contracts.ServerStats{TotalServers: len(filtered)}
	for i := range filtered {
		if filtered[i].Connected {
			out.ConnectedServers++
		}
		if filtered[i].Quarantined {
			out.QuarantinedServers++
		}
		if config.ServerContributesTools(filtered[i].Enabled, filtered[i].Quarantined) {
			out.TotalTools += filtered[i].ToolCount
		}
	}
	return out
}

// revealSecrets is the ONE place this package decides whether a response may
// carry raw credential values (#1167).
//
// It ANDs the operator's `reveal_secret_headers` opt-in with the caller's
// identity, exactly as the MCP door has always done. Before this, four
// handlers in this package read the flag alone with the *http.Request sitting
// right there in scope, so an mcp_agt_ token got the operator's answer.
//
// Fails closed when the config cannot be read.
func (s *Server) revealSecrets(ctx context.Context) bool {
	cfg, err := s.controller.GetConfig()
	if err != nil || cfg == nil {
		return false
	}
	return auth.RevealSecretsAllowed(ctx, cfg.RevealSecretHeaders)
}
