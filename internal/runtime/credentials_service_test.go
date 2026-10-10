package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// countingStore wraps the real store and counts the mint calls, so a refusal
// can be proven to have reached no store write (T014/T015/T022a).
type countingStore struct {
	*storage.Manager
	creates, mints atomic.Int64
	failMint       bool
	failListAfter  atomic.Bool
}

func (c *countingStore) CreateAgentToken(t auth.AgentToken, raw string, key []byte) error {
	c.creates.Add(1)
	if c.failMint {
		return errors.New("disk full")
	}
	if err := c.Manager.CreateAgentToken(t, raw, key); err != nil {
		return err
	}
	c.failListAfter.Store(true)
	return nil
}

func (c *countingStore) MintClientCredentialWith(id, raw string, key []byte, opts storage.ClientMintOptions) (*auth.AgentToken, error) {
	c.mints.Add(1)
	if c.failMint {
		return nil, errors.New("disk full")
	}
	rec, err := c.Manager.MintClientCredentialWith(id, raw, key, opts)
	if err == nil {
		c.failListAfter.Store(true)
	}
	return rec, err
}

// listFailsAfterMint makes every listing fail once a mint committed: the
// T015a fault injection (no fallible step after the commit).
type listFailsAfterMint struct{ *countingStore }

func (l listFailsAfterMint) ListAgentTokens() ([]auth.AgentToken, error) {
	if l.failListAfter.Load() {
		return nil, errors.New("store unavailable")
	}
	return l.Manager.ListAgentTokens()
}

type credHarness struct {
	*svcHarness
	store     *countingStore
	cs        *CredentialsService
	generated atomic.Int64
}

func newCredHarness(t *testing.T) *credHarness { return newCredHarnessWith(t, false, false) }

func newCredHarnessWith(t *testing.T, failMint, listFails bool) *credHarness {
	t.Helper()
	h := &credHarness{svcHarness: newSvcHarness(t)}
	h.cfg.APIKey = "admin-api-key-0123456789abcdef0123456789abcdef"
	h.cfg.Profiles = append(h.cfg.Profiles, config.ProfileConfig{Name: "daily-research", Servers: []string{"a"}})
	h.store = &countingStore{Manager: h.sm, failMint: failMint}
	var clientStore ClientCredentialStore = h.store
	var tokenStore CredentialTokenStore = h.store
	if listFails {
		clientStore = listFailsAfterMint{h.store}
		tokenStore = listFailsAfterMint{h.store}
	}
	// Rebuild the clients service over the counting store, keeping the
	// harness's recording activity/publish/notifier.
	old := h.svc
	h.svc = NewClientsService(ClientsServiceDeps{
		Store: clientStore, HMACKey: old.hmacKey, Config: old.cfg, Guard: old.guard,
		Activity: old.activity, Publish: old.publish, Now: old.now,
	})
	h.svc.SetNotifier(old.notifier)
	h.cs = NewCredentialsService(CredentialsServiceDeps{
		Tokens: tokenStore, Clients: h.svc, HMACKey: old.hmacKey, Config: old.cfg, Guard: old.guard,
		Activity: old.activity, Publish: old.publish, Now: old.now,
		Generate: func() (string, error) { h.generated.Add(1); return auth.GenerateToken() },
	})
	return h
}

func mcpActor() Actor { return Actor{Kind: "api_key", Surface: profile.SurfaceMCP} }

func mcpTokenReq(name, prof, exp string) IssueTokenRequest {
	return IssueTokenRequest{Name: name, Profile: prof, ProfilePresent: true, ExpiresIn: exp, RequireProfile: true, EnforceGuard: true}
}

func mcpClientReq(id, prof, exp string) IssueClientRequest {
	return IssueClientRequest{ID: id, Profile: prof, ProfilePresent: true, ExpiresIn: exp, RequireProfile: true, RefuseExistingRecord: true}
}

func codeOf(err error) string {
	var ce *CredentialError
	if errors.As(err, &ce) {
		return ce.Code()
	}
	var se *SecretInputError
	if errors.As(err, &se) {
		return se.Code()
	}
	var ge *BindingGuardError
	if errors.As(err, &ge) {
		return ge.Code()
	}
	return ""
}

func (h *credHarness) listing() []auth.AgentToken {
	all, err := h.sm.ListAgentTokens()
	require.NoError(h.t, err)
	return all
}

func (h *credHarness) eventsOf(typ EventType) []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Event
	for _, e := range h.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// T010: IssueToken refusals and success in MCP mode.
