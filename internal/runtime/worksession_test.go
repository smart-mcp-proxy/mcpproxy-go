package runtime

import (
	"sync"
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

// FR-006 / cross-model review finding: the principal MUST be part of the key.
// Two different agent tokens driving the same client in the same project are
// different work, and must not be merged into one session.
func TestWorkSession_SeparatesPrincipals(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	base := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/alpha"}
	a := base
	a.Principal = "agent:ci-bot"
	b := base
	b.Principal = "agent:release-bot"

	assert.NotEqual(t, tr.Resolve(a), tr.Resolve(b),
		"two agent tokens in the same project must not share a work session")
}

// The tracker must not grow forever on a long-lived daemon. Reap is wired into
// the activity retention loop; this pins the behaviour it relies on.
func TestWorkSession_ReapDropsOnlyIdleEntries(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))

	stale := WorkSessionIdentity{ClientName: "claude-code", WorkspaceRoot: "/repos/old"}
	tr.Resolve(stale)

	advance(5 * time.Hour)

	active := WorkSessionIdentity{ClientName: "cursor", WorkspaceRoot: "/repos/live"}
	liveID := tr.Resolve(active)

	assert.Equal(t, 1, tr.Reap(4*time.Hour), "only the idle entry is dropped")
	assert.Equal(t, liveID, tr.Resolve(active), "the live work session is untouched")
}

// Spec 124 FR-003/FR-008: the run name splits one principal's work, and is a
// field of its own: the same name under two principals is two sessions.
func TestWorkSession_RunNameSplitsWithinAPrincipalOnly(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	base := WorkSessionIdentity{Principal: "agent:ci:mcp_agt_aaaa", ClientName: "rest"}

	unnamed := tr.Resolve(base)
	a := base
	a.RunName = "nightly-1"
	b := base
	b.RunName = "nightly-2"
	other := a
	other.Principal = "agent:ci:mcp_agt_bbbb"

	ida, idb, ido := tr.Resolve(a), tr.Resolve(b), tr.Resolve(other)
	assert.NotEqual(t, unnamed, ida)
	assert.NotEqual(t, ida, idb)
	assert.NotEqual(t, ida, ido, "a run name must never join two principals")
	assert.Equal(t, ida, tr.Resolve(a), "the same run stays one session")

	// A run name alone is an identity: an unauthenticated REST script that
	// names its run is grouped by it.
	assert.NotEmpty(t, tr.Resolve(WorkSessionIdentity{RunName: "solo"}))
}

// Spec 124 FR-009: the credential generation is part of the principal, so a
// regenerated token starts a new session inside the idle window.
func TestWorkSession_GenerationInPrincipalSplits(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	old := tr.Resolve(WorkSessionIdentity{Principal: "agent:ci:mcp_agt_old1", ClientName: "c"})
	advance(time.Minute)
	regenerated := tr.Resolve(WorkSessionIdentity{Principal: "agent:ci:mcp_agt_new1", ClientName: "c"})
	assert.NotEqual(t, old, regenerated)
}

// Spec 124 FR-019: Touch moves the idle clock of a session that is reused
// without a new Resolve.
func TestWorkSession_TouchKeepsALongConnectionAlive(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "p", ClientName: "ide"}
	ws := tr.Resolve(id)

	for i := 0; i < 5; i++ {
		advance(20 * time.Minute)
		tr.Touch(ws, id)
	}
	assert.Equal(t, ws, tr.Resolve(id), "100 minutes of steady calls is one session")
}

// A reaped or idle entry is reinstated under the id the connection still
// stamps, so a second connection joins it.
func TestWorkSession_TouchReinstatesAReapedEntry(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "p", ClientName: "ide"}
	ws := tr.Resolve(id)

	advance(3 * time.Hour)
	require.Equal(t, 1, tr.Reap(time.Hour))
	tr.Touch(ws, id)
	assert.Equal(t, ws, tr.Resolve(id))
}

// Touch never steals an identity that a newer live session owns.
func TestWorkSession_TouchLeavesANewerLiveSessionAlone(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "p", ClientName: "ide"}
	old := tr.Resolve(id)
	advance(time.Hour)
	fresh := tr.Resolve(id)
	require.NotEqual(t, old, fresh)

	tr.Touch(old, id)
	assert.Equal(t, fresh, tr.Resolve(id))
}

// Spec 124 FR-008: past the daily bound the name is ignored, the call is not
// refused, and the count restarts the next day.
func TestWorkSession_NamedSessionsAreBoundedPerPrincipalPerDay(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 15, 0, 0, 1, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "agent:ci:mcp_agt_aaaa", ClientName: "rest"}
	unnamed := tr.Resolve(id)

	seen := map[string]bool{}
	for i := 0; i < MaxNamedWorkSessionsPerDay; i++ {
		n := id
		n.RunName = "run-" + time.Duration(i).String()
		seen[tr.Resolve(n)] = true
	}
	require.Len(t, seen, MaxNamedWorkSessionsPerDay)

	over := id
	over.RunName = "one-too-many"
	assert.Equal(t, unnamed, tr.Resolve(over), "the name is ignored past the bound")

	// Another principal is not affected.
	otherP := over
	otherP.Principal = "agent:other:mcp_agt_bbbb"
	assert.NotEqual(t, unnamed, tr.Resolve(otherP))

	advance(24 * time.Hour)
	assert.NotEqual(t, unnamed, tr.Resolve(over), "the bound is per day")
}

