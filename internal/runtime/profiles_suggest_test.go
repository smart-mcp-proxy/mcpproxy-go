package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// `access suggest` (#1700): the service reads blocked profile refusals and
// proposes changes. These tests pin the maintainer's rules: the explainer is
// re-run per group, a deny rule is never widened, an unannotated tool never
// gets a tier, the built-ins carry a warning, and nothing is ever written.

// suggestEvaluator answers Explain from a per-tool table.
type suggestEvaluator struct {
	fakeEvaluator
	byTool map[string]*AccessExplanation
	asked  []string
}

func (e *suggestEvaluator) Explain(_ context.Context, s profile.AccessSubject, tool string) (*AccessExplanation, error) {
	e.asked = append(e.asked, s.Profile+"|"+tool)
	if x, ok := e.byTool[tool]; ok {
		return x, nil
	}
	return &AccessExplanation{Verdict: profile.ExplainVerdictAllowed}, nil
}

func blockedExplanation(step profile.ExplainStep, fixes ...Fix) *AccessExplanation {
	return &AccessExplanation{Verdict: profile.ExplainVerdictBlocked, FirstFailure: step, Fixes: fixes}
}

type suggestRig struct {
	*profilesHarness
	ev2 *suggestEvaluator
}

func newSuggestRig(t *testing.T) *suggestRig {
	t.Helper()
	h := newProfilesHarness(t)
	ev := &suggestEvaluator{byTool: map[string]*AccessExplanation{
		"github:create_issue": blockedExplanation(profile.StepTierCap,
			Fix{Step: profile.StepTierCap, Action: profile.FixAllowInProfile, Target: "research", Label: "Allow github:create_issue in research"}),
		"github:search_code": blockedExplanation(profile.StepTierCap,
			Fix{Step: profile.StepTierCap, Action: profile.FixClassifyInProfile, Target: "research", Label: "Classify github:search_code in research"},
			Fix{Step: profile.StepTierCap, Action: profile.FixAllowInProfile, Target: "research", Label: "Allow github:search_code in research"}),
		"github:delete_repo": blockedExplanation(profile.StepToolRule,
			Fix{Step: profile.StepToolRule, Action: profile.FixAllowInProfile, Target: "research", Label: "Allow github:delete_repo in research"}),
		"slack:post": blockedExplanation(profile.StepServerInScope,
			Fix{Step: profile.StepServerInScope, Action: profile.FixAddServerToProfile, Target: "research", Label: "Add slack to research"}),
		"github:already_ok": {Verdict: profile.ExplainVerdictAllowed},
		// Refused by the tier cap when recorded; a deny rule has been added since.
		"github:now_denied": blockedExplanation(profile.StepToolRule,
			Fix{Step: profile.StepToolRule, Action: profile.FixAllowInProfile, Target: "research", Label: "Allow github:now_denied in research"}),
	}}
	h.rt.SetProfileEvaluator(ev)
	_, err := h.svc.Create(context.Background(), h.actor(), config.ProfileConfig{Name: "research", Servers: []string{"github"}, MaxTier: "read"})
	require.NoError(t, err)
	return &suggestRig{profilesHarness: h, ev2: ev}
}

func (r *suggestRig) block(prof, server, tool string, reason profile.BlockReason, token string, age time.Duration, requestID string) {
	r.t.Helper()
	require.NoError(r.t, r.rt.StorageManager().SaveActivity(&storage.ActivityRecord{
		Type: storage.ActivityTypePolicyDecision, Status: "blocked", Profile: prof, ServerName: server, ToolName: tool,
		BlockReason: string(reason), TokenName: token, RequestID: requestID, Timestamp: time.Now().Add(-age),
	}))
}

func (r *suggestRig) find(res *AccessSuggestions, tool string, reason profile.BlockReason) *AccessSuggestion {
	for i := range res.Suggestions {
		if res.Suggestions[i].Tool == tool && res.Suggestions[i].BlockReason == reason {
			return &res.Suggestions[i]
		}
	}
	return nil
}

