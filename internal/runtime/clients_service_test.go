package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

var svcTestKey = []byte("clients-service-test-key-0123456")

type svcHarness struct {
	t        *testing.T
	svc      *ClientsService
	sm       *storage.Manager
	cfg      *config.Config
	mu       sync.Mutex
	records  []*storage.ActivityRecord
	events   []Event
	notified []string
	clock    time.Time
	guard    BindingGuard
	reader   fakeReader
}

type fakeReader struct {
	secrets map[string]string // client id -> secret held; missing = unreadable
}

func (f fakeReader) ClientSecret(id string) (string, bool, error) {
	s, ok := f.secrets[id]
	if !ok {
		return "", false, errors.New("unreadable")
	}
	return s, s != "", nil
}

type notifyFn func(string)

func (f notifyFn) NotifyBindingChanged(name string) { f(name) }

type stubGuard struct{ refuse *BindingGuardError }

func (g stubGuard) BindingGuardDelta(cur, cand GuardState) []BindingRef {
	if g.refuse == nil {
		return nil
	}
	return g.refuse.Bindings
}
func (g stubGuard) BindingGuardFixes(GuardState, []BindingRef) []GuardFix { return g.refuse.Fixes }
func (g stubGuard) BindingGuardActiveBindings() []BindingRef              { return nil }

func newSvcHarness(t *testing.T) *svcHarness {
	t.Helper()
	sm, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	h := &svcHarness{t: t, sm: sm, clock: time.Now(), reader: fakeReader{secrets: map[string]string{}}}
	h.cfg = config.DefaultConfig()
	h.cfg.RequireMCPAuth = true
	h.cfg.Profiles = []config.ProfileConfig{
		{Name: "ro", Servers: []string{"a"}},
		{Name: "full", Servers: []string{"a", "b"}},
	}
	h.svc = NewClientsService(ClientsServiceDeps{
		Store:   sm,
		HMACKey: func() ([]byte, error) { return svcTestKey, nil },
		Config:  func() *config.Config { return h.cfg },
		Guard: func() BindingGuard {
			if h.guard != nil {
				return h.guard
			}
			return ConservativeBindingGuard{}
		},
		Activity: func(r *storage.ActivityRecord) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.records = append(h.records, r)
			return nil
		},
		Publish: func(e Event) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.events = append(h.events, e)
		},
		Now: func() time.Time { return h.clock },
	})
	h.svc.SetNotifier(notifyFn(func(name string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.notified = append(h.notified, name)
	}))
	h.svc.SetConfigReader(readerFunc(func(id string) (string, bool, error) { return h.reader.ClientSecret(id) }))
	return h
}

type readerFunc func(string) (string, bool, error)

func (f readerFunc) ClientSecret(id string) (string, bool, error) { return f(id) }

func (h *svcHarness) changes() []map[string]interface{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]interface{}
	for _, r := range h.records {
		require.Equal(h.t, storage.ActivityTypeProfileChange, r.Type)
		out = append(out, r.Metadata)
	}
	return out
}

func (h *svcHarness) actor() Actor {
	return Actor{Kind: "api_key", Name: "", Surface: profile.SurfaceAPI}
}