func TestCredentialsService_IssueToken_MCPTable(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	// an expired and a revoked record holding names
	_, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("taken-active", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("taken-revoked", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "taken-revoked"}, false)
	require.NoError(t, err)
	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("taken-expired", "ro", "1h"))
	require.NoError(t, err)
	h.clock = h.clock.Add(2 * time.Hour)

	noProfile := mcpTokenReq("t1", "", "1h")
	noProfile.ProfilePresent = false
	cases := []struct {
		name  string
		req   IssueTokenRequest
		code  string
		state string
	}{
		{"profile missing", noProfile, profile.CredentialErrorCodeMissingArgument, ""},
		{"profile empty", mcpTokenReq("t1", "", "1h"), profile.CredentialErrorCodeProfileRequired, ""},
		{"profile unknown", mcpTokenReq("t1", "daily-reserch", "1h"), profile.CredentialErrorCodeUnknownProfile, ""},
		{"expiry missing", mcpTokenReq("t1", "ro", ""), profile.CredentialErrorCodeMissingArgument, ""},
		{"expiry malformed", mcpTokenReq("t1", "ro", "1y"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"expiry overflow", mcpTokenReq("t1", "ro", "106752d"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"expiry overflow 2", mcpTokenReq("t1", "ro", "9999999999d"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"expiry over cap", mcpTokenReq("t1", "ro", "400d"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"expiry zero", mcpTokenReq("t1", "ro", "0s"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"expiry negative", mcpTokenReq("t1", "ro", "-1h"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"name syntax", mcpTokenReq("bad name!", "ro", "1h"), profile.CredentialErrorCodeInvalidArgument, ""},
		{"name missing", mcpTokenReq("", "ro", "1h"), profile.CredentialErrorCodeMissingArgument, ""},
		{"reserved prefix", mcpTokenReq("client-x", "ro", "1h"), profile.CredentialErrorCodeReservedIdentity, ""},
		{"exists active", mcpTokenReq("taken-active", "ro", "1h"), profile.CredentialErrorCodeIdentityExists, "expired"},
		{"exists revoked", mcpTokenReq("taken-revoked", "ro", "1h"), profile.CredentialErrorCodeIdentityExists, "revoked"},
		{"exists expired", mcpTokenReq("taken-expired", "ro", "1h"), profile.CredentialErrorCodeIdentityExists, "expired"},
		{"purpose too long", func() IssueTokenRequest {
			r := mcpTokenReq("t1", "ro", "1h")
			r.Purpose = strings.Repeat("x", 501)
			return r
		}(), profile.CredentialErrorCodeInvalidArgument, ""},
	}
	before := len(h.listing())
	creates := h.store.creates.Load()
	gen := h.generated.Load()
	for _, c := range cases {
		_, err := h.cs.IssueToken(ctx, mcpActor(), c.req)
		require.Error(t, err, c.name)
		assert.Equal(t, c.code, codeOf(err), c.name)
		if c.state != "" {
			var ce *CredentialError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, c.state, ce.State, c.name)
		}
		// FR-020: unparsed values are never echoed.
		for _, v := range []string{"1y", "106752d", "9999999999d", "bad name!"} {
			assert.NotContains(t, err.Error(), v, c.name)
		}
	}
	assert.Len(t, h.listing(), before, "no refusal leaves a record")
	assert.Equal(t, creates, h.store.creates.Load(), "no refusal reaches the store")
	assert.Equal(t, gen, h.generated.Load(), "no secret is generated before validation passes")

	// Success: scope from the profile only, pinned, issuer, purpose, guard-bound.
	r := mcpTokenReq("research-task-42", "daily-research", "30m")
	r.Purpose = "Daily research digest"
	res, err := h.cs.IssueToken(ctx, mcpActor(), r)
	require.NoError(t, err)
	assert.Regexp(t, `^mcp_agt_[0-9a-f]{64}$`, res.Secret)
	assert.Equal(t, "token", res.View.Kind)
	assert.Equal(t, "pinned", res.View.Binding)
	assert.True(t, res.View.Lease)
	assert.Equal(t, "active", res.View.State)
	assert.Equal(t, "ok", res.View.ProfileState)
	assert.Equal(t, auth.TokenPrefix(res.Secret), res.View.TokenPrefix)
	tok, err := h.sm.GetAgentTokenByName("research-task-42")
	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, tok.AllowedServers)
	assert.ElementsMatch(t, []string{"read", "write", "destructive"}, tok.Permissions)
	assert.Equal(t, "daily-research", tok.ProfilePin)
	assert.True(t, tok.GuardBound)
	assert.Equal(t, &auth.CredentialIssuer{ActorKind: "api_key", Surface: "mcp"}, tok.Issuer)
	assert.Equal(t, "Daily research digest", tok.Purpose)
}

func TestCredentialsService_IssueToken_TokenLimit(t *testing.T) {
	h := newCredHarness(t)
	key := svcTestKey
	for i := 0; i < auth.MaxTokens; i++ {
		raw, _ := auth.GenerateToken()
		require.NoError(t, h.sm.CreateAgentToken(auth.AgentToken{Name: fmt.Sprintf("seed-%03d", i), AllowedServers: []string{"*"}, Permissions: []string{"read"}, ExpiresAt: time.Now().Add(time.Hour)}, raw, key))
	}
	_, err := h.cs.IssueToken(context.Background(), mcpActor(), mcpTokenReq("one-more", "ro", "1h"))
	assert.Equal(t, profile.CredentialErrorCodeTokenLimitReached, codeOf(err))
}

// REST mode keeps the legacy scope path and texts (A9: no guard).
func TestCredentialsService_IssueToken_RESTMode(t *testing.T) {
	h := newCredHarness(t)
	h.cfg.RequireMCPAuth = false // REST issues without the guard
	res, err := h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{
		Name: "ci", AllowedServers: []string{"a"}, Permissions: []string{"read", "write"}, Expiry: ExpiryTokenDefault,
		ProfilePin: "ro", Purpose: "nightly",
	})
	require.NoError(t, err)
	assert.WithinDuration(t, h.clock.Add(auth.DefaultTokenExpiry), *res.View.ExpiresAt, time.Second)
	tok, _ := h.sm.GetAgentTokenByName("ci")
	assert.Equal(t, []string{"a"}, tok.AllowedServers)
	assert.False(t, tok.GuardBound, "REST tokens are not standing bindings (A13)")
	assert.Equal(t, "api", tok.Issuer.Surface)

	_, err = h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{Name: "ci2", Permissions: []string{"read", "bogus"}, Expiry: ExpiryTokenDefault})
	require.Error(t, err)
	assert.Equal(t, `invalid permission: "bogus" (valid: read, write, destructive)`, err.Error())
	_, err = h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{Name: "ci2", ExpiresIn: "bogus", Expiry: ExpiryTokenDefault})
	assert.Equal(t, `invalid expiry duration: "bogus"`, err.Error())
	_, err = h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{Name: "ci2", Profile: "ro", ProfilePin: "full", Expiry: ExpiryTokenDefault})
	assert.Equal(t, `"profile" and "profile_pin" name different profiles; send one of them`, err.Error())
	_, err = h.cs.IssueToken(context.Background(), funnelActor(), IssueTokenRequest{Name: "ci", Expiry: ExpiryTokenDefault})
	assert.Equal(t, `A token named "ci" already exists`, err.Error())
}

