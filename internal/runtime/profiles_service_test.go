package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

type fakeEvaluator struct {
	tried    int
	explains int
	counts   ToolCounts
}

func (f *fakeEvaluator) EffectiveTools(_ context.Context, name string, _ EffectiveToolsOptions) (*EffectiveToolsResult, error) {
	return &EffectiveToolsResult{Profile: name}, nil
}
func (f *fakeEvaluator) TryProfile(context.Context, config.ProfileConfig, string, int) (*TryResult, error) {
	f.tried++
	return &TryResult{Results: []map[string]interface{}{}, Hidden: []TryHidden{}}, nil
}
func (f *fakeEvaluator) Explain(context.Context, profile.AccessSubject, string) (*AccessExplanation, error) {
	f.explains++
	return &AccessExplanation{}, nil
}
func (f *fakeEvaluator) ToolCounts(context.Context, string, ViewerScope) ToolCounts { return f.counts }

type profilesHarness struct {
	t   *testing.T
	rt  *Runtime
	svc *ProfilesService
	ev  *fakeEvaluator
}

func newProfilesHarness(t *testing.T) *profilesHarness {
	t.Helper()
	rt := newFunnelRuntime(t)
	h := &profilesHarness{t: t, rt: rt, svc: rt.ProfilesService(), ev: &fakeEvaluator{}}
	rt.SetProfileEvaluator(h.ev)
	return h
}

func (h *profilesHarness) actor() Actor {
	return Actor{Kind: "agent_token", Name: "ops-bot", Surface: profile.SurfaceMCP}
}

func (h *profilesHarness) mintClient(id, pin, mode string) {
	h.t.Helper()
	_, err := h.rt.StorageManager().MintClientCredential(id, "mcp_cli_"+id+"_svc_test_secret_0000000", funnelKey, mode, pin, time.Now().Add(time.Hour))
	require.NoError(h.t, err)
}

func (h *profilesHarness) mintToken(name, pin string) {
	h.t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(h.t, err)
	require.NoError(h.t, h.rt.StorageManager().CreateAgentToken(auth.AgentToken{
		Name: name, AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		ExpiresAt: time.Now().Add(time.Hour), ProfilePin: pin,
	}, raw, funnelKey))
}

func (h *profilesHarness) pin(name string) string {
	h.t.Helper()
	tok, err := h.rt.StorageManager().GetAgentTokenByName(name)
	require.NoError(h.t, err)
	require.NotNil(h.t, tok, name)
	return tok.ProfilePin
}

func (h *profilesHarness) profileNames() []string {
	cfg, err := h.rt.GetDesiredConfig()
	require.NoError(h.t, err)
	var out []string
	for _, p := range cfg.Profiles {
		out = append(out, p.Name)
	}
	return out
}

func (h *profilesHarness) records() []map[string]interface{} { return profileChanges(h.t, h.rt) }