// mint mints cursor's credential through the connect minter (Issue+Commit).
func (h *svcHarness) mint(clientID, prof string, mode *string) string {
	h.t.Helper()
	m := h.svc.ConnectMinter()
	intent := connect.CredentialIntent{Profile: &prof, Mode: mode, ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue(clientID, intent)
	require.NoError(h.t, err)
	require.NoError(h.t, m.Commit(clientID, intent, issued))
	return issued.Secret
}

func (h *svcHarness) authenticates(secret string) bool {
	_, err := h.sm.ValidateAgentToken(secret, svcTestKey)
	return err == nil
}

func strp(s string) *string { return &s }

func TestClientsService_SetBindingRecordsAndEffects(t *testing.T) {
	h := newSvcHarness(t)
	secret := h.mint("cursor", "ro", nil) // named profile -> locked by default
	require.True(t, h.authenticates(secret))
	require.Len(t, h.changes(), 1)
	mint := h.changes()[0]
	require.Equal(t, "assign", mint["change"])
	require.Equal(t, "", mint["previous_profile"], "a mint is an assign with an empty previous_profile")
	require.Equal(t, "ro", mint["profile"])
	require.Equal(t, "cursor", mint["client_id"])
	require.Equal(t, "client-cursor", mint["token_name"])

	ctx := context.Background()
	a := Actor{Kind: "agent_token", Name: "admin-bot", Surface: profile.SurfaceCLI}

	// profile change -> assign, mode kept (US2-2: mode omitted keeps the mode)
	v, err := h.svc.SetBinding(ctx, a, "cursor", "full", nil)
	require.NoError(t, err)
	require.Equal(t, "full", v.Profile)
	require.Equal(t, auth.ProfileModeLocked, v.Mode, "mode omitted keeps locked")
	require.Equal(t, profile.CredentialStateClient, v.CredentialState)
	rec := h.changes()[1]
	require.Equal(t, "assign", rec["change"])
	require.Equal(t, "ro", rec["previous_profile"])
	require.Equal(t, "full", rec["profile"])
	require.Equal(t, "agent_token", rec["actor_kind"])
	require.Equal(t, "admin-bot", rec["actor_name"])
	require.Equal(t, "cli", rec["surface"])
	require.Equal(t, []string{"client-cursor"}, h.notified)
	require.NotEmpty(t, h.events)
	last := h.events[len(h.events)-1]
	require.Equal(t, EventTypeClientBindingChanged, last.Type)
	require.Equal(t, "full", last.Payload["profile"])
	require.Equal(t, "ro", last.Payload["previous_profile"])
	require.True(t, h.authenticates(secret), "the secret is unchanged, the client config is never touched")

	// same profile, locked -> switchable is unlock; back is lock
	_, err = h.svc.SetBinding(ctx, a, "cursor", "full", strp("switchable"))
	require.NoError(t, err)
	require.Equal(t, "unlock", h.changes()[2]["change"])
	_, err = h.svc.SetBinding(ctx, a, "cursor", "full", strp("locked"))
	require.NoError(t, err)
	require.Equal(t, "lock", h.changes()[3]["change"])

	// profile and mode both change: one assign whose diff carries the mode
	_, err = h.svc.SetBinding(ctx, a, "cursor", "ro", strp("switchable"))
	require.NoError(t, err)
	require.Len(t, h.changes(), 5)
	require.Equal(t, "assign", h.changes()[4]["change"])
	diff := h.changes()[4]["diff"].(map[string]interface{})
	require.Equal(t, map[string]interface{}{"from": "locked", "to": "switchable"}, diff["mode"])

	// no-op: no record, no notification
	n := len(h.notified)
	_, err = h.svc.SetBinding(ctx, a, "cursor", "ro", strp("switchable"))
	require.NoError(t, err)
	require.Len(t, h.changes(), 5)
	require.Len(t, h.notified, n)

	// profile "" with mode omitted is All servers, switchable
	v, err = h.svc.SetBinding(ctx, a, "cursor", "", nil)
	require.NoError(t, err)
	require.Equal(t, "", v.Profile)
	require.Equal(t, auth.ProfileModeSwitchable, v.Mode)
}

func TestClientsService_SetBindingRefusalsWriteNothing(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	a := h.actor()

	// no credential at all
	_, err := h.svc.SetBinding(ctx, a, "cursor", "ro", nil)
	var noCred *NoClientCredentialError
	require.ErrorAs(t, err, &noCred)
	require.Equal(t, "client cursor has no active client credential; connect it with a profile first", err.Error())
	require.Equal(t, "no_client_credential", noCred.Code())

	h.mint("cursor", "ro", nil)
	before := len(h.changes())

	_, err = h.svc.SetBinding(ctx, a, "cursor", "ghost", nil)
	var val *ValidationError
	require.ErrorAs(t, err, &val)
	require.Equal(t, "profile", val.Field)
	require.Equal(t, `unknown profile "ghost"`, val.Message)

	_, err = h.svc.SetBinding(ctx, a, "cursor", "", strp("locked"))
	require.ErrorAs(t, err, &val)
	require.Equal(t, "mode", val.Field)

	_, err = h.svc.SetBinding(ctx, a, "cursor", "ro", strp("sticky"))
	require.ErrorAs(t, err, &val)
	require.Equal(t, "mode", val.Field)

	_, err = h.svc.SetBinding(ctx, a, "Bad/Id", "ro", nil)
	require.ErrorAs(t, err, &val)
	require.Equal(t, "id", val.Field)

	// guard refusal
	h.guard = stubGuard{refuse: &BindingGuardError{
		Bindings: []BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "full", Mode: "locked"}},
		Fixes:    []GuardFix{{Kind: "require_mcp_auth"}},
	}}
	_, err = h.svc.SetBinding(ctx, a, "cursor", "full", nil)
	var guardErr *BindingGuardError
	require.ErrorAs(t, err, &guardErr)
	h.guard = nil
	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile, "a refused write changes nothing")
	require.Len(t, h.changes(), before, "and records nothing")

	// revoked and expired credentials are refused with no_client_credential
	_, err = h.svc.Forget(ctx, a, "cursor", true)
	require.NoError(t, err)
	_, err = h.svc.SetBinding(ctx, a, "cursor", "ro", nil)
	require.ErrorAs(t, err, &noCred)
	require.Equal(t, profile.CredentialStateRevoked, noCred.State)
}

