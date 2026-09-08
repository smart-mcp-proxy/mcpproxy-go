package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
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

// TestFilterAggregatedPromptsForAuth_UnstampedFailsClosed (Spec 104 FR-016g):
// an upstream prompt with no canonical-owner stamp cannot have come from
// buildAggregatedServerPrompts, so the filter must not guess its owner from the
// display name (that re-parse is the original leak). It is dropped for scoped
// callers and left alone for unscoped ones.
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

	got := proxy.filterAggregatedPromptsForAuth(scoped, []mcp.Prompt{unstamped, stamped})
	assert.ElementsMatch(t, []string{stamped.Name}, promptNamesForTest(got), "unstamped upstream prompt is dropped for a scoped caller")

	got = proxy.filterAggregatedPromptsForAuth(context.Background(), []mcp.Prompt{unstamped, stamped})
	assert.ElementsMatch(t, []string{unstamped.Name, stamped.Name}, promptNamesForTest(got), "unscoped callers are not filtered")
}

// TestStripAggregatedPromptServer_PreservesUpstreamMeta verifies the stamp is
// removed without disturbing upstream-supplied _meta and without mutating the
// registered prompt (mcp-go hands filters a shared Meta pointer).
func TestStripAggregatedPromptServer_PreservesUpstreamMeta(t *testing.T) {
	upstream := &mcp.Meta{AdditionalFields: map[string]any{"vendor/x": "keep"}}
	registered := mcp.Prompt{Name: "srv__p", Meta: stampAggregatedPromptServer(upstream, "srv")}

	owner, ok := aggregatedPromptServer(registered)
	require.True(t, ok)
	assert.Equal(t, "srv", owner)

	stripped := stripAggregatedPromptServer(registered)
	require.NotNil(t, stripped.Meta)
	assert.Equal(t, map[string]any{"vendor/x": "keep"}, stripped.Meta.AdditionalFields)
	_, stillStamped := aggregatedPromptServer(registered)
	assert.True(t, stillStamped, "the registered prompt must not be mutated by stripping a copy")

	bare := stripAggregatedPromptServer(mcp.Prompt{Name: "srv__q", Meta: stampAggregatedPromptServer(nil, "srv")})
	assert.Nil(t, bare.Meta, "a stamp-only _meta is removed entirely")
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
	assert.NotNil(t, denied["error"], "a token scoped to server 'a' must not fetch a__b's prompt: %v", denied)

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
