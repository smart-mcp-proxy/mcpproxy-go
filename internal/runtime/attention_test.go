package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

func attnServer(name string, mut func(*AttentionServer)) AttentionServer {
	s := AttentionServer{
		Name:    name,
		Enabled: true,
		Health: contracts.HealthStatus{
			Status: health.StatusReady,
			Usable: true,
		},
		StateSince: time.Now(),
	}
	if mut != nil {
		mut(&s)
	}
	return s
}

// TestAttentionComputeOrderedItems exercises Compute across every kind at
// once and asserts exact ordering (rank ascending, then subject.name).
func TestAttentionComputeOrderedItems(t *testing.T) {
	now := time.Date(2026, 9, 25, 6, 12, 0, 0, time.UTC)
	stale := now.Add(-90 * time.Second)

	in := AttentionInput{
		Now: now,
		Servers: []AttentionServer{
			attnServer("github", func(s *AttentionServer) {
				s.Quarantined = true
				s.Health.Status = health.StatusSignInRequired
				s.Health.Summary = "Sign in required"
				s.Detail = "OAuth · api.githubcopilot.com"
				s.StateSince = stale
			}),
			attnServer("weather", func(s *AttentionServer) {
				s.Health.Status = health.StatusNeedsSecret
				s.StateSince = stale
			}),
			attnServer("broken-config", func(s *AttentionServer) {
				s.Health.Status = health.StatusNeedsConfig
				s.Health.Actions = []string{health.ActionConfigure}
				s.StateSince = stale
			}),
			attnServer("flaky", func(s *AttentionServer) {
				s.Health.Status = health.StatusError
				s.StateSince = stale
			}),
			attnServer("trusted-changed", func(s *AttentionServer) {
				s.Changed = 1
				s.StateSince = stale
			}),
			attnServer("trusted-pending", func(s *AttentionServer) {
				s.Pending = 2
				s.StateSince = stale
			}),
			attnServer("disabled", func(s *AttentionServer) {
				s.Enabled = false
				s.Health.Status = health.StatusError
				s.StateSince = stale
			}),
			attnServer("healthy-login-nudge", func(s *AttentionServer) {
				// ready server with a proactive login nudge is never an item.
				s.Health.Status = health.StatusReady
				s.Health.Actions = []string{health.ActionLogin}
			}),
		},
		Clients: []AttentionClient{
			{
				ID:          "codex",
				DisplayName: "Codex CLI",
				ConnectedAt: timePtr(now.Add(-10 * time.Minute)),
			},
		},
	}

	items := Compute(in)

	kinds := make([]string, len(items))
	for i, it := range items {
		kinds[i] = it.Kind + ":" + it.Subject.ID
	}

	require.Equal(t, []string{
		"sign_in_required:github",
		"missing_secret:weather",
		"config_error:broken-config",
		"server_error:flaky",
		"server_review:github",
		"tool_review:trusted-changed",
		"tool_review:trusted-pending",
		"client_never_seen:codex",
	}, kinds)

	// Ranks ascending.
	for i := 1; i < len(items); i++ {
		assert.LessOrEqual(t, items[i-1].Rank, items[i].Rank, "items must be rank-ordered")
	}

	// Stable ids.
	assert.Equal(t, "sign_in_required:server:github", items[0].ID)
	assert.Equal(t, "server_review:server:github", items[4].ID)
}

func TestAttentionDisabledServerNeverAnItem(t *testing.T) {
	in := AttentionInput{
		Now: time.Now(),
		Servers: []AttentionServer{
			attnServer("off", func(s *AttentionServer) {
				s.Enabled = false
				s.Health.Status = health.StatusError
				s.StateSince = time.Now().Add(-time.Hour)
			}),
		},
	}
	assert.Empty(t, Compute(in))
}

func TestAttentionConnectingThreshold(t *testing.T) {
	now := time.Now()

	under := AttentionInput{Now: now, Servers: []AttentionServer{
		attnServer("s", func(s *AttentionServer) {
			s.Health.Status = health.StatusConnecting
			s.StateSince = now.Add(-30 * time.Second)
		}),
	}}
	assert.Empty(t, Compute(under), "connecting < 60s must not be an item")

	over := AttentionInput{Now: now, Servers: []AttentionServer{
		attnServer("s", func(s *AttentionServer) {
			s.Health.Status = health.StatusConnecting
			s.StateSince = now.Add(-61 * time.Second)
		}),
	}}
	items := Compute(over)
	require.Len(t, items, 1)
	assert.Equal(t, AttentionKindServerError, items[0].Kind)
}

func TestAttentionReadyServerWithLoginNudgeIsNotAnItem(t *testing.T) {
	in := AttentionInput{Now: time.Now(), Servers: []AttentionServer{
		attnServer("s", func(s *AttentionServer) {
			s.Health.Status = health.StatusReady
			s.Health.Actions = []string{health.ActionLogin}
		}),
	}}
	assert.Empty(t, Compute(in))
}

func TestAttentionQuarantinedTransportFaultYieldsOnlyServerReview(t *testing.T) {
	now := time.Now()
	in := AttentionInput{Now: now, Servers: []AttentionServer{
		attnServer("q", func(s *AttentionServer) {
			s.Quarantined = true
			s.Health.Status = health.StatusError
			s.StateSince = now.Add(-90 * time.Second)
		}),
	}}
	items := Compute(in)
	require.Len(t, items, 1)
	assert.Equal(t, AttentionKindServerReview, items[0].Kind)
}

func TestAttentionToolReviewOneItemPerNonZeroState(t *testing.T) {
	in := AttentionInput{Now: time.Now(), Servers: []AttentionServer{
		attnServer("both", func(s *AttentionServer) {
			s.Pending = 1
			s.Changed = 2
		}),
	}}
	items := Compute(in)
	require.Len(t, items, 2)
	assert.Equal(t, AttentionKindToolReview, items[0].Kind)
	assert.Equal(t, AttentionRankToolReviewChanged, items[0].Rank)
	assert.Equal(t, AttentionKindToolReview, items[1].Kind)
	assert.Equal(t, AttentionRankToolReviewPending, items[1].Rank)
	assert.NotEqual(t, items[0].ID, items[1].ID, "stable ids must differ per state")
}

func TestAttentionClientNeverSeenThreshold(t *testing.T) {
	now := time.Now()

	tooSoon := AttentionInput{Now: now, Clients: []AttentionClient{
		{ID: "c", DisplayName: "C", ConnectedAt: timePtr(now.Add(-4 * time.Minute))},
	}}
	assert.Empty(t, Compute(tooSoon))

	overdue := AttentionInput{Now: now, Clients: []AttentionClient{
		{ID: "c", DisplayName: "C", ConnectedAt: timePtr(now.Add(-6 * time.Minute))},
	}}
	items := Compute(overdue)
	require.Len(t, items, 1)
	assert.Equal(t, AttentionKindClientNeverSeen, items[0].Kind)

	seen := AttentionInput{Now: now, Clients: []AttentionClient{
		{
			ID:          "c",
			DisplayName: "C",
			ConnectedAt: timePtr(now.Add(-6 * time.Minute)),
			LastSeen:    timePtr(now.Add(-1 * time.Minute)),
		},
	}}
	assert.Empty(t, Compute(seen), "a session since the connect write clears the item")
}

func timePtr(t time.Time) *time.Time { return &t }