// T011: IssueClient refusals and success in MCP mode.
func TestCredentialsService_IssueClient_MCPTable(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	_, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w-active", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w-revoked", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Client: "w-revoked"}, false)
	require.NoError(t, err)
	// A regular (grandfathered) token holding client-w-conflict.
	raw, _ := auth.GenerateToken()
	require.NoError(t, h.sm.CreateAgentToken(auth.AgentToken{Name: "client-w-conflict", Kind: auth.KindClient, ClientID: "w-conflict"}, raw, svcTestKey))
	all, _ := h.sm.ListAgentTokens()
	_ = all

	long := mcpClientReq("w2", "ro", "1h")
	long.DisplayName = strings.Repeat("x", 65)
	longPurpose := mcpClientReq("w2", "ro", "1h")
	longPurpose.Purpose = strings.Repeat("x", 501)
	badMode := mcpClientReq("w2", "ro", "1h")
	bm := "open"
	badMode.Mode = &bm
	cases := []struct {
		name  string
		req   IssueClientRequest
		code  string
		state string
	}{
		{"invalid id", mcpClientReq("Bad/ID", "ro", "1h"), profile.CredentialErrorCodeInvalidArgument, ""},
		{"supported id", mcpClientReq("cursor", "ro", "1h"), profile.CredentialErrorCodeReservedIdentity, ""},
		{"exists active", mcpClientReq("w-active", "ro", "1h"), profile.CredentialErrorCodeIdentityExists, "active"},
		{"exists revoked", mcpClientReq("w-revoked", "ro", "1h"), profile.CredentialErrorCodeIdentityExists, "revoked"},
		{"empty profile", mcpClientReq("w2", "", "1h"), profile.CredentialErrorCodeProfileRequired, ""},
		{"unknown profile", mcpClientReq("w2", "nope", "1h"), profile.CredentialErrorCodeUnknownProfile, ""},
		{"no expiry", mcpClientReq("w2", "ro", ""), profile.CredentialErrorCodeMissingArgument, ""},
		{"bad expiry", mcpClientReq("w2", "ro", "106752d"), profile.CredentialErrorCodeInvalidExpiry, ""},
		{"long display", long, profile.CredentialErrorCodeInvalidArgument, ""},
		{"long purpose", longPurpose, profile.CredentialErrorCodeInvalidArgument, ""},
		{"bad mode", badMode, profile.CredentialErrorCodeInvalidArgument, ""},
	}
	mints := h.store.mints.Load()
	before := len(h.listing())
	for _, c := range cases {
		_, err := h.cs.IssueClient(ctx, mcpActor(), c.req)
		require.Error(t, err, c.name)
		assert.Equal(t, c.code, codeOf(err), c.name)
		if c.state != "" {
			var ce *CredentialError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, c.state, ce.State, c.name)
		}
		assert.NotContains(t, err.Error(), "Bad/ID", c.name)
	}
	assert.Equal(t, mints, h.store.mints.Load())
	assert.Len(t, h.listing(), before)

	// Default mode is locked; switchable only when explicit.
	res, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("delegated-worker", "daily-research", "1h"))
	require.NoError(t, err)
	assert.Regexp(t, `^mcp_cli_[0-9a-f]{64}$`, res.Secret)
	assert.Equal(t, "client", res.View.Kind)
	assert.Equal(t, "delegated-worker", res.View.ID)
	assert.Equal(t, "client-delegated-worker", res.View.TokenName)
	assert.Equal(t, "locked", res.View.Binding)
	assert.True(t, res.View.Lease)
	assert.Equal(t, &auth.CredentialIssuer{ActorKind: "api_key", Surface: "mcp"}, res.View.Issuer)
	sw := "switchable"
	r2 := mcpClientReq("w-switch", "ro", "48h")
	r2.Mode = &sw
	res2, err := h.cs.IssueClient(ctx, mcpActor(), r2)
	require.NoError(t, err)
	assert.Equal(t, "switchable", res2.View.Binding)
	assert.False(t, res2.View.Lease, "48h is not a lease")
}

