package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 108 D39 (T148): the tool refusals a REST caller sees are the DISCLOSED
// texts of internal/profile/testdata/contract/tool_refusals.json (a pinned
// token is told its own profile). The handlers pass the typed refusal through
// verbatim, so these tests render the expected message from the golden instead
// of repeating a literal that could drift from it.

type p108ToolRefusals struct {
	Refusals []struct {
		Name      string `json:"name"`
		Disclosed string `json:"disclosed"`
	} `json:"refusals"`
}

// p108DisclosedToolRefusal renders one disclosed golden refusal for
// github:create_issue under the profile label `"Work Read-only" (work-readonly)`.
func p108DisclosedToolRefusal(t testing.TB, name string) string {
	t.Helper()
	var g p108ToolRefusals
	p108ReadJSON(t, "internal/profile/testdata/contract/tool_refusals.json", &g)
	for _, r := range g.Refusals {
		if r.Name == name {
			return strings.NewReplacer(
				"<server>", "github", "<tool>", "create_issue", "<tier>", "write", "<cap>", "read",
				"<label>", `"Work Read-only" (work-readonly)`,
			).Replace(r.Disclosed)
		}
	}
	require.Failf(t, "missing golden", "no tool refusal named %q", name)
	return ""
}

// p108ErrorText returns the `error` field of a {"success":false,"error":...}
// body, JSON-decoded (the quotes around a profile title are escaped on the
// wire), or the raw body when it is not that envelope.
func p108ErrorText(body string) string {
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err == nil && env.Error != "" {
		return env.Error
	}
	return body
}