// The credential precondition (409 no_client_credential) is checked before the
// requested profile is validated: a client without an active credential gets
// the contract's 409 whatever profile the request names.
func TestClientsService_SetBindingCredentialCheckedBeforeProfile(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		revoked bool
		state   string
	}{
		{"no credential", false, "none"},
		{"revoked credential", true, "revoked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSvcHarness(t)
			a := h.actor()
			if tc.revoked {
				h.mint("cursor", "ro", nil)
				_, err := h.svc.Forget(ctx, a, "cursor", true)
				require.NoError(t, err)
			}
			_, err := h.svc.SetBinding(ctx, a, "cursor", "ghost", nil)
			var noCred *NoClientCredentialError
			require.ErrorAs(t, err, &noCred, "unknown profile must not mask the credential precondition")
			require.Equal(t, "no_client_credential", noCred.Code())
			require.Equal(t, tc.state, string(noCred.State))
		})
	}
}

func TestClientsService_AddIDRules(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	long := strings.Repeat("a", 57)
	cases := []struct{ id, msg string }{
		{"Dev/Laptop", `invalid client id "Dev/Laptop": must be lower-case letters, digits, '-' or '_', start with a letter or digit, and be at most 56 characters`},
		{"DevLaptop", `invalid client id "DevLaptop": must be lower-case letters, digits, '-' or '_', start with a letter or digit, and be at most 56 characters`},
		{long, `invalid client id "` + long + `": must be lower-case letters, digits, '-' or '_', start with a letter or digit, and be at most 56 characters`},
		{"cursor", `client id "cursor" is a supported client; use connect instead`},
	}
	for _, c := range cases {
		_, _, err := h.svc.Add(ctx, h.actor(), AddRequest{ID: c.id, Profile: "ro"})
		var val *ValidationError
		require.ErrorAs(t, err, &val, c.id)
		require.Equal(t, "id", val.Field)
		require.Equal(t, c.msg, err.Error())
	}
	toks, err := h.sm.ListAgentTokens()
	require.NoError(t, err)
	require.Empty(t, toks, "nothing minted for a rejected id")

	view, secret, err := h.svc.Add(ctx, h.actor(), AddRequest{ID: "acme-bot", Profile: "ro"})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(secret, "mcp_cli_"))
	require.Equal(t, "acme-bot", view.ID)
	require.Equal(t, "locked", view.Mode)
	require.True(t, h.authenticates(secret))
	require.Equal(t, "assign", h.changes()[0]["change"])

	_, _, err = h.svc.Add(ctx, h.actor(), AddRequest{ID: "acme-bot", Profile: "ro"})
	require.Error(t, err, "an active credential is never replaced by add")
}

