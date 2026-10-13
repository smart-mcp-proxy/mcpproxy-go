package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 090 (T016) shared probe.
//
// Policy decisions leave the server through exactly one funnel —
// MCPProxyServer.emitActivityPolicyDecision — and land on the runtime event
// bus. Rather than stubbing that funnel (which would let a call site pass the
// wrong id and still "pass"), these tests wire a REAL Runtime and read the real
// events off its subscription, so what is asserted is what an SSE client would
// actually receive.
//
// The probe lives in its own file because four different test files
// (intent_validation, mcp_routing, output_sanitisation, mcp_output_schema) each
// drive a different family of block sites through it.
type policyDecisionProbe struct {
	mu     sync.Mutex
	events []map[string]any
}

// watchPolicyDecisions subscribes to rt's event bus and collects every
// activity.policy_decision until the test ends.
func watchPolicyDecisions(t *testing.T, rt *runtime.Runtime) *policyDecisionProbe {
	t.Helper()

	probe := &policyDecisionProbe{}
	ch := rt.SubscribeEvents()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range ch {
			if evt.Type != runtime.EventTypeActivityPolicyDecision {
				continue
			}
			probe.mu.Lock()
			probe.events = append(probe.events, evt.Payload)
			probe.mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		rt.UnsubscribeEvents(ch)
		<-done
	})

	return probe
}

// awaitOne waits for exactly one policy decision and returns its payload.
// Publication is synchronous into a buffered channel but the drain is not, so
// the wait is what makes the assertion deterministic.
func (p *policyDecisionProbe) awaitOne(t *testing.T) map[string]any {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		n := len(p.events)
		var first map[string]any
		if n > 0 {
			first = p.events[0]
		}
		p.mu.Unlock()

		if n == 1 {
			return first
		}
		require.LessOrEqual(t, n, 1, "expected a single policy decision, got %d", n)
		if time.Now().After(deadline) {
			t.Fatal("no activity.policy_decision event was emitted within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requestID pulls the correlation id out of a policy-decision payload,
// asserting it is present and non-empty — an empty id is worse than useless to
// the glance, which would collapse every legacy-shaped block into one row.
func requestIDOf(t *testing.T, payload map[string]any) string {
	t.Helper()

	raw, ok := payload["request_id"]
	require.True(t, ok, "policy decision payload must carry request_id, got keys %v", keysOfPayload(payload))
	id, ok := raw.(string)
	require.True(t, ok, "request_id must be a string, got %T", raw)
	require.NotEmpty(t, id, "request_id must not be empty")
	return id
}

func keysOfPayload(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	return keys
}