// T014 + T015: no silent defaults; refusals never generate, mint or record.
func TestCredentialsService_NoSilentDefaultsAndAtomicRefusals(t *testing.T) {
	h := newCredHarness(t)
	ctx := context.Background()
	for _, req := range []IssueTokenRequest{
		mcpTokenReq("a1", "", "1h"), mcpTokenReq("a1", "ghost", "1h"),
		func() IssueTokenRequest { r := mcpTokenReq("a1", "", "1h"); r.ProfilePresent = false; return r }(),
	} {
		_, err := h.cs.IssueToken(ctx, mcpActor(), req)
		require.Error(t, err)
	}
	for _, req := range []IssueClientRequest{mcpClientReq("a1", "", "1h"), mcpClientReq("a1", "ghost", "1h")} {
		_, err := h.cs.IssueClient(ctx, mcpActor(), req)
		require.Error(t, err)
	}
	assert.Zero(t, h.store.creates.Load())
	assert.Zero(t, h.store.mints.Load())
	assert.Zero(t, h.generated.Load())
	assert.Empty(t, h.changes(), "a failed attempt writes no profile_change")
	assert.Empty(t, h.eventsOf(EventTypeCredentialsChanged))
}

// T015a: nothing after the commit can fail the call; a failed mint leaves nothing.
func TestCredentialsService_NoFallibleStepAfterCommit(t *testing.T) {
	ctx := context.Background()
	h := newCredHarnessWith(t, false, true)
	res, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w1", "ro", "1h"))
	require.NoError(t, err, "a listing failure after the mint must not fail the issue")
	require.NotEmpty(t, res.Secret)
	assert.Equal(t, "w1", res.View.ID)
	h.store.failListAfter.Store(false)
	tres, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("t1", "ro", "1h"))
	require.NoError(t, err)
	require.NotEmpty(t, tres.Secret)

	// REST add (ClientsService.Add) is on the same path now.
	h.store.failListAfter.Store(false)
	view, secret, err := h.svc.Add(ctx, funnelActor(), AddRequest{ID: "rest-w", Profile: "ro", ExpiresAt: h.clock.Add(time.Hour)})
	require.NoError(t, err)
	require.NotEmpty(t, secret)
	assert.Equal(t, "rest-w", view.ID)

	// A failing mint: no record, no issue record, no event.
	f := newCredHarnessWith(t, true, false)
	_, err = f.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("t1", "ro", "1h"))
	require.Error(t, err)
	_, err = f.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w1", "ro", "1h"))
	require.Error(t, err)
	assert.Empty(t, f.listing())
	assert.Empty(t, f.changes())
	assert.Empty(t, f.eventsOf(EventTypeCredentialsChanged))
}

