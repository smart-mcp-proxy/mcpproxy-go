package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
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

	// Full aggregated set every case starts from: 2 built-ins + 2 upstream.
	base := func() []mcp.Prompt {
		return []mcp.Prompt{
			{Name: builtinSetup},
			{Name: builtinTrbl},
			{Name: githubPrompt},
			{Name: gitlabPrompt},
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
		})
	}
}

// TestFilterAggregatedPromptsForAuth_KeepsUnparseableName verifies a display
// name that cannot be parsed into server__prompt is kept, never dropped or
// panicked on (F1: the filter must not break on the pre-existing __ collision).
func TestFilterAggregatedPromptsForAuth_KeepsUnparseableName(t *testing.T) {
	proxy := &MCPProxyServer{}
	weird := "noseparator" // no "__", not a built-in
	got := proxy.filterAggregatedPromptsForAuth(
		auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type:           auth.AuthTypeAgent,
			AgentName:      "scoped-bot",
			AllowedServers: []string{"github"},
			Permissions:    []string{auth.PermRead},
		}),
		[]mcp.Prompt{{Name: weird}, {Name: FormatDirectPromptName("github", "x")}},
	)
	assert.ElementsMatch(t, []string{FormatDirectPromptName("github", "x"), weird}, promptNamesForTest(got))
}