func TestSuggest_GroupsCountsAndRechecks(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Hour, "r1")
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", 2*time.Hour, "r2")
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "dev-bot", 3*time.Hour, "r3")
	r.block("research", "github", "already_ok", profile.BlockReasonTier, "ci-bot", time.Hour, "r4")
	r.block("gone", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Hour, "r5")
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", 48*time.Hour, "r6") // outside 24h
	require.NoError(t, r.rt.StorageManager().SaveActivity(&storage.ActivityRecord{
		Type: storage.ActivityTypePolicyDecision, Status: "blocked", Profile: "research", ServerName: "github", ToolName: "x",
		BlockReason: "quarantined", Timestamp: time.Now(),
	}))
	require.NoError(t, r.rt.StorageManager().SaveActivity(&storage.ActivityRecord{
		Type: storage.ActivityTypePolicyDecision, Status: "blocked", ServerName: "github", ToolName: "y",
		BlockReason: string(profile.BlockReasonTier), Timestamp: time.Now(),
	}))

	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)

	require.Len(t, res.Suggestions, 1)
	s := res.Suggestions[0]
	assert.Equal(t, "research", s.Profile)
	assert.Equal(t, "github:create_issue", s.Tool)
	assert.Equal(t, 3, s.Count, "the 48h-old record is outside the default window")
	assert.Equal(t, []string{"ci-bot", "dev-bot"}, s.Tokens)
	assert.Equal(t, SuggestAllow, s.Suggestion)
	require.Len(t, s.Fixes, 1)
	assert.Equal(t, profile.FixAllowInProfile, s.Fixes[0].Action)

	assert.Equal(t, 1, res.AlreadyFixed)
	assert.Equal(t, 1, res.ProfileRemoved)
	assert.Equal(t, 1, res.NonProfileBlocked)
	assert.Equal(t, 1, res.Unattributed)
	assert.Equal(t, "24h0m0s", res.Window)
}

func TestSuggest_WindowFiltersAndRetention(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", 48*time.Hour, "r1")

	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	assert.Empty(t, res.Suggestions)

	res, err = r.svc.Suggest(context.Background(), SuggestRequest{Since: 7 * 24 * time.Hour})
	require.NoError(t, err)
	require.Len(t, res.Suggestions, 1)
	assert.Equal(t, 1, res.Suggestions[0].Count)
}

func TestSuggest_ProfileAndTokenFilters(t *testing.T) {
	r := newSuggestRig(t)
	_, err := r.svc.Create(context.Background(), r.actor(), config.ProfileConfig{Name: "other", Servers: []string{"github"}, MaxTier: "read"})
	require.NoError(t, err)
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Hour, "r1")
	r.block("other", "github", "create_issue", profile.BlockReasonTier, "dev-bot", time.Hour, "r2")

	res, err := r.svc.Suggest(context.Background(), SuggestRequest{Profile: "other"})
	require.NoError(t, err)
	require.Len(t, res.Suggestions, 1)
	assert.Equal(t, "other", res.Suggestions[0].Profile)

	res, err = r.svc.Suggest(context.Background(), SuggestRequest{Token: "ci-bot"})
	require.NoError(t, err)
	require.Len(t, res.Suggestions, 1)
	assert.Equal(t, []string{"ci-bot"}, res.Suggestions[0].Tokens)
}

func TestSuggest_SameRequestCountsOnce(t *testing.T) {
	r := newSuggestRig(t)
	sm := r.rt.StorageManager()
	for _, typ := range []storage.ActivityType{storage.ActivityTypePolicyDecision, storage.ActivityTypeToolCall} {
		require.NoError(t, sm.SaveActivity(&storage.ActivityRecord{
			Type: typ, Status: "blocked", Profile: "research", ServerName: "github", ToolName: "create_issue",
			BlockReason: string(profile.BlockReasonTier), RequestID: "same", Timestamp: time.Now(),
		}))
	}
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	require.Len(t, res.Suggestions, 1)
	assert.Equal(t, 1, res.Suggestions[0].Count)
	assert.Equal(t, 1, res.BlockedCalls)
}

func TestSuggest_DenyRuleIsReportedNeverWidened(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "delete_repo", profile.BlockReasonRule, "ci-bot", time.Hour, "r1")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{IncludePatch: true})
	require.NoError(t, err)
	s := r.find(res, "github:delete_repo", profile.BlockReasonRule)
	require.NotNil(t, s)
	assert.Equal(t, SuggestNone, s.Suggestion)
	assert.Empty(t, s.Fixes, "the explainer's allow fix would not help against a deny rule and is not offered")
	assert.Contains(t, s.Note, "explicit rule")
	assert.Empty(t, res.Patches)
}