// T016: concurrent issues of one name serialize: exactly one wins.
func TestCredentialsService_ConcurrentIssueSameName(t *testing.T) {
	h := newCredHarness(t)
	var ok, exists atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.cs.IssueToken(context.Background(), mcpActor(), mcpTokenReq("same-name", "ro", "1h"))
			switch {
			case err == nil:
				ok.Add(1)
			case codeOf(err) == profile.CredentialErrorCodeIdentityExists:
				exists.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(1), ok.Load())
	assert.Equal(t, int64(19), exists.Load())
}

// T017 + T018: one issue record, safe diff, one invalidation event.
func TestCredentialsService_AuditAndEvent(t *testing.T) {
	h := newCredHarness(t)
	ctx := context.Background()
	r := mcpClientReq("w1", "ro", "1h")
	r.Purpose = "SECRET-FREE but private brief text"
	res, err := h.cs.IssueClient(ctx, mcpActor(), r)
	require.NoError(t, err)
	ch := h.changes()
	require.Len(t, ch, 1)
	assert.Equal(t, "issue", ch[0]["change"])
	assert.Equal(t, "api_key", ch[0]["actor_kind"])
	assert.Equal(t, "mcp", ch[0]["surface"])
	assert.Equal(t, "w1", ch[0]["client_id"])
	assert.Equal(t, "client-w1", ch[0]["token_name"])
	diff := ch[0]["diff"].(map[string]interface{})
	for _, k := range []string{"credential_kind", "binding", "expires_at", "lease", "token_prefix", "purpose_set", "via", "mode"} {
		assert.Contains(t, diff, k)
	}
	assert.Equal(t, true, diff["purpose_set"])
	raw, _ := json.Marshal(ch[0])
	assert.NotContains(t, string(raw), res.Secret)
	assert.NotContains(t, string(raw), "private brief")
	rec, _ := h.sm.GetAgentTokenByName("client-w1")
	assert.NotContains(t, string(raw), rec.TokenHash)

	ev := h.eventsOf(EventTypeCredentialsChanged)
	require.Len(t, ev, 1)
	assert.Equal(t, map[string]any{"kind": "client", "id": "w1", "token_name": "client-w1", "change": "issue", "profile": "ro"}, ev[0].Payload)
	assert.Len(t, h.eventsOf(EventTypeClientBindingChanged), 1, "existing listeners still get the binding event")
}

// T022a: the shared service screen refuses secret-shaped values in every
// persisted field, before generating or storing anything.
func TestCredentialsService_InputScreen(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	issuedTok, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("seed-token", "ro", "1h"))
	require.NoError(t, err)
	issuedCli, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("seed-client", "ro", "1h"))
	require.NoError(t, err)
	secrets := map[string]string{
		"agent token": issuedTok.Secret, "client": issuedCli.Secret, "api key": h.cfg.APIKey,
		"aws key": "AKIA" + "IOSFODNN7REALKEY",
	}
	creates, mints, gen := h.store.creates.Load(), h.store.mints.Load(), h.generated.Load()
	nChanges, nEvents := len(h.changes()), len(h.eventsOf(EventTypeCredentialsChanged))
	for label, secret := range secrets {
		for _, field := range []string{"name", "profile", "purpose", "expires_in"} {
			r := mcpTokenReq("t-ok", "ro", "1h")
			switch field {
			case "name":
				r.Name = secret
			case "profile":
				r.Profile = secret
			case "purpose":
				r.Purpose = "brief " + secret
			case "expires_in":
				r.ExpiresIn = secret + "d"
			}
			_, err := h.cs.IssueToken(ctx, mcpActor(), r)
			var se *SecretInputError
			require.ErrorAs(t, err, &se, "%s in %s", label, field)
			assert.Equal(t, field, se.Field(), label)
			assert.NotContains(t, err.Error(), secret)
		}
		for _, field := range []string{"client", "display_name", "profile", "mode", "expires_in", "purpose"} {
			r := mcpClientReq("c-ok", "ro", "1h")
			switch field {
			case "client":
				r.ID = secret
			case "display_name":
				r.DisplayName = secret
			case "profile":
				r.Profile = secret
			case "mode":
				m := secret
				r.Mode = &m
			case "expires_in":
				r.ExpiresIn = secret
			case "purpose":
				r.Purpose = secret
			}
			_, err := h.cs.IssueClient(ctx, mcpActor(), r)
			var se *SecretInputError
			require.ErrorAs(t, err, &se, "%s in %s", label, field)
			assert.Equal(t, field, se.Field(), label)
		}
	}
	assert.Equal(t, creates, h.store.creates.Load())
	assert.Equal(t, mints, h.store.mints.Load())
	assert.Equal(t, gen, h.generated.Load())
	assert.Len(t, h.changes(), nChanges)
	assert.Len(t, h.eventsOf(EventTypeCredentialsChanged), nEvents)
}