func TestProfilesService_CreateUpdateAndConflicts(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()

	res, err := h.svc.Create(ctx, h.actor(), config.ProfileConfig{Name: "tmp", Servers: []string{"a", "ghost"}, MaxTier: "read"})
	require.NoError(t, err)
	assert.Equal(t, "tmp", res.Profile.Name)
	assert.Equal(t, []string{"a"}, res.Profile.EffectiveServers)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], `unknown server "ghost"`)

	// One record, attributed.
	recs := h.records()
	require.Len(t, recs, 1)
	assert.Equal(t, "create", recs[0]["change"])
	assert.Equal(t, "tmp", recs[0]["profile"])
	assert.Equal(t, "agent_token", recs[0]["actor_kind"])
	assert.Equal(t, "ops-bot", recs[0]["actor_name"])
	assert.Equal(t, "mcp", recs[0]["surface"])

	_, err = h.svc.Create(ctx, h.actor(), config.ProfileConfig{Name: "tmp"})
	var exists *ProfileExistsError
	require.True(t, errors.As(err, &exists))
	assert.Equal(t, "profile_exists", exists.Code())
	assert.Equal(t, `profile "tmp" already exists`, exists.Error())

	for _, name := range []string{"active", "try"} {
		_, err = h.svc.Create(ctx, h.actor(), config.ProfileConfig{Name: name})
		var ve *ValidationError
		require.True(t, errors.As(err, &ve), name)
		assert.Equal(t, "name", ve.Field)
		assert.Equal(t, `profile name "`+name+`" is reserved by the REST API`, ve.Message)
	}

	// Each fatal FR-007 rule is a 400 with its field and the unchanged text.
	_, err = h.svc.Create(ctx, h.actor(), config.ProfileConfig{Name: "bad", MaxTier: "bogus"})
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "max_tier", ve.Field)
	assert.Contains(t, ve.Message, `invalid max_tier "bogus": must be one of read, write, destructive`)
	assert.NotContains(t, h.profileNames(), "bad")

	// Update: a name mismatch is its own refusal.
	_, err = h.svc.Update(ctx, h.actor(), "tmp", config.ProfileConfig{Name: "other"})
	var mismatch *NameMismatchError
	require.True(t, errors.As(err, &mismatch))
	assert.Equal(t, "name_mismatch", mismatch.Code())

	_, err = h.svc.Update(ctx, h.actor(), "ghost", config.ProfileConfig{Name: "ghost"})
	var nf *ProfileNotFoundError
	require.True(t, errors.As(err, &nf))

	up, err := h.svc.Update(ctx, h.actor(), "tmp", config.ProfileConfig{Name: "tmp", Servers: []string{"a"}, MaxTier: "write"})
	require.NoError(t, err)
	assert.Equal(t, "write", up.Profile.MaxTier)
	recs = h.records()
	require.Len(t, recs, 2)

	// A write whose only change is tools.classify is recorded as classify.
	_, err = h.svc.Update(ctx, h.actor(), "tmp", config.ProfileConfig{Name: "tmp", Servers: []string{"a"}, MaxTier: "write",
		Tools: &config.ProfileToolRules{Classify: map[string]string{"a:t": "read"}}})
	require.NoError(t, err)
	changes := changesOf(h.records())
	assert.Equal(t, "tmp,", changes["classify"])
}

func TestProfilesService_DeleteRefusalsAndReassign(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	h.mintClient("cursor", "ro", auth.ProfileModeLocked)
	h.mintToken("ro-bot", "ro")
	sw := []string{"ro"}
	cfgTweak := func(d *config.Config) (ChangeHint, error) {
		d.Profiles[1].SwitchableTo = &sw // full may switch to ro
		return ChangeHint{}, nil
	}
	_, _, err := h.rt.MutateConfig(ctx, h.actor(), cfgTweak, TokenRewrite{})
	require.NoError(t, err)
	before := len(h.records())

	// In use, no reassign, no force.
	_, err = h.svc.Delete(ctx, h.actor(), "ro", "", false)
	var inUse *ProfileInUseError
	require.True(t, errors.As(err, &inUse))
	assert.Equal(t, []UsedByClient{{ID: "cursor", Mode: "locked"}}, inUse.UsedBy.Clients)
	assert.Equal(t, []string{"ro-bot"}, inUse.UsedBy.Tokens)
	assert.Contains(t, h.profileNames(), "ro")
	assert.Len(t, h.records(), before, "a refusal writes no record")

	// A bad reassign target is a 400 on reassign_to.
	for _, target := range []string{"ro", "ghost"} {
		_, err = h.svc.Delete(ctx, h.actor(), "ro", target, false)
		var ve *ValidationError
		require.True(t, errors.As(err, &ve), target)
		assert.Equal(t, "reassign_to", ve.Field)
	}

	// Reassign moves every pin to the target and removes the name everywhere.
	res, err := h.svc.Delete(ctx, h.actor(), "ro", "full", false)
	require.NoError(t, err)
	assert.Equal(t, "ro", res.Deleted)
	assert.Equal(t, []string{"cursor"}, res.Moved.Clients)
	assert.Equal(t, []string{"ro-bot"}, res.Moved.Tokens)
	assert.Equal(t, "full", h.pin("client-cursor"))
	assert.Equal(t, "full", h.pin("ro-bot"))
	assert.NotContains(t, h.profileNames(), "ro")
	cfg, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.Profiles[0].SwitchableTo)
	assert.Empty(t, *cfg.Profiles[0].SwitchableTo, "the deleted name is gone from switchable_to")

	recs := h.records()
	require.Len(t, recs, before+1)
	last := recs[0] // newest first
	assert.Equal(t, "delete", last["change"])
	assert.Equal(t, "ro", last["previous_profile"])
	assert.Equal(t, "full", last["profile"])
}

