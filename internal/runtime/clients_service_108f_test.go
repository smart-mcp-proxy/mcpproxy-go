package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 108-f T068/T074b: the service half of the clients REST surface - custom
// clients, warning shape, the admin-key upgrade, the time-only reconciler.

func TestClientsService_CustomAddCarriesDisplayNameAndExpiry(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	expires := h.clock.Add(30 * 24 * time.Hour)

	view, secret, err := h.svc.Add(ctx, h.actor(), AddRequest{ID: "dev-laptop", DisplayName: "Dev laptop", Profile: "ro", ExpiresAt: expires})
	require.NoError(t, err)
	require.NotNil(t, view)
	assert.Equal(t, "Dev laptop", view.DisplayName)
	assert.Equal(t, "locked", view.Mode)
	require.NotNil(t, view.ExpiresAt)
	assert.WithinDuration(t, expires, *view.ExpiresAt, time.Second)
	assert.Regexp(t, `^mcp_cli_[0-9a-f]{64}$`, secret)
	assert.True(t, h.authenticates(secret))

	recs, err := h.svc.Records()
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "Dev laptop", recs[0].DisplayName)

	// One assign record with an empty previous_profile, never a secret.
	ch := h.changes()
	require.Len(t, ch, 1)
	assert.Equal(t, "assign", ch[0]["change"])
	assert.Equal(t, "", ch[0]["previous_profile"])
	assert.NotContains(t, fmt.Sprint(ch[0]), secret)

	// The invariants: a long display name, a supported id, a bad id.
	_, _, err = h.svc.Add(ctx, h.actor(), AddRequest{ID: "other", DisplayName: strings.Repeat("x", 65)})
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "display_name", ve.Field)
	for _, id := range []string{"Dev/Laptop", "DevLaptop", "cursor", strings.Repeat("a", 57)} {
		_, _, err = h.svc.Add(ctx, h.actor(), AddRequest{ID: id})
		require.True(t, errors.As(err, &ve), id)
		assert.Equal(t, "id", ve.Field, id)
	}
	assert.Len(t, h.changes(), 1, "a refused add writes no record")
}

func TestClientsService_WarningShapeSeverityAndAction(t *testing.T) {
	h := newSvcHarness(t)
	h.mint("cursor", "ro", nil)
	h.cfg.Profiles = nil                                      // ro deleted from config
	h.clock = h.clock.Add(auth.MaxTokenExpiry - 24*time.Hour) // expiring within 14 days
	_, _, err := h.svc.Rotate(context.Background(), h.actor(), "cursor")
	require.NoError(t, err)
	h.guard = stubGuard{}

	byCode := map[profile.WarningCode]Warning{}
	for _, w := range h.svc.Warnings(map[string]profile.CredentialState{"codex": profile.CredentialStateAdminKey}) {
		byCode[w.Code] = w
	}
	require.Contains(t, byCode, profile.WarningClientCredentialExpiring)
	require.Contains(t, byCode, profile.WarningClientRotationPending)
	require.Contains(t, byCode, profile.WarningProfileMissing)
	require.Contains(t, byCode, profile.WarningClientHoldsAdminKey)

	exp := byCode[profile.WarningClientCredentialExpiring]
	assert.Equal(t, profile.WarningSeverityWarn, exp.Severity)
	assert.Equal(t, &WarningAction{Kind: "reconnect_client", Target: "cursor"}, exp.Action)

	pending := byCode[profile.WarningClientRotationPending]
	assert.Equal(t, profile.WarningSeverityInfo, pending.Severity, "a rotation in progress is informational")
	assert.Equal(t, &WarningAction{Kind: "reconnect_client", Target: "cursor"}, pending.Action)

	missing := byCode[profile.WarningProfileMissing]
	assert.Equal(t, profile.WarningSeverityWarn, missing.Severity)
	assert.Equal(t, &WarningAction{Kind: "move_client", Target: "cursor"}, missing.Action)

	admin := byCode[profile.WarningClientHoldsAdminKey]
	assert.Equal(t, profile.WarningSeverityWarn, admin.Severity)
	assert.Equal(t, &WarningAction{Kind: profile.WarningActionUpgradeAdminKeyHolders}, admin.Action, "no target")
}