// T025 + T026 + T027: revoke semantics for both kinds.
func TestCredentialsService_Revoke(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	_, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("t1", "ro", "1h"))
	require.NoError(t, err)
	res, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "t1"}, false)
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.Equal(t, "revoked", res.View.State)
	require.NotNil(t, res.View.RevokedAt)
	assert.Equal(t, []string{"t1"}, h.notified)
	n := len(h.changes())
	again, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "t1"}, false)
	require.NoError(t, err)
	assert.False(t, again.Changed)
	assert.Len(t, h.changes(), n, "an idempotent revoke writes no record")
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "nope"}, false)
	assert.Equal(t, profile.CredentialErrorCodeIdentityNotFound, codeOf(err))

	// An expired token is revoked (changed=true).
	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("t-exp", "ro", "1m"))
	require.NoError(t, err)
	h.clock = h.clock.Add(time.Hour)
	res, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "t-exp"}, false)
	require.NoError(t, err)
	assert.True(t, res.Changed)
	h.clock = time.Now()

	// Client: revoke record kind, via credentials, config untouched.
	_, err = h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w1", "ro", "1h"))
	require.NoError(t, err)
	cres, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Client: "w1"}, false)
	require.NoError(t, err)
	assert.True(t, cres.Changed)
	assert.True(t, cres.ClientConfigUntouched)
	ch := h.changes()
	last := ch[len(ch)-1]
	assert.Equal(t, "revoke", last["change"])
	assert.Equal(t, "credentials", last["diff"].(map[string]interface{})["via"])
	ev := h.eventsOf(EventTypeCredentialsChanged)
	assert.Equal(t, "revoke", ev[len(ev)-1].Payload["change"])

	// A supported client minted by connect with a held claim: revoke wins (A19).
	sec := h.mint("cursor", "ro", nil)
	require.True(t, h.authenticates(sec))
	m := h.svc.ConnectMinter()
	prof := "ro"
	intent := connect.CredentialIntent{Profile: &prof, ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue("cursor", intent)
	require.NoError(t, err)
	sres, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Client: "cursor"}, false)
	require.NoError(t, err, "a held connect claim never blocks a revoke")
	assert.True(t, sres.Changed)
	assert.True(t, sres.ClientConfigUntouched)
	var sup *CredentialSupersededError
	require.ErrorAs(t, m.Commit("cursor", intent, issued), &sup)
	assert.False(t, h.authenticates(sec))
	assert.False(t, h.authenticates(issued.Secret))

	// The Clients page Forget still writes `forget` and now publishes the event.
	_, err = h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w2", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.svc.Forget(ctx, funnelActor(), "w2", false)
	require.NoError(t, err)
	ch = h.changes()
	assert.Equal(t, "forget", ch[len(ch)-1]["change"])
	ev = h.eventsOf(EventTypeCredentialsChanged)
	assert.Equal(t, "forget", ev[len(ev)-1].Payload["change"])
}

// T028: list/get views, precedence, lease boundary, dangling, no secret fields.
func TestCredentialsService_ListGetViews(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	_, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("b-token", "ro", "24h"))
	require.NoError(t, err)
	_, err = h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("a-client", "full", "86401s"))
	require.NoError(t, err)
	views, err := h.cs.List(CredentialFilter{})
	require.NoError(t, err)
	require.Len(t, views, 2)
	assert.Equal(t, "client", views[0].Kind)
	assert.Equal(t, "token", views[1].Kind)
	assert.False(t, views[0].Lease, "24h + 1s is not a lease")
	assert.True(t, views[1].Lease, "exactly 24h is a lease")

	only, err := h.cs.List(CredentialFilter{Kind: "token"})
	require.NoError(t, err)
	assert.Len(t, only, 1)
	byProfile, _ := h.cs.List(CredentialFilter{Profile: "full"})
	assert.Len(t, byProfile, 1)
	noClients, _ := h.cs.List(CredentialFilter{ExcludeClients: true})
	assert.Len(t, noClients, 1)

	// Revoked takes precedence over expired.
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "b-token"}, false)
	require.NoError(t, err)
	h.clock = h.clock.Add(48 * time.Hour)
	v, err := h.cs.Get(CredentialRef{Token: "b-token"})
	require.NoError(t, err)
	assert.Equal(t, "revoked", v.State)
	cv, err := h.cs.Get(CredentialRef{Client: "a-client"})
	require.NoError(t, err)
	assert.Equal(t, "expired", cv.State)

	// Dangling after the profile disappears.
	h.cfg.Profiles = h.cfg.Profiles[:1] // drop "full"
	cv, _ = h.cs.Get(CredentialRef{Client: "a-client"})
	assert.Equal(t, "dangling", cv.ProfileState)

	_, err = h.cs.Get(CredentialRef{Client: "nope"})
	assert.Equal(t, profile.CredentialErrorCodeIdentityNotFound, codeOf(err))
	_, err = h.cs.Get(CredentialRef{Client: "a", Token: "b"})
	assert.Equal(t, profile.CredentialErrorCodeInvalidArgument, codeOf(err))

	// Reflection: no secret-bearing field in the view.
	typ := reflect.TypeOf(CredentialView{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		low := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
		for _, bad := range []string{"hash", "pending", "secret", "user_id", "prior"} {
			assert.NotContains(t, low, bad, f.Name)
		}
	}
}