func TestProfilesService_DeleteForceLeavesDanglingPins(t *testing.T) {
	h := newProfilesHarness(t)
	h.mintToken("ro-bot", "ro")
	res, err := h.svc.Delete(context.Background(), h.actor(), "ro", "", true)
	require.NoError(t, err)
	assert.Empty(t, res.Moved.Tokens)
	assert.Equal(t, "ro", h.pin("ro-bot"), "force leaves the pin dangling (the resolver denies everything)")
	assert.NotContains(t, h.profileNames(), "ro")
}

func TestProfilesService_DeleteAnonymousProfile(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	require.NoError(t, h.svc.SetAnonymous(ctx, h.actor(), "full"))

	// Refused even with force and with an empty used_by.
	for _, force := range []bool{false, true} {
		_, err := h.svc.Delete(ctx, h.actor(), "full", "", force)
		var anon *ProfileIsAnonymousError
		require.True(t, errors.As(err, &anon), "force=%v", force)
		assert.Equal(t, "profile_is_anonymous_profile", anon.Code())
		assert.True(t, anon.UsedBy.AnonymousProfile)
	}
	assert.Contains(t, h.profileNames(), "full")

	// With reassign_to the anonymous_profile moves.
	res, err := h.svc.Delete(ctx, h.actor(), "full", "ro", false)
	require.NoError(t, err)
	assert.Equal(t, "ro", res.AnonymousProfileMovedTo)
	cfg, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, "ro", cfg.AnonymousProfile)
}

// FR-008a delta: a refusal leaves NOTHING changed - no profile deleted, no pin
// moved, no switchable_to edited, no record.
func TestProfilesService_GuardRefusalChangesNothing(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	h.mintClient("cursor", "ro", auth.ProfileModeLocked)
	h.mintToken("ro-bot", "ro")
	sw := []string{"ro"}
	_, _, err := h.rt.MutateConfig(ctx, h.actor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[1].SwitchableTo = &sw
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)

	cfgBefore, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)
	recBefore := len(h.records())
	h.rt.SetBindingGuard(stubGuard{refuse: &BindingGuardError{
		Bindings: []BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}},
		Fixes:    []GuardFix{{Kind: profile.GuardFixRequireMCPAuth}},
	}})

	for name, op := range map[string]func() error{
		"delete with reassign": func() error { _, err := h.svc.Delete(ctx, h.actor(), "ro", "full", false); return err },
		"delete with force":    func() error { _, err := h.svc.Delete(ctx, h.actor(), "ro", "", true); return err },
		"rename":               func() error { _, err := h.svc.Rename(ctx, h.actor(), "ro", "ro2"); return err },
		"create":               func() error { _, err := h.svc.Create(ctx, h.actor(), config.ProfileConfig{Name: "new"}); return err },
		"update": func() error {
			_, err := h.svc.Update(ctx, h.actor(), "ro", config.ProfileConfig{Name: "ro", Servers: []string{"a", "b"}})
			return err
		},
		"classify": func() error { _, err := h.svc.Classify(ctx, h.actor(), "ro", "a:t", "read"); return err },
	} {
		err := op()
		var refusal *BindingGuardError
		require.True(t, errors.As(err, &refusal), "%s: %v", name, err)
	}

	cfgAfter, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, cfgBefore.Profiles, cfgAfter.Profiles)
	assert.Equal(t, "ro", h.pin("client-cursor"))
	assert.Equal(t, "ro", h.pin("ro-bot"))
	assert.Len(t, h.records(), recBefore)
}