func TestClientsService_GuardWarningActionIsRequireAuth(t *testing.T) {
	h := newSvcHarness(t)
	h.guard = activeGuard{bindings: []BindingRef{{ClientID: "cursor", Profile: "ro", Mode: "locked"}}}
	var got *Warning
	for _, w := range h.svc.Warnings(nil) {
		if w.Code == profile.WarningAnonymousDeniedByBindingGuard {
			w := w
			got = &w
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, profile.WarningSeverityWarn, got.Severity)
	assert.Equal(t, &WarningAction{Kind: "change_setting", Target: "require_mcp_auth"}, got.Action)
	assert.NotEmpty(t, got.Bindings)
}

type activeGuard struct{ bindings []BindingRef }

func (g activeGuard) BindingGuardDelta(_, _ GuardState) []BindingRef { return nil }
func (g activeGuard) BindingGuardFixes(GuardState, []BindingRef) []GuardFix {
	return []GuardFix{{Kind: profile.GuardFixRequireMCPAuth}}
}
func (g activeGuard) BindingGuardActiveBindings() []BindingRef { return g.bindings }

func TestClientsService_ReconcileTimeOnlyFinalizesOnlyCustomClientsAfterTheOverlap(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	_, _, err := h.svc.Add(ctx, h.actor(), AddRequest{ID: "acme-bot"})
	require.NoError(t, err)
	_, _, err = h.svc.Rotate(ctx, h.actor(), "acme-bot")
	require.NoError(t, err)
	h.mint("cursor", "ro", nil)
	_, _, err = h.svc.Rotate(ctx, h.actor(), "cursor")
	require.NoError(t, err)

	pending := func(id string) bool {
		recs, err := h.svc.Records()
		require.NoError(t, err)
		for _, r := range recs {
			if r.ClientID == id {
				return r.PendingHash != ""
			}
		}
		return false
	}
	require.NoError(t, h.svc.ReconcileTimeOnly(ctx))
	assert.True(t, pending("acme-bot"), "inside the 24 h overlap nothing finalizes")

	h.clock = h.clock.Add(25 * time.Hour)
	require.NoError(t, h.svc.ReconcileTimeOnly(ctx))
	assert.False(t, pending("acme-bot"), "a custom client finalizes after the overlap")
	assert.True(t, pending("cursor"), "a supported client's rotation resolves from its config, never here")
}

// --- admin-key upgrade -------------------------------------------------------

type fakeUpgradePort struct {
	h        *svcHarness
	states   map[string]string // client id -> credential_state
	previews int
	fail     map[string]error
	wrote    []string
	intent   connect.CredentialIntent
}

func (p *fakeUpgradePort) GetStatus(id string) (connect.ClientStatus, error) {
	st, ok := p.states[id]
	if !ok {
		return connect.ClientStatus{ID: id}, nil
	}
	return connect.ClientStatus{ID: id, Connected: true, Exists: true, CredentialState: st}, nil
}

func (p *fakeUpgradePort) PreviewWithIntent(id, _ string, intent connect.CredentialIntent) (*connect.ConnectPreview, error) {
	p.previews++
	prof, mode := "", auth.ProfileModeSwitchable
	if intent.Profile != nil {
		prof = *intent.Profile
		if prof != "" {
			mode = auth.ProfileModeLocked
		}
	}
	return &connect.ConnectPreview{
		Client: id, DisplayPath: "~/." + id + "/mcp.json", EntryExists: true, EntryText: `{"url":"…"}`,
		Credential: "mcp_cli_••••", Profile: prof, Mode: mode, PreconditionToken: "tok-" + id,
	}, nil
}

func (p *fakeUpgradePort) ConnectWithOptions(id, _ string, opts connect.ConnectOptions) (*connect.ConnectResult, error) {
	if err := p.fail[id]; err != nil {
		return nil, err
	}
	p.intent = opts.Intent
	m := p.h.svc.ConnectMinter()
	issued, err := m.Issue(id, opts.Intent)
	if err != nil {
		return nil, err
	}
	if err := m.Commit(id, opts.Intent, issued); err != nil {
		return nil, err
	}
	p.wrote = append(p.wrote, id)
	p.states[id] = "client"
	return &connect.ConnectResult{Success: true, Client: id}, nil
}

func newUpgradeHarness(t *testing.T, states map[string]string) (*svcHarness, *fakeUpgradePort, *[]string) {
	h := newSvcHarness(t)
	h.cfg.RequireMCPAuth = false
	port := &fakeUpgradePort{h: h, states: states, fail: map[string]error{}}
	h.svc.SetUpgradePort(port)
	observed := &[]string{}
	h.svc.SetObserver(func(id string, st profile.CredentialState) { *observed = append(*observed, id+"="+string(st)) })
	return h, port, observed
}

func TestClientsService_UpgradePreviewIsMaskedAndWritesNothing(t *testing.T) {
	h, port, observed := newUpgradeHarness(t, map[string]string{"cursor": "admin_key", "codex": "admin_key", "windsurf": "client", "vscode": "none"})
	h.guard = stubGuard{} // this test is about masking; the guard has its own test
	ro := "ro"

	prev, err := h.svc.PreviewAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{Profile: &ro})
	require.NoError(t, err)
	require.Len(t, prev.Preview, 2, "only the admin-key holders")
	assert.Equal(t, "codex", prev.Preview[0].ClientID)
	assert.Equal(t, "cursor", prev.Preview[1].ClientID)
	for _, row := range prev.Preview {
		assert.Equal(t, "mcp_cli_••••", row.Credential)
		assert.Equal(t, "ro", row.Profile)
		assert.Equal(t, "locked", row.Mode)
		assert.NotEmpty(t, row.PreconditionToken)
		assert.NotEmpty(t, row.DisplayName)
	}
	assert.NotEmpty(t, prev.PreconditionToken)
	assert.Empty(t, prev.NextStep)
	assert.Nil(t, prev.Guard)

	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	assert.Empty(t, toks, "a preview mints nothing")
	assert.Empty(t, h.changes())
	assert.Empty(t, port.wrote)
	assert.Contains(t, *observed, "cursor=admin_key", "every on-demand classification is observed")
	assert.Contains(t, *observed, "windsurf=client")

	// With nobody holding the key the preview says what to do next.
	h2, _, _ := newUpgradeHarness(t, map[string]string{"cursor": "client"})
	prev, err = h2.svc.PreviewAdminKeyUpgrade(context.Background(), h2.actor(), UpgradeRequest{})
	require.NoError(t, err)
	assert.Empty(t, prev.Preview)
	assert.Equal(t, "rotate_admin_api_key", prev.NextStep)
}

