package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-f T074d (FR-035): GET /access/explain - the administrator gate, the
// subject rules and their texts, and the wire shape.

func TestAccessExplainRoute_ScopedCallerIs403(t *testing.T) {
	g := newProfilesRig(t)
	rec := g.do(g.token, http.MethodGet, "/api/v1/access/explain?client=cursor&tool=alpha:list", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, g.fake.calls)
}

func TestAccessExplainRoute_SubjectAndToolRules(t *testing.T) {
	g := newProfilesRig(t)
	get := func(q string) *httptest.ResponseRecorder {
		return g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/access/explain"+q, "")
	}

	for _, tc := range []struct {
		name, q string
		status  int
		text    string
	}{
		{"no subject", "?tool=alpha:list", 400, "exactly one of client, token, profile, anonymous is required"},
		{"two subjects", "?client=cursor&profile=research&tool=alpha:list", 400, "exactly one of client, token, profile, anonymous is required"},
		{"client credential as token", "?token=client-cursor&tool=alpha:list", 400, "use client=<id> for a client credential"},
		{"built-in tool", "?profile=research&tool=upstream_servers", 400, "access/explain covers upstream tools (server:tool) only"},
		{"missing tool", "?profile=research", 400, "access/explain covers upstream tools (server:tool) only"},
		{"half a tool id", "?profile=research&tool=alpha:", 400, "access/explain covers upstream tools (server:tool) only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(tc.q)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			var body struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.text, body.Error)
		})
	}
	assert.Empty(t, g.fake.calls, "a malformed request never reaches the evaluator")

	// Unknown subjects are 404 with the subject's own text.
	for _, tc := range []struct {
		err  error
		q    string
		text string
	}{
		{profile.ErrUnknownToken, "?token=nope&tool=alpha:list", "token not found"},
		{profile.ErrUnknownProfile, "?profile=nope&tool=alpha:list", "profile not found"},
		{profile.ErrUnknownClient, "?client=nope&tool=alpha:list", "client not found"},
	} {
		g.fake.err = tc.err
		rec := get(tc.q)
		require.Equal(t, http.StatusNotFound, rec.Code, tc.q)
		assert.Contains(t, rec.Body.String(), tc.text)
	}

	// The evaluator being unwired is a 503, never an empty verdict.
	g.fake.err = internalRuntime.ErrEvaluatorUnavailable
	assert.Equal(t, http.StatusServiceUnavailable, get("?anonymous=true&tool=alpha:list").Code)

	// Every subject kind reaches the service.
	g.fake.err, g.fake.calls = nil, nil
	for q, kind := range map[string]profile.AccessSubjectKind{
		"?client=cursor&tool=alpha:list":    profile.AccessSubjectClient,
		"?token=ro-bot&tool=alpha:list":     profile.AccessSubjectToken,
		"?profile=research&tool=alpha:list": profile.AccessSubjectProfile,
		"?anonymous=true&tool=alpha:list":   profile.AccessSubjectAnonymous,
	} {
		require.Equal(t, http.StatusOK, get(q).Code, q)
		assert.Contains(t, g.fake.calls, "explain:"+string(kind)+":alpha:list")
	}
}

// The wire shape equals the shared contract fixture (the same JSON the CLI,
// the MCP tool, the Web UI and the macOS app decode).
func TestAccessExplainRoute_ResponseShapeEqualsTheSharedFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "profile", "testdata", "contract", "explain_blocked.json"))
	require.NoError(t, err)

	explanation := internalRuntime.AccessExplanation{
		Subject: internalRuntime.ExplainSubjectView{Kind: profile.AccessSubjectClient, Name: "cursor"},
		Tool:    "github:create_issue",
		Profile: internalRuntime.ExplainProfileView{Name: "work-readonly", Source: "pin"},
		Verdict: profile.ExplainVerdictHidden, FirstFailure: profile.StepTierCap,
		Fixes: []internalRuntime.Fix{
			{Step: profile.StepTierCap, Action: profile.FixAllowInProfile, Target: "work-readonly", Label: "Allow github:create_issue in Work Read-only"},
			{Step: profile.StepTierCap, Action: profile.FixMoveClient, Target: "cursor", Label: "Move Cursor to Work Full"},
		},
	}
	for _, s := range profile.StepOrder() {
		st := internalRuntime.ExplainStepView{Step: s, Status: profile.AccessStepPass}
		switch s {
		case profile.StepTierCap:
			st.Status, st.Detail = profile.AccessStepFail, "above_tier_cap"
		case profile.StepTokenPermission, profile.StepGlobalGate, profile.StepServerState, profile.StepToolApproval:
			st.Status = profile.AccessStepSkip
		}
		explanation.Steps = append(explanation.Steps, st)
	}
	got, err := json.Marshal(explanation)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(got))
}