// T028a: the tool addresses the ownerless token namespace only.
func TestCredentialsService_OwnerlessNamespace(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	mk := func(name, owner string) string {
		raw, _ := auth.GenerateToken()
		require.NoError(t, h.sm.CreateAgentToken(auth.AgentToken{Name: name, UserID: owner, AllowedServers: []string{"*"}, Permissions: []string{"read"}, ExpiresAt: time.Now().Add(time.Hour)}, raw, svcTestKey))
		return raw
	}
	u1 := mk("shared", "u1")
	u2 := mk("shared", "u2")
	mk("shared", "")
	mk("tenant-only", "u1")

	views, err := h.cs.List(CredentialFilter{})
	require.NoError(t, err)
	require.Len(t, views, 1, "owned tokens are never listed")
	assert.Equal(t, "shared", views[0].ID)

	_, err = h.cs.Get(CredentialRef{Token: "tenant-only"})
	notFound := err
	_, err2 := h.cs.Get(CredentialRef{Token: "missing-name"})
	assert.Equal(t, profile.CredentialErrorCodeIdentityNotFound, codeOf(notFound))
	assert.Equal(t, strings.Replace(err2.Error(), "missing-name", "tenant-only", 1), notFound.Error(), "same text as a missing name")

	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("tenant-only", "ro", "1h"))
	assert.NoError(t, err, "a tenant name is not a conflict")
	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("shared", "ro", "1h"))
	assert.Equal(t, profile.CredentialErrorCodeIdentityExists, codeOf(err))

	res, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "shared"}, false)
	require.NoError(t, err)
	assert.True(t, res.Changed)
	for _, raw := range []string{u1, u2} {
		_, err := h.sm.ValidateAgentToken(raw, svcTestKey)
		assert.NoError(t, err, "tenant tokens are untouched")
	}
}

// Spec 115 UI-005: a lease client gets no expiring-soon/reconnect warning; a
// long-lived one close to expiry still does.
func TestCredentialsService_LeaseHasNoExpiringWarning(t *testing.T) {
	h := newCredHarness(t)
	ctx := context.Background()
	_, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("lease-w", "ro", "30m"))
	require.NoError(t, err)
	_, err = h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("long-w", "ro", "25h"))
	require.NoError(t, err)
	h.clock = h.clock.Add(10 * time.Minute)
	expiring := map[string]bool{}
	for _, w := range h.svc.Warnings(nil) {
		if w.Code == profile.WarningClientCredentialExpiring {
			expiring[w.ClientID] = true
		}
	}
	assert.False(t, expiring["lease-w"])
	assert.True(t, expiring["long-w"])
}

// Review r1: client lifecycle records carry `mode` on issue and revoke, token
// records `pin_source`; a malformed token reference is refused without echo.
func TestCredentialsService_LifecycleDiffKindsAndTokenRefSyntax(t *testing.T) {
	h := newCredHarness(t)
	ctx := context.Background()
	_, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("w1", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Client: "w1"}, false)
	require.NoError(t, err)
	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("t1", "ro", "1h"))
	require.NoError(t, err)
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "t1"}, false)
	require.NoError(t, err)
	for _, c := range h.changes() {
		diff := c["diff"].(map[string]interface{})
		if c["client_id"] != "" {
			assert.Equal(t, "locked", diff["mode"], "%v", c["change"])
		} else {
			assert.Equal(t, "token_pin", diff["pin_source"], "%v", c["change"])
		}
	}
	for _, bad := range []string{"bad name!", "a/b", strings.Repeat("x", 65)} {
		_, err := h.cs.Get(CredentialRef{Token: bad})
		assert.Equal(t, profile.CredentialErrorCodeInvalidArgument, codeOf(err))
		assert.NotContains(t, err.Error(), bad)
		_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: bad}, false)
		assert.Equal(t, profile.CredentialErrorCodeInvalidArgument, codeOf(err))
		assert.NotContains(t, err.Error(), bad)
	}
}

