package runtime

// Spec 133 PR-1a: the agent-token lifecycle reports each committed effect to
// the AdminAuditor, from what the storage transaction changed; refusals and
// no-ops report nothing.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

type capturingAuditor struct {
	mu   sync.Mutex
	recs []AdminRecord
}

func (c *capturingAuditor) AdminAction(_ context.Context, rec AdminRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, rec)
}

func (c *capturingAuditor) take() []AdminRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.recs
	c.recs = nil
	return out
}

// lineOf renders a record with a fixed api_key request context so the test
// can assert on the wire shape.
func lineOf(t *testing.T, rec AdminRecord) map[string]interface{} {
	t.Helper()
	line, err := audit.NewAdminAction(audit.AdminActionInput{
		Ts: rec.At, RequestID: "req-1", Origin: "local", Source: "api",
		Caller: audit.Caller{Kind: "api_key"}, Action: rec.Action, Target: rec.Target, Changes: rec.Changes,
	})
	require.NoError(t, err)
	raw, err := line.JSON()
	require.NoError(t, err)
	var obj map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &obj))
	return obj
}

func newAuditedCredHarness(t *testing.T) (*credHarness, *capturingAuditor) {
	h := newCredHarness(t)
	cap := &capturingAuditor{}
	h.cs.adminAudit = &adminAuditHolder{}
	h.cs.adminAudit.set(cap)
	return h, cap
}

func restTokenReq(name string) IssueTokenRequest {
	return IssueTokenRequest{Name: name, ExpiresIn: "1h", Expiry: ExpiryTokenDefault}
}

func TestAdminAudit_TokenLifecycle(t *testing.T) {
	ctx := context.Background()
	h, cap := newAuditedCredHarness(t)
	a := Actor{Kind: "api_key", Surface: profile.SurfaceCLI}

	res, err := h.cs.IssueToken(ctx, a, restTokenReq("ci-bot"))
	require.NoError(t, err)
	recs := cap.take()
	require.Len(t, recs, 1)
	require.Equal(t, audit.ActionTokenCreate, recs[0].Action)
	created := lineOf(t, recs[0])
	tgt := created["target"].(map[string]interface{})
	require.Equal(t, "ci-bot", tgt["name"])
	ref := tgt["ref"].(string)
	require.True(t, auth.ValidAuditRef(ref))
	stored, _ := h.sm.GetAgentTokenByName("ci-bot")
	require.Equal(t, stored.AuditRef, ref, "the line's ref is the stored one")
	require.Contains(t, mustJSON(t, created), `"after":"`+res.View.TokenPrefix+`"`)
	require.NotContains(t, mustJSON(t, created), res.Secret)

	// Rotate: old and new prefix from the rotating transaction, same ref.
	rot, err := h.cs.Regenerate(ctx, a, "ci-bot")
	require.NoError(t, err)
	recs = cap.take()
	require.Len(t, recs, 1)
	require.Equal(t, audit.ActionTokenRotate, recs[0].Action)
	rl := lineOf(t, recs[0])
	require.Equal(t, ref, rl["target"].(map[string]interface{})["ref"])
	ch := rl["changes"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "/token_prefix", ch["field"])
	require.Equal(t, res.View.TokenPrefix, ch["before"])
	require.Equal(t, rot.View.TokenPrefix, ch["after"])
	require.NotContains(t, mustJSON(t, rl), rot.Secret)

	// Revoke once: one line; again: nothing.
	_, err = h.cs.Revoke(ctx, a, CredentialRef{Token: "ci-bot"}, true)
	require.NoError(t, err)
	recs = cap.take()
	require.Len(t, recs, 1)
	require.Equal(t, audit.ActionTokenRevoke, recs[0].Action)
	vl := lineOf(t, recs[0])
	require.Equal(t, ref, vl["target"].(map[string]interface{})["ref"])
	require.Contains(t, mustJSON(t, vl), `"before":"`+rot.View.TokenPrefix+`"`)
	_, err = h.cs.Revoke(ctx, a, CredentialRef{Token: "ci-bot"}, true)
	require.NoError(t, err)
	require.Empty(t, cap.take(), "revoking an already revoked token writes no line")

	// Rotating a revoked token is refused: nothing.
	_, err = h.cs.Regenerate(ctx, a, "ci-bot")
	require.Error(t, err)
	require.Empty(t, cap.take())

	// Permanent delete: one line with the same ref; a second delete is a 404.
	_, err = h.cs.DeleteToken(ctx, a, "ci-bot")
	require.NoError(t, err)
	recs = cap.take()
	require.Len(t, recs, 1)
	require.Equal(t, audit.ActionTokenDelete, recs[0].Action)
	require.Equal(t, ref, lineOf(t, recs[0])["target"].(map[string]interface{})["ref"])
	_, err = h.cs.DeleteToken(ctx, a, "ci-bot")
	require.Error(t, err)
	require.Empty(t, cap.take())

	// Refused create (duplicate name syntax error): nothing.
	_, err = h.cs.IssueToken(ctx, a, restTokenReq("bad name"))
	require.Error(t, err)
	require.Empty(t, cap.take())
}