func TestClientsService_UpgradeApplyMintsOneRecordPerClientAndReportsFailures(t *testing.T) {
	h, port, _ := newUpgradeHarness(t, map[string]string{"cursor": "admin_key", "codex": "admin_key", "zcode": "admin_key"})
	port.fail["zcode"] = errors.New("config is read-only")

	prev, err := h.svc.PreviewAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{})
	require.NoError(t, err)
	res, err := h.svc.ApplyAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{PreconditionToken: prev.PreconditionToken})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"cursor", "codex"}, res.Upgraded)
	require.Len(t, res.Failed, 1)
	assert.Equal(t, "zcode", res.Failed[0].ClientID)
	assert.Contains(t, res.Failed[0].Error, "read-only")
	assert.Empty(t, res.NextStep, "zcode still holds the admin key")

	assigns := 0
	for _, c := range h.changes() {
		if c["change"] == "assign" {
			assigns++
		}
	}
	assert.Equal(t, 2, assigns, "one assign record per upgraded client")

	// Once the last holder is gone the next step is the admin-key rotation.
	delete(port.fail, "zcode")
	res, err = h.svc.ApplyAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"zcode"}, res.Upgraded)
	assert.Equal(t, "rotate_admin_api_key", res.NextStep)
}

func TestClientsService_UpgradeApplyRefusesAStalePreviewToken(t *testing.T) {
	h, port, _ := newUpgradeHarness(t, map[string]string{"cursor": "admin_key"})
	_, err := h.svc.ApplyAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{PreconditionToken: "stale"})
	var pf *PreconditionFailedError
	require.True(t, errors.As(err, &pf))
	assert.Equal(t, "precondition_failed", pf.Code())
	assert.Empty(t, port.wrote)
	assert.Empty(t, h.changes())
}

// FR-008a over the WHOLE request: with require_mcp_auth off and no
// anonymous_profile, naming a profile would leave every upgraded client's
// binding bypassable (the conservative guard refuses any named binding).
func TestClientsService_UpgradeGuardIsWholeRequestAndOnlyForNamedProfiles(t *testing.T) {
	h, port, _ := newUpgradeHarness(t, map[string]string{"cursor": "admin_key", "codex": "admin_key"})
	h.guard = ConservativeBindingGuard{}
	ro := "ro"

	prev, err := h.svc.PreviewAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{Profile: &ro})
	require.NoError(t, err)
	require.NotNil(t, prev.Guard, "the preview reports the refusal the apply would return")
	assert.Equal(t, "binding_bypassable_without_auth", prev.Guard.Code)
	assert.Len(t, prev.Guard.Bindings, 2)
	assert.NotEmpty(t, prev.Guard.Fixes)

	_, err = h.svc.ApplyAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{Profile: &ro})
	var refusal *BindingGuardError
	require.True(t, errors.As(err, &refusal))
	assert.Empty(t, port.wrote, "nothing minted, no file written")
	toks, _ := h.sm.ListAgentTokens()
	assert.Empty(t, toks)
	assert.Empty(t, h.changes())

	// No profile: the guard never refuses (All servers cannot be bypassed).
	res, err := h.svc.ApplyAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{})
	require.NoError(t, err)
	assert.Len(t, res.Upgraded, 2)

	// An unknown profile is a 400 on profile.
	h2, _, _ := newUpgradeHarness(t, map[string]string{"cursor": "admin_key"})
	ghost := "ghost"
	_, err = h2.svc.PreviewAdminKeyUpgrade(context.Background(), h2.actor(), UpgradeRequest{Profile: &ghost})
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "profile", ve.Field)
}

func TestClientsService_UpgradeWithoutAPortIsUnavailable(t *testing.T) {
	h := newSvcHarness(t)
	_, err := h.svc.PreviewAdminKeyUpgrade(context.Background(), h.actor(), UpgradeRequest{})
	require.ErrorIs(t, err, connect.ErrNoCredentialMinter)
}
