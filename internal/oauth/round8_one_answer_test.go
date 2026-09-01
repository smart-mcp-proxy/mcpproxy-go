package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Issue #1148, round 8 finding 3. `command` and `working_dir` are ONE leaf with
// THREE doors and, before this round, TWO answers: the generic MCP view
// (`quarantine_security list_quarantined`, built by the shared walk) ran the
// leaf rule over them, while `upstream_servers list` and
// RedactServerSecretFields (REST + SSE) published them RAW — and the decision
// table recorded them as not-secret, which is the third answer.
//
// A command path is rarely a credential. That is a reason for the rule to leave
// it readable, not a reason for three doors to disagree: the shared leaf rule
// already passes `npx` and `/opt/tools` through untouched, and it is the only
// thing that also catches a token pasted into either field.
func TestCommandAndWorkingDir_HaveOneAnswerOnEveryDoor(t *testing.T) {
	const token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

	// The generic MCP view — the reference answer.
	view := RedactedConfigView("", &config.ServerConfig{
		Name: "s", Command: token, WorkingDir: "/opt/" + token,
	})
	require.NotNil(t, view)

	srv := &contracts.Server{Name: "s", Command: token, WorkingDir: "/opt/" + token}
	RedactServerSecretFields(srv)

	assert.NotContains(t, srv.Command, token,
		"the REST/SSE door published `command` verbatim while the MCP door masks it")
	assert.NotContains(t, srv.WorkingDir, token,
		"the REST/SSE door published `working_dir` verbatim while the MCP door masks it")
	assert.Equal(t, view["command"], srv.Command, "one leaf, one answer")
	assert.Equal(t, view["working_dir"], srv.WorkingDir, "one leaf, one answer")
}

// The readable half of the same rule: an ordinary command and an ordinary
// working directory must survive every door byte-identical, or masking them
// would have cost the operator the field.
func TestCommandAndWorkingDir_StayReadableWhenTheyCarryNoCredential(t *testing.T) {
	srv := &contracts.Server{Name: "s", Command: "npx", WorkingDir: "/opt/tools/mcp"}
	RedactServerSecretFields(srv)
	assert.Equal(t, "npx", srv.Command)
	assert.Equal(t, "/opt/tools/mcp", srv.WorkingDir)
}

// The decision table is the third door: it recorded `command` / `working_dir`
// as not-secret, which is what let two of the three implementations disagree
// without any guard noticing.
func TestCommandAndWorkingDir_DecisionMatchesTheRule(t *testing.T) {
	for _, path := range []string{"command", "working_dir"} {
		assert.Equal(t, MaskDecisionRefuse, ServerFieldMaskDecisions[path],
			"%s is masked on every read door and no write rule binds a revert to it, "+
				"so an echoed mask must be refused", path)
	}
}

// RedactedConfigView is the hoisted view every door can now reach — including
// the ones below internal/server in the import graph.
func TestRedactedConfigView_MasksEverySecretBearingLeaf(t *testing.T) {
	const token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"
	view := RedactedConfigView("", &config.ServerConfig{
		Name:      "s",
		URL:       "https://h/mcp?opaque=" + token,
		Env:       map[string]string{"BENIGN": token},
		Headers:   map[string]string{"X-Custom": token},
		Args:      []string{"--endpoint=" + token},
		OAuth:     &config.OAuthConfig{ClientSecret: token},
		Isolation: &config.IsolationConfig{ExtraArgs: []string{"-e", "API_KEY=" + token}},
	})
	require.NotNil(t, view)
	assert.NotContains(t, flatten(view), token, "the shared view published a credential: %#v", view)
}

func flatten(v interface{}) string {
	switch typed := v.(type) {
	case map[string]interface{}:
		out := ""
		for k, val := range typed {
			out += k + "=" + flatten(val) + ";"
		}
		return out
	case []interface{}:
		out := ""
		for _, val := range typed {
			out += flatten(val) + ","
		}
		return out
	case string:
		return typed
	default:
		return ""
	}
}