// spyGuard wraps the real ConservativeBindingGuard, records the (current,
// candidate) pins every evaluation saw, and can additionally refuse any
// candidate that re-pins a client onto a named profile.
type spyGuard struct {
	inner        ConservativeBindingGuard
	refuseRepin  string // refuse when a candidate client is pinned to this profile
	sawCurrent   []string
	sawCandidate []string
}

func (g *spyGuard) BindingGuardDelta(cur, cand GuardState) []BindingRef {
	for _, t := range cur.Tokens {
		g.sawCurrent = append(g.sawCurrent, t.ProfilePin)
	}
	var refs []BindingRef
	for i := range cand.Tokens {
		g.sawCandidate = append(g.sawCandidate, cand.Tokens[i].ProfilePin)
		if g.refuseRepin != "" && cand.Tokens[i].ProfilePin == g.refuseRepin {
			refs = append(refs, BindingRefOf(&cand.Tokens[i]))
		}
	}
	return append(refs, g.inner.BindingGuardDelta(cur, cand)...)
}
func (g *spyGuard) BindingGuardFixes(s GuardState, r []BindingRef) []GuardFix {
	return g.inner.BindingGuardFixes(s, r)
}
func (g *spyGuard) BindingGuardActiveBindings() []BindingRef { return nil }

// Delete-with-reassign consults the binding guard over the tokens AS THEY WOULD
// BE after the re-pin, and a refusal leaves the pin where it was. Run with
// require_mcp_auth OFF, the state in which the conservative guard is live: it
// treats an already-bound client as unchanged (so reassigning is allowed), and
// a guard that refuses the re-pin must stop the delete. Without the guard call
// in the funnel the pin moves and this test fails.
func TestProfilesService_GuardSeesTheReassignedBindingsOnDelete(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	_, _, err := h.rt.MutateConfig(ctx, h.actor(), func(d *config.Config) (ChangeHint, error) {
		d.RequireMCPAuth = false
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	h.mintClient("cursor", "ro", auth.ProfileModeLocked)

	spy := &spyGuard{refuseRepin: "full"}
	h.rt.SetBindingGuard(spy)
	_, err = h.svc.Delete(ctx, h.actor(), "ro", "full", false)
	var refusal *BindingGuardError
	require.True(t, errors.As(err, &refusal), "the guard must be consulted and refuse: %v", err)
	assert.Equal(t, "ro", h.pin("client-cursor"), "a refused delete leaves the pin alone")
	assert.Contains(t, h.profileNames(), "ro", "a refused delete removes nothing")
	assert.Contains(t, spy.sawCurrent, "ro")
	assert.Contains(t, spy.sawCandidate, "full", "the guard saw the post-reassign binding")

	// The real conservative guard, auth off: an already-bound client is not
	// newly bypassable, so the reassign goes through.
	h.rt.SetBindingGuard(ConservativeBindingGuard{})
	_, err = h.svc.Delete(ctx, h.actor(), "ro", "full", false)
	require.NoError(t, err)
	assert.Equal(t, "full", h.pin("client-cursor"))
}

func TestProfilesService_RenameMovesEveryReference(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	h.mintClient("cursor", "ro", auth.ProfileModeLocked)
	h.mintToken("ro-bot", "ro")
	sw := []string{"ro"}
	_, _, err := h.rt.MutateConfig(ctx, h.actor(), func(d *config.Config) (ChangeHint, error) {
		d.Profiles[1].SwitchableTo = &sw
		d.AnonymousProfile = "ro"
		return ChangeHint{}, nil
	}, TokenRewrite{})
	require.NoError(t, err)
	before := len(h.records())

	res, err := h.svc.Rename(ctx, h.actor(), "ro", "ro2")
	require.NoError(t, err)
	assert.Equal(t, "ro2", res.Profile.Name)
	assert.Equal(t, []string{"cursor"}, res.Moved.Clients)
	assert.Equal(t, []string{"ro-bot"}, res.Moved.Tokens)
	assert.Equal(t, "ro2", h.pin("client-cursor"))
	assert.Equal(t, "ro2", h.pin("ro-bot"))
	cfg, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, "ro2", cfg.AnonymousProfile)
	assert.Equal(t, []string{"ro2"}, *cfg.Profiles[1].SwitchableTo)

	recs := h.records()
	require.Len(t, recs, before+1, "one rename record, not a delete+create pair")
	assert.Equal(t, "rename", recs[0]["change"])
	assert.Equal(t, "ro", recs[0]["previous_profile"])
	assert.Equal(t, "ro2", recs[0]["profile"])

	// Conflicts.
	_, err = h.svc.Rename(ctx, h.actor(), "ro2", "full")
	var exists *ProfileExistsError
	require.True(t, errors.As(err, &exists))
	_, err = h.svc.Rename(ctx, h.actor(), "ro2", "active")
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "new_name", ve.Field)
	_, err = h.svc.Rename(ctx, h.actor(), "ro2", "Bad Name")
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "new_name", ve.Field)
	assert.Equal(t, "ro2", h.pin("client-cursor"), "a refused rename moves nothing")
}

