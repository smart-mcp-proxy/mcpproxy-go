package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Spec 124 D2: every tool-call attempt has exactly ONE counted row. The
// counting rule (storage.AttemptRoleOf) is stateless and correct only while
// the emit sites uphold that, so these tests drive the real handlers and count
// what they persist.

// attemptCounts sums the roles of every persisted row of one token.
type attemptCounts struct{ attempts, failures, blocks int }

func (f *restV3Fixture) attemptCountsFor(token string) attemptCounts {
	var c attemptCounts
	for _, r := range f.activities() {
		if r.TokenName != token {
			continue
		}
		role := storage.AttemptRoleOf(r)
		if !role.Counted {
			continue
		}
		c.attempts++
		if role.Failed {
			c.failures++
		}
		if role.Block {
			c.blocks++
		}
	}
	return c
}

func (f *restV3Fixture) eventuallyCounts(token string, want attemptCounts) {
	f.t.Helper()
	require.Eventually(f.t, func() bool { return f.attemptCountsFor(token) == want },
		attributionWait, 10*time.Millisecond, "counts for %s: want %+v", token, want)
	// The activity stream is ordered: once the marker written by settle is
	// stored, nothing emitted earlier is still in flight, so "no extra counted
	// row" is not vacuous.
	f.settle()
	assert.Equal(f.t, want, f.attemptCountsFor(token), "no further counted row may arrive")
}

// A successful call writes a tool_call row and its call_tool_* mirror: one
// attempt, no failure.
func TestAttemptInvariant_SuccessfulCallIsOneAttempt(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("inv-ok", "")

	rec := f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	f.eventuallyCounts("inv-ok", attemptCounts{attempts: 1})
}

// A pre-dispatch profile refusal writes a blocking policy_decision and returns
// without a tool_call row: the decision is the counted row, a failure and a
// policy block.
func TestAttemptInvariant_PreDispatchRefusalIsOneBlockedAttempt(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("inv-refused", restV3ReadonlyID)

	rec := f.callTool(key, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	f.eventuallyCounts("inv-refused", attemptCounts{attempts: 1, failures: 1, blocks: 1})
}

// A builtin management operation refused for an agent token writes one
// internal_tool_call error row. It is a refusal by MCPProxy itself, so it is a
// policy block, not an ordinary failure (FR-016).
func TestAttemptInvariant_BuiltinPolicyRefusalIsABlock(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("inv-mgmt", "")

	f.callTool(key, "upstream_servers", map[string]interface{}{"operation": "remove", "name": "github"}, "")

	f.eventuallyCounts("inv-mgmt", attemptCounts{attempts: 1, failures: 1, blocks: 1})
	for _, r := range f.activities() {
		if r.TokenName == "inv-mgmt" && r.Type == storage.ActivityTypeInternalToolCall && r.ToolName == "upstream_servers" {
			assert.Equal(t, storage.ErrorClassProxyPolicy, r.ErrorClass)
		}
	}
}

// A blocking decision emitted NEXT TO a tool_call row (generation change,
// review not recorded, output sanitisation) is marked as its companion at the
// emit site and is not counted a second time. Driven through the proxy's own
// two funnels, in the order those sites call them.
func TestAttemptInvariant_CompanionDecisionIsNotCountedAgain(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "inv-companion", TokenPrefix: "mcp_agt_cccc",
	})
	const reqID = "req-companion-1"

	f.proxy.emitActivityToolCallCompleted(ctx, "github", "list_issues", "", reqID, "mcp", "error", "tool identity changed",
		5, nil, "", false, "", nil, "", "", 0, 0, "", nil, "")
	f.proxy.emitActivityCompanionDecision(ctx, "github", "list_issues", "", reqID, "blocked",
		"tool identity changed", telemetry.BlockReasonToolNotCallable)

	f.eventuallyCounts("inv-companion", attemptCounts{attempts: 1, failures: 1})

	var decision *storage.ActivityRecord
	for _, r := range f.activities() {
		if r.TokenName == "inv-companion" && r.Type == storage.ActivityTypePolicyDecision {
			decision = r
		}
	}
	require.NotNil(t, decision, "the decision row is still written and shown")
	assert.True(t, decision.Companion)

	// The same decision WITHOUT the mark is a sole row and is counted: the
	// mark, not the row type, is what keeps the count at one.
	f.proxy.emitActivityPolicyDecision(ctx, "github", "list_issues", "", "req-sole-2", "blocked",
		"outside profile", telemetry.BlockReasonProfileScope)
	f.eventuallyCounts("inv-companion", attemptCounts{attempts: 2, failures: 2, blocks: 1})
}

// Review round 2: a call refused before any gate or dispatch, because the
// request itself was unusable, used to leave no activity row. It is one failed
// attempt of the session.
func TestAttemptInvariant_MalformedCallIsOneFailedAttempt(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	key := f.mint("inv-malformed", "")

	// Arguments that do not parse.
	f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{"}, "")
	f.eventuallyCounts("inv-malformed", attemptCounts{attempts: 1, failures: 1, blocks: 1})

	// A name that is not server:tool, and no name at all.
	f.callTool(key, "call_tool_read", map[string]interface{}{"name": "no-colon", "args_json": "{}"}, "")
	f.callTool(key, "call_tool_read", map[string]interface{}{"args_json": "{}"}, "")
	f.eventuallyCounts("inv-malformed", attemptCounts{attempts: 3, failures: 3, blocks: 3})

	rows := 0
	for _, r := range f.activities() {
		if r.TokenName != "inv-malformed" {
			continue
		}
		rows++
		assert.Equal(t, storage.ActivityTypePolicyDecision, r.Type, "MCPProxy refused the call; nothing was dispatched")
		assert.Empty(t, r.Arguments, "the unusable arguments are not recorded")
		assert.NotEmpty(t, r.WorkSessionID)
	}
	assert.Equal(t, 3, rows, "one row per refused request and no mirror")
}

// Review round 2: code_execution refused because the feature is disabled is
// one blocked attempt, not a call that left no trace.
func TestAttemptInvariant_DisabledCodeExecutionIsOneBlockedAttempt(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.EnableCodeExecution = false })
	key := f.mint("inv-codeexec", "")

	f.callTool(key, "code_execution", map[string]interface{}{"code": "1+1"}, "")
	f.eventuallyCounts("inv-codeexec", attemptCounts{attempts: 1, failures: 1, blocks: 1})
}
