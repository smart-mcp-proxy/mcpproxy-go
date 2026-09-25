package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109 FR-013 (PR 109-e, T070): the server-card stats line and the macOS
// Servers rows need per-server calls/errors/last-call-time from ONE
// `GET /activity/summary` response per page load, rather than a per-server
// query each. `PerServer` is additive and computed in the SAME counting pass
// as the existing totals, from the same storage.CountsAsCall/
// IsManagementBuiltin definitions TopServers already uses — reusing the
// activity_call_parity_test.go fixture keeps the two aggregates pinned
// against one record set instead of a second, possibly-drifting one.
func TestActivitySummaryPerServer(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	srv := newCallParityServer(t, ts)

	summary := getSummary(t, srv)

	require.NotEmpty(t, summary.PerServer, "every server with a call in the period must appear")

	byName := make(map[string]struct {
		calls  int
		errors int
	}, len(summary.PerServer))
	for _, ps := range summary.PerServer {
		byName[ps.Name] = struct {
			calls  int
			errors int
		}{ps.Calls, ps.Errors}
		// Every listed server had at least one call in the period, so it must
		// carry a resolved last-call time, never the empty string.
		assert.NotEmpty(t, ps.LastCallAt, "%s: PerServer only lists servers with a call", ps.Name)
		parsed, err := time.Parse(time.RFC3339, ps.LastCallAt)
		require.NoError(t, err, "%s: last_call_at must be RFC3339", ps.Name)
		assert.WithinDuration(t, ts, parsed, time.Second, "%s: last_call_at must be the fixture's call time", ps.Name)
	}

	// Hand-counted from parityRecords (see activity_call_parity_test.go):
	//   everything:     3 successful echo + 2 failed doesnotexist = 5 calls, 2 errors
	//   memory:         create_entities success + the code_execution sub-call
	//                   (read_graph) = 2 calls, 0 errors
	//   broken-remote:  1 failed call = 1 call, 1 error
	//   evil:           1 blocked policy decision, counted as a failed call
	assert.Equal(t, 5, byName["everything"].calls, "everything: calls")
	assert.Equal(t, 2, byName["everything"].errors, "everything: errors")
	assert.Equal(t, 2, byName["memory"].calls, "memory: calls")
	assert.Equal(t, 0, byName["memory"].errors, "memory: errors")
	assert.Equal(t, 1, byName["broken-remote"].calls, "broken-remote: calls")
	assert.Equal(t, 1, byName["broken-remote"].errors, "broken-remote: errors")
	assert.Equal(t, 1, byName["evil"].calls, "evil: calls (a policy block is a call the user made and did not get)")
	assert.Equal(t, 1, byName["evil"].errors, "evil: errors")

	// The three discovery/management built-ins (retrieve_tools x2,
	// describe_tool) carry no ServerName and must never synthesize a
	// pseudo-server entry.
	for _, ps := range summary.PerServer {
		assert.NotEmpty(t, ps.Name, "PerServer must never carry an empty server name")
	}

	// Deterministic ordering (by name) so two callers of the same response
	// never see a different row order.
	names := make([]string, len(summary.PerServer))
	for i, ps := range summary.PerServer {
		names[i] = ps.Name
	}
	assert.IsIncreasing(t, names, "PerServer is sorted by name for a stable response")
}

// A server whose only activity in the period is a non-call event (a security
// scan, a quarantine auto-approval) must not appear in PerServer at all —
// PerServer answers "which servers had a CALL", not "which servers have any
// row in the log" (that broader question is TopServers/serverCounts).
func TestActivitySummaryPerServerExcludesNonCallOnlyServers(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	srv := newCallParityServer(t, ts)

	summary := getSummary(t, srv)

	// "everything" also has a security-scan-only-looking row in the fixture,
	// but it has real calls too, so this checks the general invariant instead:
	// no PerServer entry may report zero calls.
	for _, ps := range summary.PerServer {
		assert.Greater(t, ps.Calls, 0, "%s: PerServer must not list a server with zero calls", ps.Name)
	}
}