func TestClientsService_ForgetRotateFinalizeRecords(t *testing.T) {
	h := newSvcHarness(t)
	ctx := context.Background()
	old := h.mint("cursor", "ro", nil)

	secret, view, err := h.svc.Rotate(ctx, h.actor(), "cursor")
	require.NoError(t, err)
	require.True(t, view.RotationPending)
	require.True(t, h.authenticates(old))
	require.True(t, h.authenticates(secret), "both secrets authenticate while staged")
	require.Len(t, h.changes(), 1, "stage writes no record")

	_, err = h.svc.FinalizeRotation(ctx, h.actor(), "cursor")
	require.NoError(t, err)
	require.False(t, h.authenticates(old))
	require.True(t, h.authenticates(secret))
	require.Len(t, h.changes(), 2)
	rot := h.changes()[1]
	require.Equal(t, "rotate", rot["change"])
	diff := rot["diff"].(map[string]interface{})
	require.Equal(t, "finalized", diff["outcome"])
	for k, v := range diff {
		if s, ok := v.(string); ok && k != "outcome" {
			require.LessOrEqual(t, len(s), 12, "only the 12-char display prefix, never a secret (%s)", k)
		}
	}

	_, err = h.svc.FinalizeRotation(ctx, h.actor(), "cursor")
	require.NoError(t, err, "second finalize is an idempotent no-op")
	require.Len(t, h.changes(), 2)

	_, err = h.svc.Forget(ctx, h.actor(), "cursor", true)
	require.NoError(t, err)
	require.False(t, h.authenticates(secret))
	forget := h.changes()[2]
	require.Equal(t, "forget", forget["change"])
	require.Equal(t, "client-cursor", forget["token_name"])
	require.Equal(t, true, forget["diff"].(map[string]interface{})["disconnected"])
}

func TestClientsService_BulkAssignSkipsRefusedClients(t *testing.T) {
	h := newSvcHarness(t)
	h.mint("cursor", "ro", nil)
	h.mint("windsurf", "ro", nil)
	h.mint("codex", "full", nil)

	// refuse windsurf only
	moved, skipped, err := h.svc.BulkAssign(context.Background(), h.actor(), "ro", "full", nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"cursor", "windsurf"}, moved)
	require.Empty(t, skipped)

	// revoke one and move back: the revoked one has no record bound to "full"
	// that is active, so it is not touched; the rest move.
	_, err = h.svc.Forget(context.Background(), h.actor(), "windsurf", false)
	require.NoError(t, err)
	h.guard = stubGuard{refuse: &BindingGuardError{
		Bindings: []BindingRef{{ClientID: "cursor", Profile: "ro"}}, Fixes: []GuardFix{{Kind: "require_mcp_auth"}},
	}}
	moved, skipped, err = h.svc.BulkAssign(context.Background(), h.actor(), "full", "ro", nil)
	require.NoError(t, err)
	require.Empty(t, moved)
	codes := map[string]string{}
	for _, s := range skipped {
		codes[s.ClientID] = s.Code
	}
	require.Equal(t, "binding_bypassable_without_auth", codes["cursor"])
	require.Equal(t, "binding_bypassable_without_auth", codes["codex"])
	require.Equal(t, "no_client_credential", codes["windsurf"])
}