func TestProfilesService_RenameFailureNeverWidens(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	h.mintClient("cursor", "ro", auth.ProfileModeLocked)
	before := len(h.records())

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	h.rt.configSvc.UpdatePath(filepath.Join(blocker, "mcp_config.json"))

	// (a) the config write fails after the re-pin: the pins are put back.
	_, err := h.svc.Rename(ctx, h.actor(), "ro", "ro2")
	require.Error(t, err)
	assert.Equal(t, "ro", h.pin("client-cursor"))
	assert.Len(t, h.records(), before, "no record for a failed write")

	// (b) the rollback also fails: the pin stays on the NEW name, which the
	// running config lacks - deny-all, never wider than "ro".
	h.rt.pinStoreOverride = &flakyPins{inner: h.rt.StorageManager(), failRestore: true}
	_, err = h.svc.Rename(ctx, h.actor(), "ro", "ro2")
	require.Error(t, err)
	assert.Equal(t, "ro2", h.pin("client-cursor"))
	running := h.rt.Config()
	for _, p := range running.Profiles {
		assert.NotEqual(t, "ro2", p.Name)
	}
	assert.Len(t, h.records(), before)
}

func TestProfilesService_ClassifyAndSetAnonymous(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()

	v, err := h.svc.Classify(ctx, h.actor(), "ro", "a:list", "read")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a:list": "read"}, v.Profile.Tools.Classify)
	recs := h.records()
	require.Len(t, recs, 1)
	assert.Equal(t, "classify", recs[0]["change"])

	v, err = h.svc.Classify(ctx, h.actor(), "ro", "a:list", "")
	require.NoError(t, err)
	assert.Empty(t, v.Profile.Tools.Classify)

	for _, bad := range []struct{ tool, tier string }{{"nocolon", "read"}, {"a:*", "read"}, {"a:t", "root"}} {
		_, err = h.svc.Classify(ctx, h.actor(), "ro", bad.tool, bad.tier)
		var ve *ValidationError
		require.True(t, errors.As(err, &ve), bad.tool)
		assert.Equal(t, "tools.classify", ve.Field)
	}
	_, err = h.svc.Classify(ctx, h.actor(), "ghost", "a:t", "read")
	var nf *ProfileNotFoundError
	require.True(t, errors.As(err, &nf))

	// anonymous: set, clear, unknown.
	n := len(h.records())
	require.NoError(t, h.svc.SetAnonymous(ctx, h.actor(), "ro"))
	require.NoError(t, h.svc.SetAnonymous(ctx, h.actor(), ""))
	recs = h.records()
	require.Len(t, recs, n+2)
	assert.Equal(t, "anonymous", recs[0]["change"])
	assert.Equal(t, "ro", recs[0]["previous_profile"])
	err = h.svc.SetAnonymous(ctx, h.actor(), "ghost")
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "anonymous_profile", ve.Field)
}