// Review r2: the MCP write gates are re-checked under bindingWriteMu, so a
// mutation queued behind a config write that turns a gate on is refused.
func TestCredentialsService_WriteGatesRecheckedUnderMutex(t *testing.T) {
	h := newCredHarness(t)
	ctx := context.Background()
	_, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("pre", "ro", "1h"))
	require.NoError(t, err)
	h.svc.mu.Lock() // a config write holds the mutex...
	done := make(chan error, 3)
	go func() {
		r := mcpTokenReq("queued", "ro", "1h")
		r.EnforceWriteGates = true
		_, err := h.cs.IssueToken(ctx, mcpActor(), r)
		done <- err
	}()
	go func() {
		r := mcpClientReq("queued-c", "ro", "1h")
		r.EnforceWriteGates = true
		_, err := h.cs.IssueClient(ctx, mcpActor(), r)
		done <- err
	}()
	go func() {
		_, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "pre", EnforceWriteGates: true}, false)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	h.cfg.ReadOnlyMode = true // ...and turns read_only_mode on before releasing it
	h.svc.mu.Unlock()
	for i := 0; i < 3; i++ {
		assert.Equal(t, profile.CredentialErrorCodeReadOnlyMode, codeOf(<-done))
	}
	tok, _ := h.sm.GetAgentTokenByName("pre")
	assert.False(t, tok.Revoked)
	q, _ := h.sm.GetAgentTokenByName("queued")
	assert.Nil(t, q)
}

// Review r3: the service screen runs before the write-gate recheck, so a value
// the live config flags is reported (and never recorded) even under a gate.
func TestCredentialsService_ScreenPrecedesWriteGate(t *testing.T) {
	h := newCredHarness(t)
	h.cfg.ReadOnlyMode = true
	r := mcpTokenReq("x", "ro", "1h")
	r.EnforceWriteGates, r.Purpose = true, "k="+h.cfg.APIKey
	_, err := h.cs.IssueToken(context.Background(), mcpActor(), r)
	assert.Equal(t, profile.CredentialErrorCodeSecretInArgument, codeOf(err))
	c := mcpClientReq("x", "ro", "1h")
	c.EnforceWriteGates, c.Purpose = true, "k="+h.cfg.APIKey
	_, err = h.cs.IssueClient(context.Background(), mcpActor(), c)
	assert.Equal(t, profile.CredentialErrorCodeSecretInArgument, codeOf(err))
	r.Purpose = "plain"
	_, err = h.cs.IssueToken(context.Background(), mcpActor(), r)
	assert.Equal(t, profile.CredentialErrorCodeReadOnlyMode, codeOf(err))
}

// Review code-r1 (A24 revised): a configured API key of ANY nonempty length is
// screened. Keys of 1-7 characters used to skip the comparison, so a short key
// pasted into issuance metadata was persisted and echoed. The 8-character key
// is the boundary control.
func TestCredentialsService_InputScreen_ShortAPIKey(t *testing.T) {
	ctx := context.Background()
	const base = "Q7Z2q9XW" // no character of it occurs in the other request fields
	for n := 1; n <= len(base); n++ {
		key := base[:n]
		h := newCredHarness(t)
		h.cfg.APIKey = key
		creates, mints, gen := h.store.creates.Load(), h.store.mints.Load(), h.generated.Load()
		nChanges := len(h.changes())
		for _, purpose := range []string{key, "brief " + key + " end"} {
			r := mcpTokenReq("t-ok", "ro", "1h")
			r.Purpose = purpose
			_, err := h.cs.IssueToken(ctx, mcpActor(), r)
			var se *SecretInputError
			require.ErrorAs(t, err, &se, "token purpose with a %d-char API key", n)
			assert.Equal(t, []string{"purpose"}, se.Fields)
			assert.NotContains(t, err.Error(), key)

			c := mcpClientReq("c-ok", "ro", "1h")
			c.DisplayName = purpose
			_, err = h.cs.IssueClient(ctx, mcpActor(), c)
			require.ErrorAs(t, err, &se, "client display_name with a %d-char API key", n)
			assert.Equal(t, []string{"display_name"}, se.Fields)
		}
		assert.Equal(t, creates, h.store.creates.Load(), "no token stored (key len %d)", n)
		assert.Equal(t, mints, h.store.mints.Load(), "no client minted (key len %d)", n)
		assert.Equal(t, gen, h.generated.Load(), "no secret generated (key len %d)", n)
		assert.Len(t, h.changes(), nChanges)
		for _, tok := range h.listing() {
			raw, _ := json.Marshal(tok)
			assert.NotContains(t, string(raw), "brief "+key, "no stored metadata carries the key")
		}
	}
}