func TestConnectMinter_FreshMintCommitAbortAndGuard(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	intent := connect.CredentialIntent{Profile: strp("ro"), ActorKind: "api_key", Surface: "web"}

	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	require.False(t, issued.Rotating)
	require.Equal(t, "locked", issued.Mode)
	require.True(t, strings.HasPrefix(issued.Secret, "mcp_cli_"))
	require.True(t, h.authenticates(issued.Secret))

	// abort of a fresh mint: revoked, and no record (nothing happened)
	require.NoError(t, m.Abort("cursor", intent, issued))
	require.False(t, h.authenticates(issued.Secret))
	require.Empty(t, h.changes())

	// re-mint after the aborted one replaces the revoked record
	issued, err = m.Issue("cursor", intent)
	require.NoError(t, err)
	require.NoError(t, m.Commit("cursor", intent, issued))
	require.Len(t, h.changes(), 1)
	require.Equal(t, "web", h.changes()[0]["surface"])

	// classification (D7)
	require.Equal(t, profile.CredentialStateClient, m.Classify("cursor", issued.Secret))
	other, _ := auth.GenerateClientToken()
	require.Equal(t, profile.CredentialStateRevoked, m.Classify("cursor", other), "no record for a mcp_cli_ secret -> revoked")
	require.True(t, m.HeldByRecord("cursor", issued.Secret))
	require.False(t, m.HeldByRecord("cursor", other))
	h.mint("windsurf", "ro", nil)
	require.Equal(t, profile.CredentialStateNone, m.Classify("windsurf", issued.Secret), "another client's credential is not this client's")

	// guard refusal at Issue: nothing minted
	h.guard = stubGuard{refuse: &BindingGuardError{Bindings: []BindingRef{{ClientID: "zed", Profile: "ro"}}, Fixes: []GuardFix{{Kind: "require_mcp_auth"}}}}
	_, err = m.Issue("zed", intent)
	var guardErr *BindingGuardError
	require.ErrorAs(t, err, &guardErr)
	toks, _ := h.sm.ListAgentTokens()
	for _, tk := range toks {
		require.NotEqual(t, "client-zed", tk.Name)
	}
}

