package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108 FR-029 (T057): every dispatched call's activity record carries the
// profile, client and token IN EFFECT when it ran. One row per FR-020 source.

// waitToolCallFor waits for the persisted tool_call record of one dispatch,
// identified by its transport session id.
func waitToolCallFor(t *testing.T, f *restV3Fixture, sessionID, server, tool string) *storage.ActivityRecord {
	t.Helper()
	var found *storage.ActivityRecord
	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.Type == storage.ActivityTypeToolCall && r.SessionID == sessionID && r.ServerName == server && r.ToolName == tool {
				found = r
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "tool_call record for %s %s:%s", sessionID, server, tool)
	return found
}

func registerAttributionSession(f *restV3Fixture, sid, clientName, tokenName, clientID string) {
	f.proxy.sessionStore.SetSession(sid, clientName, "1.0", false, false, nil)
	f.proxy.sessionStore.SetSessionIdentity(sid, tokenName, clientID)
}

func TestActivityAttribution_EverySourceStampsTheRecord(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)

	cases := []struct {
		name                                     string
		ctx                                      func() context.Context
		sid                                      string
		clientName, tokenName, clientID          string
		wantProfile, wantSource, wantClient, tok string
	}{
		{
			name: "pin (locked client credential)", sid: "s-pin",
			ctx:        func() context.Context { return sessionCtx(clientCtx("cursor", "work-readonly", "locked"), "s-pin") },
			clientName: "Cursor", tokenName: "client-cursor", clientID: "cursor",
			wantProfile: "work-readonly", wantSource: string(profile.SourcePin), wantClient: "cursor", tok: "client-cursor",
		},
		{
			name: "binding (switchable client credential)", sid: "s-bind",
			ctx: func() context.Context {
				return sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), "s-bind")
			},
			clientName: "Zed", tokenName: "client-laptop", clientID: "laptop",
			wantProfile: "work-readonly", wantSource: string(profile.SourceBinding), wantClient: "laptop", tok: "client-laptop",
		},
		{
			name: "url (/mcp/p/<slug> with the API key)", sid: "s-url",
			ctx: func() context.Context {
				return sessionCtx(urlProfileCtx(f.proxy, "work-readonly"), "s-url")
			},
			clientName:  "Claude Code",
			wantProfile: "work-readonly", wantSource: string(profile.SourceURL),
		},
		{
			name: "session (set_profile)", sid: "s-sess",
			ctx: func() context.Context {
				return sessionProfileCtx(t, f.proxy, "s-sess", "work-readonly")
			},
			clientName:  "Claude Code",
			wantProfile: "work-readonly", wantSource: string(profile.SourceSession),
		},
		{
			name: "none (API key, no profile)", sid: "s-none",
			ctx:        func() context.Context { return sessionCtx(adminCtx(), "s-none") },
			clientName: "curl",
			wantSource: string(profile.SourceNone),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registerAttributionSession(f, tc.sid, tc.clientName, tc.tokenName, tc.clientID)
			result, err := f.proxy.handleCallToolVariant(tc.ctx(), auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
			require.NoError(t, err)
			require.False(t, result.IsError, resultText(t, result))

			rec := waitToolCallFor(t, f, tc.sid, "github", "list_issues")
			assert.Equal(t, tc.wantProfile, rec.Profile)
			assert.Equal(t, tc.wantSource, rec.ProfileSource)
			assert.Equal(t, tc.wantClient, rec.ClientID)
			assert.Equal(t, tc.tok, rec.TokenName)
			assert.Equal(t, tc.clientName, rec.ClientName)
			if tc.wantProfile != "" {
				assert.Equal(t, tc.wantProfile, rec.Metadata["profile"], "metadata.profile is still written (Spec 057 SC-003)")
			}
		})
	}
}

func TestActivityAttribution_AnonymousSource(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.AnonymousProfile = "work-readonly" })
	registerAttributionSession(f, "s-anon", "Unknown", "", "")
	result, err := f.proxy.handleCallToolVariant(sessionCtx(anonCtx(), "s-anon"), auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))

	rec := waitToolCallFor(t, f, "s-anon", "github", "list_issues")
	assert.Equal(t, "work-readonly", rec.Profile)
	assert.Equal(t, string(profile.SourceAnonymous), rec.ProfileSource)
	assert.Empty(t, rec.ClientID)
	assert.Empty(t, rec.TokenName)
}

func TestActivityAttribution_BlockedCallHasBlockReasonFieldAndMetadata(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	registerAttributionSession(f, "s-blk", "Cursor", "client-cursor", "cursor")
	ctx := sessionCtx(clientCtx("cursor", "work-readonly", "locked"), "s-blk")
	result, err := f.proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, result.IsError)

	rec := f.waitBlocked("github", "create_issue", profile.BlockReasonTier)
	assert.Equal(t, string(profile.BlockReasonTier), rec.BlockReason, "first-class field")
	assert.Equal(t, string(profile.BlockReasonTier), rec.Metadata[storage.MetadataKeyBlockReason], "and the metadata key")
	assert.Equal(t, "work-readonly", rec.Profile)
	assert.Equal(t, string(profile.SourcePin), rec.ProfileSource)
	assert.Equal(t, "cursor", rec.ClientID)
	assert.Equal(t, "client-cursor", rec.TokenName)
	assert.Equal(t, "Cursor", rec.ClientName)
}