func TestProfilesService_TryPersistsNothing(t *testing.T) {
	h := newProfilesHarness(t)
	ctx := context.Background()
	cfgBefore, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)

	res, err := h.svc.Try(ctx, config.ProfileConfig{Servers: []string{"a"}, MaxTier: "read"}, "issue", 0)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, 1, h.ev.tried)

	cfgAfter, err := h.rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, cfgBefore.Profiles, cfgAfter.Profiles)
	assert.Empty(t, h.records())

	// A draft the validator rejects is a 400 with the field, not a search.
	_, err = h.svc.Try(ctx, config.ProfileConfig{Servers: []string{"a"}, MaxTier: "bogus"}, "issue", 5)
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "max_tier", ve.Field)
	assert.Equal(t, 1, h.ev.tried)
}

func TestProfilesService_UnwiredEvaluatorIs503NotAllowed(t *testing.T) {
	rt := newFunnelRuntime(t)
	svc := rt.ProfilesService()
	_, err := svc.EffectiveTools(context.Background(), "ro", EffectiveToolsOptions{})
	require.ErrorIs(t, err, ErrEvaluatorUnavailable)
	_, err = svc.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "ro"}, "a:t")
	require.ErrorIs(t, err, ErrEvaluatorUnavailable)
	_, err = svc.Try(context.Background(), config.ProfileConfig{Servers: []string{"a"}}, "q", 5)
	require.ErrorIs(t, err, ErrEvaluatorUnavailable)
}

func TestProfilesService_ListCarriesUsedByAndCounts(t *testing.T) {
	h := newProfilesHarness(t)
	h.ev.counts = ToolCounts{Read: 2, Write: 1}
	h.mintClient("cursor", "ro", auth.ProfileModeSwitchable)
	h.mintToken("ro-bot", "ro")
	ctx := context.Background()
	require.NoError(t, h.svc.SetAnonymous(ctx, h.actor(), "ro"))

	list, err := h.svc.List(ctx, ViewerScope{})
	require.NoError(t, err)
	assert.Equal(t, "ro", list.AnonymousProfile)
	require.Len(t, list.Profiles, 2)
	ro := list.Profiles[0]
	require.NotNil(t, ro.UsedBy)
	assert.Equal(t, []UsedByClient{{ID: "cursor", Mode: "switchable"}}, ro.UsedBy.Clients)
	assert.Equal(t, []string{"ro-bot"}, ro.UsedBy.Tokens)
	assert.True(t, ro.UsedBy.AnonymousProfile)
	assert.Equal(t, ToolCounts{Read: 2, Write: 1}, ro.ToolCounts)
	assert.True(t, ro.IsLegacy)

	// A restricted viewer: no used_by, no anonymous_profile.
	scoped, err := h.svc.List(ctx, ViewerScope{Restricted: true, Visible: func(string) bool { return true }, AllowedServers: []string{"a"}})
	require.NoError(t, err)
	assert.Empty(t, scoped.AnonymousProfile)
	for _, p := range scoped.Profiles {
		assert.Nil(t, p.UsedBy)
	}
}

func TestProfilesService_Stats24hCountsByProfileAndClient(t *testing.T) {
	h := newProfilesHarness(t)
	sm := h.rt.StorageManager()
	now := time.Now()
	save := func(typ, status, prof, client string) {
		require.NoError(t, sm.SaveActivity(&storage.ActivityRecord{Type: storage.ActivityType(typ), Status: status, Profile: prof, ClientID: client, Timestamp: now}))
	}
	save("tool_call", "success", "ro", "cursor")
	save("tool_call", "blocked", "ro", "cursor")
	save("policy_decision", "blocked", "ro", "cursor")
	save("tool_call", "success", "full", "")

	stats := h.svc.Stats24h(context.Background(), ViewerScope{})
	assert.Equal(t, Counter{Calls: 2, Blocked: 2}, stats.ByProfile["ro"])
	assert.Equal(t, Counter{Calls: 1}, stats.ByProfile["full"])
	assert.Equal(t, Counter{Calls: 2, Blocked: 2}, stats.ByClient["cursor"])
}