func TestConnectMinter_ReconnectIsStagedRotation(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	old := h.mint("cursor", "ro", nil)

	// default intent keeps the existing binding
	intent := connect.CredentialIntent{ActorKind: "cli_offline", Surface: "cli"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	require.True(t, issued.Rotating)
	require.Equal(t, "ro", issued.Profile)
	require.Equal(t, "locked", issued.Mode)
	require.True(t, h.authenticates(old))
	require.True(t, h.authenticates(issued.Secret))

	// write failed -> abort: old still valid, one rotate record rolled_back
	require.NoError(t, m.Abort("cursor", intent, issued))
	require.True(t, h.authenticates(old))
	require.False(t, h.authenticates(issued.Secret))
	last := h.changes()[len(h.changes())-1]
	require.Equal(t, "rotate", last["change"])
	require.Equal(t, "rolled_back", last["diff"].(map[string]interface{})["outcome"])

	// success with a binding change applied after finalize
	intent = connect.CredentialIntent{Profile: strp("full"), Mode: strp("locked"), ActorKind: "api_key", Surface: "api"}
	issued, err = m.Issue("cursor", intent)
	require.NoError(t, err)
	view, _ := h.svc.Get("cursor")
	require.Equal(t, "ro", view.Profile, "the binding changes only after the write succeeded")
	require.NoError(t, m.Commit("cursor", intent, issued))
	require.False(t, h.authenticates(old))
	require.True(t, h.authenticates(issued.Secret))
	view, _ = h.svc.Get("cursor")
	require.Equal(t, "full", view.Profile)
	cs := h.changes()
	require.Equal(t, "rotate", cs[len(cs)-2]["change"])
	require.Equal(t, "finalized", cs[len(cs)-2]["diff"].(map[string]interface{})["outcome"])
	require.Equal(t, "assign", cs[len(cs)-1]["change"])
	require.Equal(t, []string{"client-cursor"}, h.notified)
}

// A reconnect of an EXPIRED locked client with no explicit binding must keep
// the recorded profile/mode; it must never silently widen to All servers.
func TestConnectMinter_ExpiredReconnectKeepsBinding(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	// The store validates expiry against the wall clock, so age the record in
	// place: mint it so it expires 300ms from now, then let it lapse.
	h.clock = time.Now().Add(-auth.MaxTokenExpiry + 300*time.Millisecond)
	h.mint("cursor", "ro", nil)
	time.Sleep(450 * time.Millisecond)
	h.clock = time.Now()
	view, err := h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, profile.CredentialStateExpired, view.CredentialState)

	intent := connect.CredentialIntent{ActorKind: "cli_offline", Surface: "cli"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	require.Equal(t, "ro", issued.Profile, "expired reconnect keeps the pinned profile")
	require.Equal(t, "locked", issued.Mode, "expired reconnect keeps the locked mode")
	require.NoError(t, m.Commit("cursor", intent, issued))
	require.True(t, h.authenticates(issued.Secret))
	view, err = h.svc.Get("cursor")
	require.NoError(t, err)
	require.Equal(t, "ro", view.Profile)
	require.Equal(t, "locked", view.Mode)

	// an explicit profile still overrides the recorded binding (the credential
	// minted above is active again, so this is a normal rotation)
	issued, err = m.Issue("cursor", connect.CredentialIntent{Profile: strp("full"), ActorKind: "api_key", Surface: "api"})
	require.NoError(t, err)
	require.Equal(t, "full", issued.Profile)
}

func TestClientsService_ReconcileStagedRotation(t *testing.T) {
	ctx := context.Background()
	stage := func(t *testing.T, h *svcHarness, id string) (oldSecret, newSecret string) {
		oldSecret = h.mint(id, "ro", nil)
		newSecret, _, err := h.svc.Rotate(ctx, h.actor(), id)
		require.NoError(t, err)
		return oldSecret, newSecret
	}
	count := func(h *svcHarness, outcome string) int {
		n := 0
		for _, c := range h.changes() {
			if c["change"] == "rotate" && c["diff"].(map[string]interface{})["outcome"] == outcome {
				n++
			}
		}
		return n
	}

	t.Run("config holds new -> finalize once", func(t *testing.T) {
		h := newSvcHarness(t)
		oldS, newS := stage(t, h, "cursor")
		h.reader.secrets["cursor"] = newS
		require.NoError(t, h.svc.Reconcile(ctx))
		require.NoError(t, h.svc.Reconcile(ctx))
		require.False(t, h.authenticates(oldS))
		require.True(t, h.authenticates(newS))
		require.Equal(t, 1, count(h, "finalized"))
	})
	t.Run("config holds old -> rollback", func(t *testing.T) {
		h := newSvcHarness(t)
		oldS, newS := stage(t, h, "cursor")
		h.reader.secrets["cursor"] = oldS
		require.NoError(t, h.svc.Reconcile(ctx))
		require.True(t, h.authenticates(oldS))
		require.False(t, h.authenticates(newS))
		require.Equal(t, 1, count(h, "rolled_back"))
	})
	t.Run("unreadable or neither -> keep both, warn, no record", func(t *testing.T) {
		h := newSvcHarness(t)
		oldS, newS := stage(t, h, "cursor")
		before := len(h.changes())
		require.NoError(t, h.svc.Reconcile(ctx)) // reader has no entry: unreadable
		h.reader.secrets["cursor"] = "mcp_cli_" + strings.Repeat("f", 64)
		require.NoError(t, h.svc.Reconcile(ctx)) // neither
		require.True(t, h.authenticates(oldS))
		require.True(t, h.authenticates(newS))
		require.Len(t, h.changes(), before)
		var codes []profile.WarningCode
		for _, w := range h.svc.Warnings(nil) {
			codes = append(codes, w.Code)
		}
		require.Contains(t, codes, profile.WarningClientRotationPending)
	})
	t.Run("custom client finalizes after the 24h overlap", func(t *testing.T) {
		h := newSvcHarness(t)
		oldS, newS := stage(t, h, "acme-bot")
		toks, err := h.sm.ListAgentTokens()
		require.NoError(t, err)
		var started time.Time
		for _, tk := range toks {
			if tk.Name == "client-acme-bot" {
				started = *tk.RotationStartedAt
			}
		}
		h.clock = started.Add(24*time.Hour - time.Minute)
		require.NoError(t, h.svc.ReconcileClient(ctx, "acme-bot"))
		require.True(t, h.authenticates(oldS), "23h59m: still pending")
		h.clock = started.Add(24 * time.Hour)
		require.NoError(t, h.svc.ReconcileClient(ctx, "acme-bot"))
		require.False(t, h.authenticates(oldS))
		require.True(t, h.authenticates(newS))
		require.Equal(t, 1, count(h, "finalized"))
	})
	t.Run("crash after stage 1: a new service instance rolls back", func(t *testing.T) {
		h := newSvcHarness(t)
		oldS, newS := stage(t, h, "cursor")
		h.reader.secrets["cursor"] = oldS // the write never happened
		fresh := NewClientsService(ClientsServiceDeps{
			Store: h.sm, HMACKey: func() ([]byte, error) { return svcTestKey, nil },
			Config: func() *config.Config { return h.cfg }, Now: func() time.Time { return h.clock },
		})
		fresh.SetConfigReader(readerFunc(func(id string) (string, bool, error) { return h.reader.ClientSecret(id) }))
		require.NoError(t, fresh.Reconcile(ctx))
		require.True(t, h.authenticates(oldS))
		require.False(t, h.authenticates(newS))
	})
	t.Run("crash after the config write: startup finalizes", func(t *testing.T) {
		h := newSvcHarness(t)
		oldS, newS := stage(t, h, "cursor")
		h.reader.secrets["cursor"] = newS // written, finalize never ran
		require.NoError(t, h.svc.Reconcile(ctx))
		require.False(t, h.authenticates(oldS))
		require.True(t, h.authenticates(newS))
	})
}

func TestClientsService_ForgetUnheldOnUndo(t *testing.T) {
	h := newSvcHarness(t)
	m := h.svc.ConnectMinter()
	secret := h.mint("cursor", "ro", nil)
	intent := connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}

	name, err := m.ForgetUnheld("cursor", secret, intent)
	require.NoError(t, err)
	require.Empty(t, name, "the restored entry still holds the credential")
	require.True(t, h.authenticates(secret))

	name, err = m.ForgetUnheld("cursor", "", intent)
	require.NoError(t, err)
	require.Equal(t, "client-cursor", name)
	require.False(t, h.authenticates(secret))
	last := h.changes()[len(h.changes())-1]
	require.Equal(t, "forget", last["change"])
	require.Equal(t, "undo", last["diff"].(map[string]interface{})["reason"])
}

func TestClientsService_Warnings(t *testing.T) {
	h := newSvcHarness(t)
	h.mint("cursor", "ro", nil)
	h.cfg.Profiles = []config.ProfileConfig{{Name: "full", Servers: []string{"a"}}} // ro deleted from config
	h.clock = h.clock.Add(auth.MaxTokenExpiry - 24*time.Hour)                       // expiring within 14 days

	got := map[profile.WarningCode]string{}
	for _, w := range h.svc.Warnings(map[string]profile.CredentialState{"codex": profile.CredentialStateAdminKey}) {
		got[w.Code] = w.ClientID
	}
	require.Equal(t, "cursor", got[profile.WarningClientCredentialExpiring])
	require.Equal(t, "cursor", got[profile.WarningProfileMissing])
	require.Equal(t, "codex", got[profile.WarningClientHoldsAdminKey])
}
