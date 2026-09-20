package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

func promptNamesForTest(prompts []mcp.Prompt) []string {
	names := make([]string, 0, len(prompts))
	for _, p := range prompts {
		names = append(names, p.Name)
	}
	return names
}

// TestFilterAggregatedPromptsForAuth is the PR #973 finding F1 regression: the
// aggregated-prompt path had no scope enforcement, so a scoped agent token or a
// profile-pinned session could list and fetch every server's prompts. The
// filter must drop out-of-scope upstream prompts while always keeping built-ins.
func TestFilterAggregatedPromptsForAuth(t *testing.T) {
	const (
		builtinSetup = "setup-new-mcp-server"
		builtinTrbl  = "troubleshoot-mcp-server"
	)
	githubPrompt := FormatDirectPromptName("github", "pr_review")
	gitlabPrompt := FormatDirectPromptName("gitlab", "mr_review")

	// Full aggregated set every case starts from: 2 built-ins + 2 upstream,
	// the upstream ones stamped exactly as buildAggregatedServerPrompts
	// publishes them.
	base := func() []mcp.Prompt {
		return []mcp.Prompt{
			{Name: builtinSetup},
			{Name: builtinTrbl},
			aggregatedPromptForTest("github", "pr_review"),
			aggregatedPromptForTest("gitlab", "mr_review"),
		}
	}

	agentCtx := func(servers ...string) context.Context {
		return auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type:           auth.AuthTypeAgent,
			AgentName:      "scoped-bot",
			AllowedServers: servers,
			Permissions:    []string{auth.PermRead},
		})
	}
	profileCtx := func(servers ...string) context.Context {
		return profile.WithProfileScope(
			context.Background(),
			profile.NewProfileScope("dev", servers),
		)
	}

	tests := []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{
			name: "no auth and no profile leaves everything",
			ctx:  context.Background(),
			want: []string{builtinSetup, builtinTrbl, githubPrompt, gitlabPrompt},
		},
		{
			name: "admin sees everything",
			ctx:  auth.WithAuthContext(context.Background(), auth.AdminContext()),
			want: []string{builtinSetup, builtinTrbl, githubPrompt, gitlabPrompt},
		},
		{
			name: "scoped agent token sees only its server's prompts plus built-ins",
			ctx:  agentCtx("github"),
			want: []string{builtinSetup, builtinTrbl, githubPrompt},
		},
		{
			name: "wildcard agent token sees everything",
			ctx:  agentCtx("*"),
			want: []string{builtinSetup, builtinTrbl, githubPrompt, gitlabPrompt},
		},
		{
			name: "profile-scoped session sees only in-profile prompts plus built-ins",
			ctx:  profileCtx("gitlab"),
			want: []string{builtinSetup, builtinTrbl, gitlabPrompt},
		},
		{
			name: "empty profile still keeps built-ins, drops all upstream",
			ctx:  profileCtx(),
			want: []string{builtinSetup, builtinTrbl},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proxy := &MCPProxyServer{}
			got := proxy.filterAggregatedPromptsForAuth(tc.ctx, base())
			assert.ElementsMatch(t, tc.want, promptNamesForTest(got))
			for _, pr := range got {
				_, stamped := aggregatedPromptServer(pr)
				assert.False(t, stamped, "internal owner stamp must be stripped from %q for every caller", pr.Name)
			}
		})
	}
}

// aggregatedPromptForTest builds an upstream prompt exactly as
// buildAggregatedServerPrompts publishes it: "__" display name plus the
// canonical-owner _meta stamp.
func aggregatedPromptForTest(server, prompt string) mcp.Prompt {
	return mcp.Prompt{
		Name: FormatDirectPromptName(server, prompt),
		Meta: stampAggregatedPromptServer(nil, server),
	}
}

// TestFilterAggregatedPromptsForAuth_UnstampedFailsClosed (Spec 104 FR-016g;
// Spec 105 FR-006/FR-008 FR008-G7): an upstream prompt with no canonical-owner
// stamp cannot have come from buildAggregatedServerPrompts, so the filter must
// not guess its owner from the display name (that re-parse is the original
// leak). It has no registration identity at all, so it is dropped for EVERY
// caller — scoped, unrestricted agent, and administrator alike (SC-005
// exception) — never only for a scoped one. Withholding it used to be
// conditioned on `enforce` (scoped agent or active profile), which left it
// visible to an unscoped or administrator caller; that condition is gone.
func TestFilterAggregatedPromptsForAuth_UnstampedFailsClosed(t *testing.T) {
	proxy := &MCPProxyServer{}
	unstamped := mcp.Prompt{Name: FormatDirectPromptName("github", "looks_in_scope")}
	stamped := aggregatedPromptForTest("github", "x")
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "scoped-bot",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})
	unrestricted := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "unrestricted-bot",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	for name, ctx := range map[string]context.Context{
		"scoped agent":       scoped,
		"unrestricted agent": unrestricted,
		"administrator":      context.Background(),
	} {
		got := proxy.filterAggregatedPromptsForAuth(ctx, []mcp.Prompt{unstamped, stamped})
		assert.ElementsMatchf(t, []string{stamped.Name}, promptNamesForTest(got),
			"%s: an unstamped prompt has no registration identity and is withheld from every caller", name)
	}
}

