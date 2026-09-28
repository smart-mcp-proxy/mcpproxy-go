package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// TestAttentionSubscriberBuildsServerFromServersChangedRow pins the T060
// wiring: the subscriber's builder maps a servers.changed row (health +
// quarantine.pending_count/changed_count) into an AttentionServer, and
// stamps StateSince only when health.status actually changes.
func TestAttentionSubscriberBuildsServerFromServersChangedRow(t *testing.T) {
	rt := &Runtime{eventSubs: make(map[chan Event]struct{}), internalEventSubs: make(map[chan Event]struct{})}
	sub := newAttentionSubscriber(rt, time.Millisecond)

	row := contracts.Server{
		Name:        "github",
		Enabled:     true,
		Quarantined: true,
		URL:         "https://api.githubcopilot.com/mcp",
		OAuth:       &contracts.OAuthConfig{},
		Health: &contracts.HealthStatus{
			Status:  health.StatusSignInRequired,
			Actions: []string{health.ActionLogin},
		},
		Quarantine: &contracts.QuarantineStats{PendingCount: 2, ChangedCount: 1},
	}

	t1 := time.Now()
	built := sub.buildAttentionServer(row, t1)
	assert.Equal(t, "github", built.Name)
	assert.True(t, built.Enabled)
	assert.True(t, built.Quarantined)
	assert.Equal(t, health.StatusSignInRequired, built.Health.Status)
	assert.Equal(t, 2, built.Pending)
	assert.Equal(t, 1, built.Changed)
	assert.Equal(t, t1, built.StateSince, "first sighting stamps StateSince to now")
	assert.Contains(t, built.Detail, "OAuth")
	assert.Contains(t, built.Detail, "api.githubcopilot.com")
	assert.NotContains(t, built.Detail, "/mcp", "detail must never carry the URL path")

	// A second build with the SAME status must not restamp StateSince.
	t2 := t1.Add(5 * time.Second)
	built2 := sub.buildAttentionServer(row, t2)
	assert.Equal(t, t1, built2.StateSince, "unchanged status keeps the original StateSince")

	// A status change restamps StateSince to the new "now".
	row.Health.Status = health.StatusError
	t3 := t2.Add(5 * time.Second)
	built3 := sub.buildAttentionServer(row, t3)
	assert.Equal(t, t3, built3.StateSince, "a health.status change restamps StateSince")
}

// The SC-011 100-servers/1,000-tools Compute benchmark lives in
// attention_bench_test.go (T059: buildAttentionBenchFleet,
// BenchmarkComputeSC011LargeFleet, TestComputeSC011LargeFleetP95) — this
// file's old BenchmarkAttentionComputeLargeFleet was a smaller, unasserted
// duplicate of the same fixture and has been superseded by it.
