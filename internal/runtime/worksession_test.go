package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedClock(start time.Time) (*WorkSessionTracker, func(time.Duration)) {
	t := NewWorkSessionTracker(30 * time.Minute)
	now := start
	t.now = func() time.Time { return now }
	return t, func(d time.Duration) { now = now.Add(d) }
}

// US3 / SC-002: the whole point. A client that reconnects mid-work gets a brand
// new transport session id every time; the work session must not change.
func TestWorkSession_SurvivesReconnect(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	id := WorkSessionIdentity{
		Principal:     "agent-1",
		ClientName:    "claude-code",
		ClientVersion: "1.0.60",
		WorkspaceRoot: "/Users/me/repos/mcpproxy-go",
	}

	first := tr.Resolve(id)
	require.NotEmpty(t, first)

	// Client silently reconnects several times over 20 minutes. The identity is
	// unchanged — that is what makes it the same work.
	advance(10 * time.Minute)
	second := tr.Resolve(id)
	advance(10 * time.Minute)
	third := tr.Resolve(id)

	assert.Equal(t, first, second, "a reconnect within the idle window must not start a new work session")
	assert.Equal(t, first, third)
}

// US3 / acceptance 2: a real break in work starts a new session.
func TestWorkSession_IdleWindowSplitsWork(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/a"}

	morning := tr.Resolve(id)

	// Lunch.
	advance(31 * time.Minute)
	afternoon := tr.Resolve(id)

	assert.NotEqual(t, morning, afternoon,
		"activity separated by more than the idle window is new work")
}

// The boundary itself: exactly at the window is still the same work.
func TestWorkSession_IdleWindowBoundaryIsInclusive(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/a"}

	first := tr.Resolve(id)
	advance(30 * time.Minute) // exactly the window
	assert.Equal(t, first, tr.Resolve(id))

	advance(30*time.Minute + time.Nanosecond) // just past it
	assert.NotEqual(t, first, tr.Resolve(id))
}

// US2 / SC-003: two projects, two sessions — even for the same client.
func TestWorkSession_SeparatesProjects(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	a := tr.Resolve(WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/alpha"})
	b := tr.Resolve(WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/beta"})

	assert.NotEqual(t, a, b, "work in different projects must not share a session")
}

// FR-013: two clients in the SAME project at the same time are different work.
func TestWorkSession_SeparatesClientsInSameProject(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	claude := tr.Resolve(WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/alpha"})
	cursor := tr.Resolve(WorkSessionIdentity{ClientName: "cursor", WorkspaceRoot: "/repos/alpha"})

	assert.NotEqual(t, claude, cursor)
}

// FR-010 / SC-006: Codex discloses no project (measured). It must still group.
func TestWorkSession_ClientWithNoWorkspaceStillGroups(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	codex := WorkSessionIdentity{ClientName: "codex", ClientVersion: "1.0"}

	first := tr.Resolve(codex)
	require.NotEmpty(t, first, "a client with no workspace must still get a work session")

	advance(5 * time.Minute)
	assert.Equal(t, first, tr.Resolve(codex), "and it must survive its reconnects too")
}

// Refuse to invent grouping we have no basis for. An entirely anonymous caller
// gets no work session rather than being lumped in with every other one.
func TestWorkSession_UnknownCallerIsNotGrouped(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	assert.Empty(t, tr.Resolve(WorkSessionIdentity{}))
}

// FR-011: a client-supplied conversation id overrides the heuristic entirely —
// two conversations in the same project stop collapsing (Assumption 3's escape
// hatch). No client sends one today; this proves the seam works when one does.
func TestWorkSession_CorrelationIDOverridesHeuristic(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	base := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/alpha"}
	convA := base
	convA.CorrelationID = "conv-a"
	convB := base
	convB.CorrelationID = "conv-b"

	a := tr.Resolve(convA)
	b := tr.Resolve(convB)

	assert.NotEqual(t, a, b,
		"two conversations in the same project must separate when the client identifies them")
	assert.Equal(t, a, tr.Resolve(convA), "and the same conversation must be stable")
}

// The known, accepted limitation (Assumption 3) — asserted so it is a decision,
// not an accident. Without a correlation id, two concurrent conversations from
// the same client in the same project are genuinely indistinguishable to us.
func TestWorkSession_ConcurrentConversationsCollapse_KnownLimitation(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/alpha"}

	assert.Equal(t, tr.Resolve(id), tr.Resolve(id),
		"documented limitation: without a client conversation id these collapse into one session")
}

func TestWorkSession_Reap(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	tr.Resolve(WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/a"})
	tr.Resolve(WorkSessionIdentity{ClientName: "cursor", WorkspaceRoot: "/repos/b"})

	advance(2 * time.Hour)
	assert.Equal(t, 2, tr.Reap(time.Hour), "idle work sessions must not accumulate forever")
	assert.Equal(t, 0, tr.Reap(time.Hour))
}

func TestWorkspaceName(t *testing.T) {
	cases := map[string]string{
		"/Users/me/repos/mcpproxy-go":         "mcpproxy-go",
		"file:///Users/me/repos/mcpproxy-go":  "mcpproxy-go",
		"file:///Users/me/repos/mcpproxy-go/": "mcpproxy-go",
		"":                                    "",
		"   ":                                 "",
	}
	for in, want := range cases {
		assert.Equal(t, want, WorkspaceName(in), "WorkspaceName(%q)", in)
	}
}

// Privacy (FR-020 / SC-007): the id must never carry the path or the principal,
// because it travels in URLs, logs and CSV exports.
func TestWorkSessionID_LeaksNeitherPathNorPrincipal(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	id := tr.Resolve(WorkSessionIdentity{
		Principal:     "mcp_agt_supersecret",
		ClientName:    "claude-code",
		WorkspaceRoot: "/Users/alice/private/skunkworks",
	})

	require.NotEmpty(t, id)
	assert.NotContains(t, id, "alice")
	assert.NotContains(t, id, "skunkworks")
	assert.NotContains(t, id, "supersecret")
	assert.NotContains(t, id, "/")
}

func TestWorkSessionTracker_ConcurrentResolveIsStable(t *testing.T) {
	tr := NewWorkSessionTracker(30 * time.Minute)
	id := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/a"}

	const n = 50
	out := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() { out <- tr.Resolve(id) }()
	}

	first := <-out
	for i := 1; i < n; i++ {
		assert.Equal(t, first, <-out, "concurrent resolves of one identity must agree")
	}
}