func TestSuggest_UnannotatedToolNeverGetsATier(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "search_code", profile.BlockReasonUnannotated, "ci-bot", time.Hour, "r1")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{IncludePatch: true})
	require.NoError(t, err)
	s := r.find(res, "github:search_code", profile.BlockReasonUnannotated)
	require.NotNil(t, s)
	assert.Equal(t, SuggestClassify, s.Suggestion)
	assert.Equal(t, []string{"tier"}, s.NeedsInput)
	assert.Empty(t, res.Patches, "classify is not apply-ready: no tier is assumed")
}

func TestSuggest_BuiltinToolsCarryAWarningAndNoFix(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "", "code_execution", profile.BlockReasonCodeExecution, "ci-bot", time.Hour, "r1")
	r.block("research", "", "upstream_servers", profile.BlockReasonManagement, "ci-bot", time.Hour, "r2")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{IncludePatch: true})
	require.NoError(t, err)

	ce := r.find(res, "code_execution", profile.BlockReasonCodeExecution)
	require.NotNil(t, ce)
	assert.Equal(t, SuggestNone, ce.Suggestion)
	assert.Contains(t, ce.Warning, "code_execution")
	mg := r.find(res, "upstream_servers", profile.BlockReasonManagement)
	require.NotNil(t, mg)
	assert.Contains(t, mg.Warning, "management_tools")
	assert.Empty(t, res.Patches)
	assert.Empty(t, r.ev2.asked, "built-in tools are not walked by the explainer")
}

func TestSuggest_ServerScopeSuggestsAddingTheServer(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "slack", "post", profile.BlockReasonServerScope, "ci-bot", time.Hour, "r1")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	s := r.find(res, "slack:post", profile.BlockReasonServerScope)
	require.NotNil(t, s)
	assert.Equal(t, SuggestAddServer, s.Suggestion)
}

func TestSuggest_PatchMergesApplyReadyChangesOnly(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Hour, "r1")
	r.block("research", "slack", "post", profile.BlockReasonServerScope, "ci-bot", time.Hour, "r2")
	r.block("research", "github", "search_code", profile.BlockReasonUnannotated, "ci-bot", time.Hour, "r3")

	without, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	assert.Empty(t, without.Patches, "the patch is opt-in")

	res, err := r.svc.Suggest(context.Background(), SuggestRequest{IncludePatch: true})
	require.NoError(t, err)
	require.Len(t, res.Patches, 1)
	p := res.Patches[0]
	assert.Equal(t, "research", p.Profile)
	assert.ElementsMatch(t, []string{"tools.allow += github:create_issue", "servers += slack"}, p.Changes)
	assert.Equal(t, []string{"github", "slack"}, p.Proposed.Servers)
	require.NotNil(t, p.Proposed.Tools)
	assert.Equal(t, []string{"github:create_issue"}, p.Proposed.Tools.Allow)
	assert.Equal(t, "read", p.Proposed.MaxTier, "the rest of the stored profile is carried over")
	assert.Empty(t, p.Proposed.Tools.Classify, "no tier is invented")

	// The stored profile is untouched.
	cfg := r.rt.Config()
	i := indexOfProfile(cfg, "research")
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, []string{"github"}, cfg.Profiles[i].Servers)
	assert.Nil(t, cfg.Profiles[i].Tools)
}

func TestSuggest_NeverWritesAProfile(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Hour, "r1")
	before := len(r.records())
	_, err := r.svc.Suggest(context.Background(), SuggestRequest{IncludePatch: true})
	require.NoError(t, err)
	assert.Len(t, r.records(), before, "suggest writes no profile_change record")
}

func TestSuggest_SortedByCountThenRecency(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "create_issue", profile.BlockReasonTier, "a", 5*time.Hour, "r1")
	r.block("research", "slack", "post", profile.BlockReasonServerScope, "a", 1*time.Hour, "r2")
	r.block("research", "slack", "post", profile.BlockReasonServerScope, "a", 2*time.Hour, "r3")
	r.block("research", "github", "search_code", profile.BlockReasonUnannotated, "a", 1*time.Hour, "r4")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	require.Len(t, res.Suggestions, 3)
	assert.Equal(t, "slack:post", res.Suggestions[0].Tool)
	assert.Equal(t, "github:search_code", res.Suggestions[1].Tool, "equal counts: most recent first")
	assert.Equal(t, "github:create_issue", res.Suggestions[2].Tool)
}