// Review finding: work sessions are now resolved from any request goroutine,
// so the runtime's lazily created tracker must be one instance under
// concurrent first use. Run with -race.
func TestRuntime_WorkSessionTrackerIsCreatedOnce(t *testing.T) {
	r := &Runtime{}
	id := WorkSessionIdentity{Principal: "p", ClientName: "rest"}
	const n = 32
	got := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = r.ResolveWorkSession(id)
			r.TouchWorkSession(got[i], id)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		assert.Equal(t, got[0], got[i], "one tracker, one session for one identity")
	}
}

// The effective identity drops a run name that is over the bound.
func TestWorkSession_ResolveEffectiveReportsADroppedRunName(t *testing.T) {
	tr, _ := fixedClock(time.Date(2026, 11, 15, 0, 0, 1, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "p", ClientName: "rest"}
	unnamed := tr.Resolve(id)
	for i := 0; i < MaxNamedWorkSessionsPerDay; i++ {
		n := id
		n.RunName = "run-" + time.Duration(i).String()
		_, eff := tr.ResolveEffective(n)
		require.Equal(t, n.RunName, eff.RunName)
	}
	over := id
	over.RunName = "one-too-many"
	ws, eff := tr.ResolveEffective(over)
	assert.Equal(t, unnamed, ws)
	assert.Empty(t, eff.RunName, "the caller caches and touches under the unnamed identity")
}

// Review round 3 (Spec 124): a connection pinned a session before its
// workspace was known; another connection then opened a session under the
// full identity. Touching the first session with the full identity must keep
// its own entry alive, not let it be reaped while the connection still writes
// to it.
func TestWorkSession_TouchRefreshesASessionFiledUnderAnEarlierIdentity(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
	early := WorkSessionIdentity{Principal: "agent-1", ClientName: "ide", ClientVersion: "1.0"}
	full := early
	full.WorkspaceRoot = "/repos/project"

	w1 := tr.Resolve(early)
	w2 := tr.Resolve(full)
	require.NotEqual(t, w1, w2)

	// Both connections stay busy for longer than the idle window.
	for i := 0; i < 4; i++ {
		advance(20 * time.Minute)
		tr.Touch(w1, full) // the first connection now knows its workspace
		tr.Touch(w2, full)
	}
	assert.Equal(t, w2, tr.Resolve(full), "the full identity keeps its own session")
	assert.Equal(t, w1, tr.Resolve(early), "the earlier session was kept alive under its own identity")
	assert.Zero(t, tr.Reap(30*time.Minute))
}

// Review round 6 (Spec 124 FR-019): a session known only by its id (pinned on
// a request) has its idle clock moved by TouchID, so a later request of the
// same identity inside the idle window joins it.
func TestWorkSession_TouchIDMovesTheIdleClock(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "agent-1", ClientName: "rest", RunName: "nightly"}
	ws := tr.Resolve(id)

	advance(25 * time.Minute)
	tr.TouchID(ws) // a late sub-call's row
	advance(25 * time.Minute)
	assert.Equal(t, ws, tr.Resolve(id), "50 minutes after the start, 25 after the last row: the same session")

	tr.TouchID("") // no-ops
	tr.TouchID("unknown")
}

// Review round 8: Adopt files an identity under the session a connection
// keeps, replacing one that was opened beside it.
func TestWorkSession_AdoptReplacesACompetingSession(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
	id := WorkSessionIdentity{Principal: "agent-1", ClientName: "ide", ClientVersion: "1.0"}
	kept := tr.Resolve(id)
	advance(time.Hour) // idle: the next resolve opens another
	beside := tr.Resolve(id)
	require.NotEqual(t, kept, beside)

	tr.Adopt(kept, id)
	assert.Equal(t, kept, tr.Resolve(id), "a reconnect joins the session the connection writes to")
	tr.Adopt("", id) // no-ops
	tr.Adopt(kept, WorkSessionIdentity{})
}

// Review round 9: TouchID refreshes every identity the session is filed under.
func TestWorkSession_TouchIDRefreshesEveryAlias(t *testing.T) {
	tr, advance := fixedClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
	early := WorkSessionIdentity{Principal: "agent-1", ClientName: "ide", ClientVersion: "1.0"}
	full := early
	full.WorkspaceRoot = "/repos/project"
	w := tr.Resolve(early)
	tr.Touch(w, full) // the workspace arrived: the same session under the full identity too

	for i := 0; i < 3; i++ {
		advance(25 * time.Minute)
		tr.TouchID(w)
	}
	assert.Equal(t, w, tr.Resolve(full))
	assert.Equal(t, w, tr.Resolve(early))
}