// TestStripAggregatedPromptServer_PreservesUpstreamMeta verifies the stamp is
// removed without disturbing upstream-supplied _meta and without mutating the
// registered prompt (mcp-go hands filters a shared Meta pointer), and that the
// client-visible _meta is byte-identical to what the upstream sent — nil stays
// absent, an empty `{}` stays `{}`, a progress token survives, and even an
// upstream value under our own key comes back (Spec 105 SC-005: administrator
// output must not change for prompts the feature does not withhold).
func TestStripAggregatedPromptServer_PreservesUpstreamMeta(t *testing.T) {
	wire := func(pr mcp.Prompt) string {
		t.Helper()
		b, err := json.Marshal(pr)
		require.NoError(t, err)
		return string(b)
	}

	cases := map[string]*mcp.Meta{
		"nil":            nil,
		"empty":          {AdditionalFields: map[string]any{}},
		"fields":         {AdditionalFields: map[string]any{"vendor/x": "keep"}},
		"progress token": {ProgressToken: "tok-1", AdditionalFields: map[string]any{}},
		"our key":        {AdditionalFields: map[string]any{aggregatedPromptServerMetaKey: "upstream-claims-this"}},
	}
	for name, upstream := range cases {
		t.Run(name, func(t *testing.T) {
			asSent := mcp.Prompt{Name: "srv__p", Meta: upstream}
			registered := mcp.Prompt{Name: "srv__p", Meta: stampAggregatedPromptServer(upstream, "srv")}

			owner, ok := aggregatedPromptServer(registered)
			require.True(t, ok)
			assert.Equal(t, "srv", owner, "the owner is what mcpproxy dispatches to, never an upstream claim")

			stripped := stripAggregatedPromptServer(registered)
			assert.Equal(t, wire(asSent), wire(stripped), "client-visible _meta must be exactly what the upstream sent")
			_, stillStamped := aggregatedPromptServer(registered)
			assert.True(t, stillStamped, "the registered prompt must not be mutated by stripping a copy")
			_, leaked := aggregatedPromptServer(stripped)
			assert.False(t, leaked)
		})
	}

	// An upstream that sends a bare string under our key is not a stamp: the
	// filter must not trust it as an owner.
	forged := mcp.Prompt{Name: "srv__p", Meta: &mcp.Meta{AdditionalFields: map[string]any{aggregatedPromptServerMetaKey: "srv"}}}
	_, ok := aggregatedPromptServer(forged)
	assert.False(t, ok, "an upstream-supplied string under the stamp key is not a registration identity")
	assert.Equal(t, forged, stripAggregatedPromptServer(forged), "an unstamped prompt is returned untouched")
}