func TestSuggest_RefusesWithoutEvaluator(t *testing.T) {
	rt := newFunnelRuntime(t)
	_, err := rt.ProfilesService().Suggest(context.Background(), SuggestRequest{})
	assert.ErrorIs(t, err, ErrEvaluatorUnavailable)
}

func TestParseSuggestWindow(t *testing.T) {
	ok := map[string]time.Duration{
		"":    DefaultSuggestWindow,
		"24h": 24 * time.Hour,
		"90m": 90 * time.Minute,
		"7d":  7 * 24 * time.Hour,
		" 1d": 24 * time.Hour,
	}
	for in, want := range ok {
		got, err := ParseSuggestWindow(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"0", "0h", "-1h", "-2d", "abc", "1.5d", "d", "7w"} {
		_, err := ParseSuggestWindow(bad)
		assert.Error(t, err, bad)
	}
}

func TestSuggest_CurrentDenyRuleIsNeverWidened(t *testing.T) {
	r := newSuggestRig(t)
	r.block("research", "github", "now_denied", profile.BlockReasonTier, "ci-bot", time.Hour, "r1")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{IncludePatch: true})
	require.NoError(t, err)
	s := r.find(res, "github:now_denied", profile.BlockReasonTier)
	require.NotNil(t, s)
	assert.Equal(t, SuggestNone, s.Suggestion, "the fresh explanation stops at a deny rule; the recorded tier refusal is history")
	assert.Empty(t, s.Fixes)
	assert.Contains(t, s.Note, "explicit rule")
	assert.Empty(t, res.Patches)
}

func TestSuggest_BuiltinRefusalDropsOutOnceTheSettingIsOn(t *testing.T) {
	r := newSuggestRig(t)
	on := true
	_, err := r.svc.Create(context.Background(), r.actor(), config.ProfileConfig{Name: "scripts", Servers: []string{"github"}, MaxTier: "read", CodeExecution: &on, ManagementTools: &on})
	require.NoError(t, err)
	r.block("scripts", "", "code_execution", profile.BlockReasonCodeExecution, "ci-bot", time.Hour, "r1")
	r.block("scripts", "", "upstream_servers", profile.BlockReasonManagement, "ci-bot", time.Hour, "r2")
	r.block("research", "", "code_execution", profile.BlockReasonCodeExecution, "ci-bot", time.Hour, "r3")
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	for _, sg := range res.Suggestions {
		assert.NotEqual(t, "scripts", sg.Profile, "a built-in refusal of a profile that now allows it has dropped out: %+v", sg)
	}
	require.NotNil(t, r.find(res, "code_execution", profile.BlockReasonCodeExecution), "the profile that still refuses keeps its warning")
	assert.Equal(t, 2, res.AlreadyFixed)
}

func TestSuggest_CancelledContextReturnsWithRecordsLeft(t *testing.T) {
	r := newSuggestRig(t)
	for i := 0; i < 250; i++ { // more than the stream's channel buffer
		r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Minute, fmt.Sprintf("r%d", i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.svc.Suggest(ctx, SuggestRequest{})
	require.ErrorIs(t, err, context.Canceled)

	// The producer was left behind with records to send; it must be drained so
	// its read transaction ends and the store keeps accepting writes.
	for i := 0; i < 50; i++ {
		r.block("research", "github", "create_issue", profile.BlockReasonTier, "ci-bot", time.Minute, fmt.Sprintf("w%d", i))
	}
	res, err := r.svc.Suggest(context.Background(), SuggestRequest{})
	require.NoError(t, err)
	require.NotNil(t, r.find(res, "github:create_issue", profile.BlockReasonTier))
}

func TestDrainActivities_LetsTheProducerFinish(t *testing.T) {
	ch := make(chan *storage.ActivityRecord, 100)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer close(ch)
		for i := 0; i < 250; i++ { // more than the buffer: the producer blocks without a reader
			ch <- &storage.ActivityRecord{RequestID: fmt.Sprintf("r%d", i)}
		}
	}()
	// The consumer takes a few records and walks away, as Suggest does on cancel.
	<-ch
	<-ch
	select {
	case <-finished:
		t.Fatal("the producer cannot have finished with no reader")
	case <-time.After(50 * time.Millisecond):
	}

	drainActivities(ch)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("drainActivities did not let the producer finish")
	}
}