func TestAdminAudit_MCPCreateTokenAndRevoke(t *testing.T) {
	ctx := context.Background()
	h, cap := newAuditedCredHarness(t)
	_, err := h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("worker", "ro", "1h"))
	require.NoError(t, err)
	recs := cap.take()
	require.Len(t, recs, 1)
	require.Equal(t, audit.ActionTokenCreate, recs[0].Action)
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "worker"}, false)
	require.NoError(t, err)
	recs = cap.take()
	require.Len(t, recs, 1)
	require.Equal(t, audit.ActionTokenRevoke, recs[0].Action)
}

// A client credential's record revoked through the legacy token-name door is
// not an agent token: no token.* line (its client line is PR-1b's).
func TestAdminAudit_LegacyClientRecordRevokeWritesNoTokenLine(t *testing.T) {
	ctx := context.Background()
	h, cap := newAuditedCredHarness(t)
	_, err := h.cs.IssueClient(ctx, mcpActor(), mcpClientReq("my-worker", "ro", "1h"))
	require.NoError(t, err)
	cap.take()
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: auth.ClientTokenName("my-worker")}, true)
	require.NoError(t, err)
	for _, r := range cap.take() {
		require.NotEqual(t, audit.ActionTokenRevoke, r.Action, "a client record is never an agent_token target")
	}
	_, err = h.cs.DeleteToken(ctx, mcpActor(), auth.ClientTokenName("my-worker"))
	require.NoError(t, err)
	require.Empty(t, cap.take())
}

func TestAdminAudit_TimestampIsTheCommitTime(t *testing.T) {
	ctx := context.Background()
	h, cap := newAuditedCredHarness(t)
	_, err := h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "none"}, false)
	require.Error(t, err)
	_, err = h.cs.IssueToken(ctx, mcpActor(), mcpTokenReq("tsk", "ro", "1h"))
	require.NoError(t, err)
	cap.take()
	_, err = h.cs.Revoke(ctx, mcpActor(), CredentialRef{Token: "tsk"}, false)
	require.NoError(t, err)
	recs := cap.take()
	require.Len(t, recs, 1)
	stored, _ := h.sm.GetAgentTokenByName("tsk")
	require.NotNil(t, stored.RevokedAt)
	require.True(t, recs[0].At.Equal(*stored.RevokedAt), "revoke ts is the RevokedAt the transaction stamped")
}

type lockProbeAuditor struct {
	mu   *sync.Mutex
	held []bool
}

func (l *lockProbeAuditor) AdminAction(_ context.Context, _ AdminRecord) {
	ok := l.mu.TryLock()
	if ok {
		l.mu.Unlock()
	}
	l.held = append(l.held, !ok)
}

// The synchronous audit write runs after the credential lock is released, so
// a stalled sink never blocks a concurrent revoke or a guarded config write.
func TestAdminAudit_WrittenOutsideTheCredentialLock(t *testing.T) {
	ctx := context.Background()
	h := newCredHarness(t)
	probe := &lockProbeAuditor{mu: h.cs.mu}
	h.cs.adminAudit = &adminAuditHolder{}
	h.cs.adminAudit.set(probe)
	a := Actor{Kind: "api_key", Surface: profile.SurfaceCLI}
	_, err := h.cs.IssueToken(ctx, a, restTokenReq("lk"))
	require.NoError(t, err)
	_, err = h.cs.Regenerate(ctx, a, "lk")
	require.NoError(t, err)
	_, err = h.cs.Revoke(ctx, a, CredentialRef{Token: "lk"}, true)
	require.NoError(t, err)
	_, err = h.cs.DeleteToken(ctx, a, "lk")
	require.NoError(t, err)
	require.Equal(t, []bool{false, false, false, false}, probe.held)
}

func TestAdminAudit_RotationTimestampFromTheTransaction(t *testing.T) {
	ctx := context.Background()
	h, cap := newAuditedCredHarness(t)
	a := Actor{Kind: "api_key", Surface: profile.SurfaceCLI}
	_, err := h.cs.IssueToken(ctx, a, restTokenReq("rt"))
	require.NoError(t, err)
	_, err = h.cs.Regenerate(ctx, a, "rt")
	require.NoError(t, err)
	recs := cap.take()
	stored, _ := h.sm.GetAgentTokenByName("rt")
	require.NotNil(t, stored.RotatedAt)
	require.True(t, recs[len(recs)-1].At.Equal(*stored.RotatedAt))
}

func TestAdminAudit_CreateAndDeleteTimestampsFromTheTransaction(t *testing.T) {
	ctx := context.Background()
	h, cap := newAuditedCredHarness(t)
	a := Actor{Kind: "api_key", Surface: profile.SurfaceCLI}
	_, err := h.cs.IssueToken(ctx, a, restTokenReq("cd"))
	require.NoError(t, err)
	recs := cap.take()
	stored, _ := h.sm.GetAgentTokenByName("cd")
	require.True(t, recs[0].At.Equal(stored.CreatedAt), "create ts is the created_at the transaction stamped")
	deleted, err := h.cs.DeleteToken(ctx, a, "cd")
	require.NoError(t, err)
	recs = cap.take()
	require.False(t, deleted.CommittedAt.IsZero())
	require.True(t, recs[0].At.Equal(deleted.CommittedAt))
}

func TestAdminAudit_NoAuditorIsANoOp(t *testing.T) {
	h := newCredHarness(t)
	_, err := h.cs.IssueToken(context.Background(), mcpActor(), mcpTokenReq("quiet", "ro", "1h"))
	require.NoError(t, err)
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}