// An internal_tool_call record has no request context at its funnel; it reads
// the session's latest resolution, which the same request has just written.
func TestActivityAttribution_InternalToolCallReadsSessionResolution(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	registerAttributionSession(f, "s-int", "Cursor", "client-cursor", "cursor")
	ctx := sessionCtx(clientCtx("cursor", "work-readonly", "locked"), "s-int")
	result, err := f.proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.Type == storage.ActivityTypeInternalToolCall && r.SessionID == "s-int" {
				return r.Profile == "work-readonly" && r.ProfileSource == string(profile.SourcePin) &&
					r.ClientID == "cursor" && r.TokenName == "client-cursor"
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "internal_tool_call record carries the session's attribution")
}

// No history rewrite (FR-029, US3-5): reassigning the client later does not
// touch the record the earlier call wrote.
func TestActivityAttribution_ReassignmentDoesNotRewriteHistory(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	registerAttributionSession(f, "s-hist", "Cursor", "client-cursor", "cursor")
	result, err := f.proxy.handleCallToolVariant(sessionCtx(clientCtx("cursor", "work-readonly", "locked"), "s-hist"),
		auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError)
	first := waitToolCallFor(t, f, "s-hist", "github", "list_issues")
	require.Equal(t, "work-readonly", first.Profile)

	// Reassign: the same client is now bound to work-full and calls again.
	result, err = f.proxy.handleCallToolVariant(sessionCtx(clientCtx("cursor", "work-full", "locked"), "s-hist"),
		auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Eventually(t, func() bool {
		var ro, full int
		for _, r := range f.activities() {
			if r.Type != storage.ActivityTypeToolCall || r.SessionID != "s-hist" {
				continue
			}
			switch r.Profile {
			case "work-readonly":
				ro++
			case "work-full":
				full++
			}
		}
		return ro == 1 && full == 1
	}, 5*time.Second, 10*time.Millisecond, "the earlier record still says work-readonly")
}

// A code_execution sub-call is a dispatch of its own; it carries the script
// caller's attribution (the parent's context).
func TestActivityAttribution_CodeExecutionSubCallCarriesCallerAttribution(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.EnableCodeExecution = true })
	registerAttributionSession(f, "s-code", "Cursor", "client-cursor", "cursor")
	ctx := sessionCtx(clientCtx("cursor", "work-full", "locked"), "s-code")

	result, err := f.proxy.handleCodeExecution(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{
			"code": `call_tool("github", "list_issues", {})`,
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.Type == storage.ActivityTypeToolCall && r.ParentID != "" && r.ToolName == "list_issues" {
				return r.Profile == "work-full" && r.ProfileSource == string(profile.SourcePin) &&
					r.ClientID == "cursor" && r.TokenName == "client-cursor"
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "sub-call carries the caller's attribution")
}

// REST POST /api/v1/tools/call with a legacy pinned agent token: token_name and
// the pin are recorded; there is no client id (it is not a client credential).
func TestActivityAttribution_RESTLegacyPinnedTokenRecordsTokenAndPin(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("ci-bot", "work-readonly")

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var found *storage.ActivityRecord
	require.Eventually(t, func() bool {
		for _, r := range f.activities() {
			if r.Type == storage.ActivityTypeToolCall && r.ServerName == "github" && r.ToolName == "list_issues" && r.TokenName == "ci-bot" {
				found = r
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "work-readonly", found.Profile)
	assert.Equal(t, string(profile.SourcePin), found.ProfileSource)
	assert.Empty(t, found.ClientID)
}

// Regression guard on the context plumbing itself.
func TestActivityAttribution_ContextValueWinsOverSessionForProfile(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.proxy.sessionStore.SetSession("s-ctx", "Cursor", "1.0", false, false, nil)
	f.proxy.sessionStore.UpdateSessionProfile("s-ctx", "stale", "url")

	ctx := sessionCtx(auth.WithAuthContext(context.Background(), auth.AdminContext()), "s-ctx")
	// No installed attribution: the session's latest resolution is used.
	attr := f.proxy.activityAttribution(ctx, "")
	assert.Equal(t, "stale", attr.Profile)
	assert.Equal(t, "Cursor", attr.ClientName)

	// An installed attribution (the dispatch's own resolution) wins.
	ctx2, res := f.proxy.resolveForDispatch(ctx, f.proxy.profileIndexCurrent(ctx))
	attr = f.proxy.activityAttribution(ctx2, "")
	assert.Equal(t, res.Name, attr.Profile)
	assert.Equal(t, res.Source, attr.ProfileSource)
	assert.NotEqual(t, "stale", attr.Profile)
}
