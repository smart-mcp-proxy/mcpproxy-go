package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Spec 108-f T069/T073 (FR-035, SC-009): the explainer renders the one access
// chain, so `allowed` equals what a real call does, and the first failure and
// the fixes follow the refusal precedence.

func (f *restV3Fixture) tokenDispatchCtx(t *testing.T, name string) context.Context {
	t.Helper()
	rec, err := f.rt.StorageManager().GetAgentTokenByName(name)
	require.NoError(t, err)
	require.NotNil(t, rec)
	return auth.WithAuthContext(context.Background(), rec.AuthContext())
}

func TestExplain_VerdictEqualsDispatchForEveryMatrixRow(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = false })
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mintClient("laptop", "work-full", auth.ProfileModeSwitchable)
	f.mint("ro-bot", "work-readonly")
	f.mintWith("gh-read", "", []string{"github"}, []string{auth.PermRead})

	type subjectCase struct {
		name    string
		subject profile.AccessSubject
		ctx     func() context.Context
	}
	cases := []subjectCase{
		{"locked client", profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "cursor"}, func() context.Context { return f.clientDispatchCtx(t, "cursor") }},
		{"switchable client", profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "laptop"}, func() context.Context { return f.clientDispatchCtx(t, "laptop") }},
		{"pinned agent token", profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: "ro-bot"}, func() context.Context { return f.tokenDispatchCtx(t, "ro-bot") }},
		{"unpinned token github+read", profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: "gh-read"}, func() context.Context { return f.tokenDispatchCtx(t, "gh-read") }},
		{"profile work-readonly", profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "work-readonly"}, func() context.Context { return urlProfileCtx(f.proxy, "work-readonly") }},
		{"anonymous, auth off, bound clients guard the anonymous caller", profile.AccessSubject{Kind: profile.AccessSubjectAnonymous}, anonCtx},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, tool := range enforcementMatrixAccessTools() {
				expl, err := f.proxy.Explain(context.Background(), tc.subject, tool.server+":"+tool.tool)
				require.NoError(t, err)

				// The caller picks the variant; a token with a narrow permission set
				// picks the one the tool's own annotations derive (the explainer
				// models the target-tier check, not a mismatched variant choice).
				variant := tool.variant
				if tc.subject.Kind == profile.AccessSubjectToken {
					ann, _ := f.proxy.EffectiveAnnotations(tool.server, tool.tool)
					variant = contracts.DeriveCallWith(ann)
				}
				before := f.totalDispatched()
				result, err := f.proxy.handleCallToolVariant(tc.ctx(), auditCallToolRequest(tool.server+":"+tool.tool, nil), variant)
				require.NoError(t, err)
				dispatched := f.totalDispatched() - before
				ran := !result.IsError && dispatched == 1

				assert.Equal(t, ran, expl.Verdict == profile.ExplainVerdictAllowed,
					"%s:%s verdict=%s first_failure=%s", tool.server, tool.tool, expl.Verdict, expl.FirstFailure)
				if expl.Verdict == profile.ExplainVerdictAllowed {
					assert.Empty(t, expl.FirstFailure)
					assert.Empty(t, expl.Fixes)
				} else {
					assert.Zero(t, dispatched, "a refused row reaches no upstream")
					assert.NotEmpty(t, expl.FirstFailure)
				}
				require.Len(t, expl.Steps, len(profile.StepOrder()))
			}
		})
	}
}

func TestExplain_FirstFailureFollowsRefusalPrecedence(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mintClient("old", "work-readonly", auth.ProfileModeLocked)
	_, err := f.rt.StorageManager().ForgetClientCredential("old")
	require.NoError(t, err)
	f.mint("rev-bot", "work-readonly")
	require.NoError(t, f.rt.StorageManager().RevokeAgentToken("rev-bot"))

	explain := func(s profile.AccessSubject, tool string) *explanationFacts {
		e, err := f.proxy.Explain(context.Background(), s, tool)
		require.NoError(t, err)
		return &explanationFacts{verdict: e.Verdict, first: e.FirstFailure, n: len(e.Fixes)}
	}
	cursor := profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "cursor"}

	assert.Equal(t, profile.StepTierCap, explain(cursor, "github:create_issue").first)
	assert.Equal(t, profile.ExplainVerdictHidden, explain(cursor, "github:create_issue").verdict)
	assert.Equal(t, profile.StepToolRule, explain(cursor, "github:get_secret_scanning_alert").first, "deny beats the allow rule")
	assert.Equal(t, profile.StepServerInScope, explain(cursor, "filesystem:read_text_file").first)
	assert.Equal(t, profile.StepCredential, explain(profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "old"}, "github:list_issues").first)
	assert.Equal(t, profile.StepCredential, explain(profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: "rev-bot"}, "github:list_issues").first, "a revoked token fails the credential step")
	assert.Equal(t, profile.ExplainVerdictAllowed, explain(cursor, "github:list_issues").verdict)
}