// TestAggregatedPrompt_ScopeUsesCanonicalOwner is the Spec 104 FR-016g
// regression (cross-model review): with servers "a" and "a__b" both serving a
// prompt, "a__b"'s prompt is published as "a__b__greeting". Re-parsing that
// display name on the first "__" yields owner "a", so an agent token scoped to
// "a" alone could LIST and GET a prompt the handler dispatches to "a__b". The
// filter must authorize against the canonical owner recorded at publication —
// the same one the handler dispatches to — so both list and get are denied.
func TestAggregatedPrompt_ScopeUsesCanonicalOwner(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, _ := createTestProxyWithRuntime(t, nil)
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true
	qOff := false
	proxy.config.QuarantineEnabled = &qOff

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	for _, name := range []string{"a", "a__b"} {
		testServer := servertest.NewTestStreamableHTTPServer(newTestRefreshPromptsUpstream(t))
		t.Cleanup(testServer.Close)
		require.NoError(t, um.AddServerConfig(name, &config.ServerConfig{
			Name: name, Protocol: "streamable-http", URL: testServer.URL, Enabled: true,
		}))
		client, ok := um.GetClient(name)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, client.Connect(ctx))
		cancel()
	}
	proxy.upstreamManager = um
	proxy.RefreshPrompts()

	registered := proxy.server.ListPrompts()
	require.Contains(t, registered, "a__greeting")
	require.Contains(t, registered, "a__b__greeting", "precondition: the collision-prone prompt is published")

	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead},
	})
	handle := func(id int, method, params string) map[string]interface{} {
		t.Helper()
		raw := []byte(`{"jsonrpc":"2.0","id":` + fmt.Sprint(id) + `,"method":"` + method + `","params":` + params + `}`)
		encoded, err := json.Marshal(proxy.server.HandleMessage(ctx, raw))
		require.NoError(t, err)
		var envelope map[string]interface{}
		require.NoError(t, json.Unmarshal(encoded, &envelope))
		return envelope
	}
	require.NotNil(t, proxy.server.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)))

	// prompts/list: only server "a"'s prompt (plus built-ins) is visible, and
	// the internal owner stamp never reaches the wire.
	list := handle(2, "prompts/list", `{}`)
	require.Nil(t, list["error"], "prompts/list must succeed: %v", list)
	var listed []string
	for _, pr := range list["result"].(map[string]interface{})["prompts"].([]interface{}) {
		entry := pr.(map[string]interface{})
		listed = append(listed, entry["name"].(string))
		assert.NotContains(t, entry, "_meta", "owner stamp must be stripped from prompts/list output: %v", entry)
	}
	assert.Contains(t, listed, "a__greeting")
	assert.NotContains(t, listed, "a__b__greeting", "a token scoped to server 'a' must not see a__b's prompt")

	// prompts/get: in-scope works, out-of-scope is denied.
	okGet := handle(3, "prompts/get", `{"name":"a__greeting"}`)
	assert.Nil(t, okGet["error"], "in-scope prompts/get must succeed: %v", okGet)
	denied := handle(4, "prompts/get", `{"name":"a__b__greeting"}`)
	require.NotNil(t, denied["error"], "a token scoped to server 'a' must not fetch a__b's prompt: %v", denied)

	// Non-disclosing refusal (FR-010): the hidden prompt's error must have the
	// same code and the same message (modulo the caller's own echoed name) as
	// a prompt that does not exist at all.
	absent := handle(6, "prompts/get", `{"name":"a__nonexistent"}`)
	require.NotNil(t, absent["error"], "precondition: %v", absent)
	deniedErr := denied["error"].(map[string]interface{})
	absentErr := absent["error"].(map[string]interface{})
	assert.Equal(t, absentErr["code"], deniedErr["code"], "hidden vs absent must share the JSON-RPC error code")
	assert.Equal(t,
		strings.ReplaceAll(absentErr["message"].(string), "a__nonexistent", "<name>"),
		strings.ReplaceAll(deniedErr["message"].(string), "a__b__greeting", "<name>"),
		"hidden vs absent must share the error message")

	// The handler-side gate is wired in production, not only in the unit test
	// with a fake hook: invoking the REGISTERED handler directly (bypassing
	// mcp-go's filter) still refuses the scoped caller with the not-found
	// sentinel and never contacts the upstream, while an admin gets through.
	hiddenHandler := registered["a__b__greeting"].Handler
	_, err := hiddenHandler(ctx, mcp.GetPromptRequest{Params: mcp.GetPromptParams{Name: "a__b__greeting"}})
	require.ErrorIs(t, err, errPromptNotFound, "production RefreshPrompts must wire authorizeAggregatedPromptServer into every upstream handler")
	assert.Equal(t, "prompt 'a__b__greeting' not found: prompt not found", err.Error())
	res, err := hiddenHandler(auth.WithAuthContext(context.Background(), auth.AdminContext()),
		mcp.GetPromptRequest{Params: mcp.GetPromptParams{Name: "a__b__greeting"}})
	require.NoError(t, err)
	require.NotNil(t, res)

	// The same session as an admin sees everything, with no stamp on the wire.
	ctx = auth.WithAuthContext(context.Background(), auth.AdminContext())
	adminList := handle(5, "prompts/list", `{}`)
	require.Nil(t, adminList["error"])
	var adminNames []string
	for _, pr := range adminList["result"].(map[string]interface{})["prompts"].([]interface{}) {
		entry := pr.(map[string]interface{})
		adminNames = append(adminNames, entry["name"].(string))
		assert.NotContains(t, entry, "_meta", "owner stamp must be stripped for admins too: %v", entry)
	}
	assert.Subset(t, adminNames, []string{"a__greeting", "a__b__greeting"})
}

// TestAggregatedPrompt_LateEnableStillFiltered (Spec 105 FR-006, cross-review
// round 2): the prompt filter used to be installed only when enable_prompts
// was true at CONSTRUCTION, while RefreshPrompts publishes from the LIVE
// snapshot on every servers.changed / config.reloaded / prompts-changed event.
// Boot with prompts off, flip enable_prompts + aggregate_upstream_prompts at
// runtime, and every routing-mode server received the upstream prompts with
// no scope filter at all — and, since the filter is also what strips the
// internal owner stamp, the wire carried `"_meta":{"app.mcpproxy/server":{}}`
// for every caller. The filter must be bound to every server unconditionally
// so late-published prompts are scoped and stamp-free exactly like boot-time
// ones.
func TestAggregatedPrompt_LateEnableStillFiltered(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	proxy, _ := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.EnablePrompts = false
		cfg.AggregateUpstreamPrompts = false
	})
	require.Empty(t, proxy.server.ListPrompts(), "precondition: nothing registered while prompts are disabled")

	// Hot-reload: both flags flip in the live snapshot the refresh reads.
	proxy.config.EnablePrompts = true
	proxy.config.AggregateUpstreamPrompts = true
	qOff := false
	proxy.config.QuarantineEnabled = &qOff

	um := upstream.NewManager(zap.NewNop(), proxy.config, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { um.DisconnectAll() })
	for _, name := range []string{"a", "hidden"} {
		testServer := servertest.NewTestStreamableHTTPServer(newTestRefreshPromptsUpstream(t))
		t.Cleanup(testServer.Close)
		require.NoError(t, um.AddServerConfig(name, &config.ServerConfig{
			Name: name, Protocol: "streamable-http", URL: testServer.URL, Enabled: true,
		}))
		client, ok := um.GetClient(name)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, client.Connect(ctx))
		cancel()
	}
	proxy.upstreamManager = um
	proxy.RefreshPrompts()
	require.Contains(t, proxy.server.ListPrompts(), "hidden__greeting", "precondition: the late-enabled prompts were published")

	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead},
	})
	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())

	servers := map[string]*mcpserver.MCPServer{
		"retrieve":  proxy.server,
		"direct":    proxy.directServer,
		"code-exec": proxy.codeExecServer,
		"call-tool": proxy.callToolServer,
	}
	for label, srv := range servers {
		require.NotNil(t, srv, label)
		t.Run(label, func(t *testing.T) {
			list := func(ctx context.Context) []map[string]interface{} {
				t.Helper()
				require.NotNil(t, srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)))
				encoded, err := json.Marshal(srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{}}`)))
				require.NoError(t, err)
				var envelope map[string]interface{}
				require.NoError(t, json.Unmarshal(encoded, &envelope))
				require.Nil(t, envelope["error"], "prompts/list must succeed: %v", envelope)
				var entries []map[string]interface{}
				for _, pr := range envelope["result"].(map[string]interface{})["prompts"].([]interface{}) {
					entries = append(entries, pr.(map[string]interface{}))
				}
				return entries
			}

			var scopedNames []string
			for _, entry := range list(scoped) {
				scopedNames = append(scopedNames, entry["name"].(string))
				assert.NotContains(t, entry, "_meta", "owner stamp must not reach the wire: %v", entry)
			}
			assert.Contains(t, scopedNames, "a__greeting")
			assert.NotContains(t, scopedNames, "hidden__greeting", "a token scoped to 'a' must not see 'hidden' after a late enable")

			var adminNames []string
			for _, entry := range list(admin) {
				adminNames = append(adminNames, entry["name"].(string))
				assert.NotContains(t, entry, "_meta", "owner stamp must be stripped for admins too: %v", entry)
			}
			assert.Subset(t, adminNames, []string{"a__greeting", "hidden__greeting"})
		})
	}
}

// TestBuildAggregatedServerPrompts_DropsEmptyPromptName is Spec 105 FR-008
// (FR008-G7), the FR-006 prompt analogue: an upstream prompt with an empty
// raw name ("server:") has no registration identity — the direct-surface
// equivalent of an upstream tool named "" — and buildAggregatedServerPrompts
// must never register it at all, for any caller.
func TestBuildAggregatedServerPrompts_DropsEmptyPromptName(t *testing.T) {
	upstreamPrompts := []mcp.Prompt{
		{Name: "a:"},       // empty raw prompt name
		{Name: "a:review"}, // normal
	}
	getPrompt := func(_ context.Context, _ string, _ map[string]string) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	}

	all := buildAggregatedServerPrompts(nil, upstreamPrompts, getPrompt, nil, nil)

	names := make([]string, 0, len(all))
	for _, sp := range all {
		names = append(names, sp.Prompt.Name)
	}
	assert.NotContains(t, names, "a__", "an empty raw prompt name must never be registered")
	assert.Contains(t, names, "a__review", "the sibling prompt from the same server is unaffected")
}

// TestUnstampedPrompt_WithheldFromListAndGet_ForAdminAndAgent is Spec 105
// FR008-G7's SC-005 fixture: a prompt with no accepted registration (no
// canonical-owner stamp) is withheld from prompts/list and refused by
// prompts/get, for an agent token AND for an administrator — never only for
// a scoped caller, unlike every other withholding rule in this file.
func TestUnstampedPrompt_WithheldFromListAndGet_ForAdminAndAgent(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.EnablePrompts = true

	unstamped := mcp.Prompt{Name: "ghost__unstamped"}
	// Positive control (PR #1326 review round 2, chunk D/E): a prompt WITH a
	// genuine registration identity, registered alongside the withheld one.
	// Without this the test can pass for the wrong reason — if
	// prompts/list or prompts/get were broken entirely (returning nothing,
	// or erroring on every request), the unstamped prompt would still be
	// "absent" and every prompts/get would still be "refused", and the test
	// would say nothing went wrong.
	stamped := aggregatedPromptForTest("real", "control")
	proxy.server.SetPrompts(
		mcpserver.ServerPrompt{
			Prompt: unstamped,
			Handler: func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{}}, nil
			},
		},
		mcpserver.ServerPrompt{
			Prompt: stamped,
			Handler: func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []mcp.PromptMessage{
					{Role: mcp.RoleUser, Content: mcp.TextContent{Text: "control"}},
				}}, nil
			},
		},
	)

	agentCtxForTest := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "unrestricted",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	adminCtxForTest := auth.WithAuthContext(context.Background(), auth.AdminContext())

	initMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)

	for name, ctx := range map[string]context.Context{"unrestricted agent": agentCtxForTest, "administrator": adminCtxForTest} {
		require.NotNilf(t, proxy.server.HandleMessage(ctx, initMsg), "%s", name)

		listEncoded, err := json.Marshal(proxy.server.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{}}`)))
		require.NoErrorf(t, err, "%s", name)
		var listEnvelope map[string]interface{}
		require.NoError(t, json.Unmarshal(listEncoded, &listEnvelope))
		require.Nilf(t, listEnvelope["error"], "%s: prompts/list must succeed: %v", name, listEnvelope)
		var listedNames []string
		for _, pr := range listEnvelope["result"].(map[string]interface{})["prompts"].([]interface{}) {
			listedNames = append(listedNames, pr.(map[string]interface{})["name"].(string))
		}
		assert.NotContainsf(t, listedNames, "ghost__unstamped", "%s: an unstamped prompt is withheld from EVERYONE (SC-005)", name)
		// Positive control: the properly-stamped sibling prompt IS listed,
		// proving prompts/list is not simply returning an empty/broken result
		// that would vacuously satisfy the NotContains check above.
		assert.Containsf(t, listedNames, stamped.Name, "%s: a properly stamped prompt must still be listed", name)

		getEncoded, err := json.Marshal(proxy.server.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"ghost__unstamped"}}`)))
		require.NoErrorf(t, err, "%s", name)
		var getEnvelope map[string]interface{}
		require.NoError(t, json.Unmarshal(getEncoded, &getEnvelope))
		require.NotNilf(t, getEnvelope["error"], "%s: prompts/get must refuse an unstamped prompt: %v", name, getEnvelope)
		unstampedGetErr := getEnvelope["error"].(map[string]interface{})
		assert.Containsf(t, unstampedGetErr["message"], "not found",
			"%s: the refusal must be the absent-equivalent wording, not some other failure", name)

		// Positive control: prompts/get on the properly-stamped sibling must
		// actually succeed and return its content — proving prompts/get is
		// not simply erroring on every request, which would vacuously
		// satisfy the refusal assertion above.
		controlGetEncoded, err := json.Marshal(proxy.server.HandleMessage(ctx,
			[]byte(`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"`+stamped.Name+`"}}`)))
		require.NoErrorf(t, err, "%s", name)
		var controlGetEnvelope map[string]interface{}
		require.NoError(t, json.Unmarshal(controlGetEncoded, &controlGetEnvelope))
		require.Nilf(t, controlGetEnvelope["error"], "%s: prompts/get on the stamped control prompt must succeed: %v", name, controlGetEnvelope)
		controlMessages := controlGetEnvelope["result"].(map[string]interface{})["messages"].([]interface{})
		require.Lenf(t, controlMessages, 1, "%s", name)
	}
}