type explanationFacts struct {
	verdict profile.ExplainVerdict
	first   profile.ExplainStep
	n       int
}

func TestExplain_FixesOrderedByPreference(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	cursor := profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "cursor"}

	e, err := f.proxy.Explain(context.Background(), cursor, "github:create_issue")
	require.NoError(t, err)
	require.Len(t, e.Fixes, 2)
	assert.Equal(t, profile.FixAllowInProfile, e.Fixes[0].Action)
	assert.Equal(t, "work-readonly", e.Fixes[0].Target)
	assert.Equal(t, "Allow github:create_issue in Work · Read-only", e.Fixes[0].Label)
	assert.Equal(t, profile.FixMoveClient, e.Fixes[1].Action)
	assert.Equal(t, "cursor", e.Fixes[1].Target)
	assert.Equal(t, "Move Cursor to work-full", e.Fixes[1].Label, "the first OTHER profile that admits the tool")
	for _, fix := range e.Fixes {
		assert.Equal(t, profile.StepTierCap, fix.Step)
	}

	e, err = f.proxy.Explain(context.Background(), cursor, "github:search_code") // unannotated
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(e.Fixes), 2)
	assert.Equal(t, profile.FixClassifyInProfile, e.Fixes[0].Action, "an unannotated tool is classified first")
	assert.Equal(t, profile.FixAllowInProfile, e.Fixes[1].Action)

	e, err = f.proxy.Explain(context.Background(), cursor, "filesystem:read_text_file")
	require.NoError(t, err)
	require.NotEmpty(t, e.Fixes)
	assert.Equal(t, profile.FixAddServerToProfile, e.Fixes[0].Action)
	assert.Equal(t, "work-readonly", e.Fixes[0].Target)

	// An allowed row has no fixes.
	e, err = f.proxy.Explain(context.Background(), cursor, "github:list_issues")
	require.NoError(t, err)
	assert.Empty(t, e.Fixes)
	assert.Equal(t, "work-readonly", e.Profile.Name)
	assert.Equal(t, "pin", e.Profile.Source)
	assert.Equal(t, "cursor", e.Subject.Name)
}

func TestExplain_ProfileSubjectOffersNoMoveClient(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	e, err := f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "work-readonly"}, "github:create_issue")
	require.NoError(t, err)
	for _, fix := range e.Fixes {
		assert.NotEqual(t, profile.FixMoveClient, fix.Action, "move_client appears only for client subjects")
	}
	assert.Equal(t, profile.AccessSubjectProfile, e.Subject.Kind)
}

func TestExplain_BuiltinToolIsRefused(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	for _, tool := range []string{"upstream_servers", "retrieve_tools", "github:", ":tool", ""} {
		_, err := f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "work-full"}, tool)
		require.ErrorIs(t, err, profile.ErrExplainBuiltinTool, tool)
	}
}

func TestExplain_UnknownToolAndServerAreHidden(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.mintClient("laptop", "work-full", auth.ProfileModeSwitchable)
	laptop := profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "laptop"}
	for _, tool := range []string{"github:no_such_tool", "ghost:anything"} {
		e, err := f.proxy.Explain(context.Background(), laptop, tool)
		require.NoError(t, err, tool)
		assert.Equal(t, profile.ExplainVerdictHidden, e.Verdict, tool)
		assert.NotEmpty(t, e.FirstFailure)
	}
}

func TestExplain_SubjectErrors(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	_, err := f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: "nope"}, "github:list_issues")
	require.ErrorIs(t, err, profile.ErrUnknownToken)
	_, err = f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectToken, TokenName: "client-cursor"}, "github:list_issues")
	require.ErrorIs(t, err, profile.ErrClientCredentialToken)
	_, err = f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "ghost"}, "github:list_issues")
	require.ErrorIs(t, err, profile.ErrUnknownProfile)
	_, err = f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "no-such-client"}, "github:list_issues")
	require.ErrorIs(t, err, profile.ErrUnknownClient)
}

func TestExplain_AnonymousRequiresAuthFailsCredential(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	e, err := f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectAnonymous}, "github:list_issues")
	require.NoError(t, err)
	assert.Equal(t, profile.StepCredential, e.FirstFailure)
	assert.Equal(t, profile.ExplainVerdictHidden, e.Verdict)
}

func TestExplain_AnonymousUnderActiveGuardFailsProfile(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = false })
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	e, err := f.proxy.Explain(context.Background(), profile.AccessSubject{Kind: profile.AccessSubjectAnonymous}, "github:list_issues")
	require.NoError(t, err)
	assert.Equal(t, profile.StepProfile, e.FirstFailure)
	require.NotEmpty(t, e.Fixes)
	assert.Equal(t, profile.FixChangeSetting, e.Fixes[0].Action)
	assert.Equal(t, "require_mcp_auth", e.Fixes[0].Target)
	assert.Equal(t, "anonymous_profile", e.Fixes[1].Target)
}
