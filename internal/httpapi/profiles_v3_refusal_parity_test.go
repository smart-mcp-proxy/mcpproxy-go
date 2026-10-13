//go:build !server

package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-l (L14, T121): the four refusals every surface shares are compared
// to ONE golden (internal/profile/testdata/contract/refusals.json). This is the
// REST half; TestProfilesV3RefusalParity_MCP (internal/server) and
// TestProfilesV3RefusalParity_CLI (cmd/mcpproxy) are the other two, so REST,
// the MCP `profiles` tool and the CLI cannot print different words.

type p108Refusal struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Field  string `json:"field"`
	Error  string `json:"error"`
}

func p108LoadRefusals(t testing.TB) map[string]p108Refusal {
	t.Helper()
	var doc struct {
		Refusals []p108Refusal `json:"refusals"`
	}
	p108ReadJSON(t, "internal/profile/testdata/contract/refusals.json", &doc)
	out := map[string]p108Refusal{}
	for _, r := range doc.Refusals {
		out[r.Name] = r
	}
	require.Len(t, out, 4, "the golden carries the four shared refusals")
	return out
}

// text substitutes the template placeholders.
func (r p108Refusal) text(p, id string) string {
	return strings.NewReplacer("<p>", p, "<id>", id).Replace(r.Error)
}

func p108AssertRefusal(t *testing.T, r p108Refusal, status int, body map[string]any, p, id string) {
	t.Helper()
	require.Equal(t, r.Status, status, r.Name)
	require.Equal(t, r.text(p, id), body["error"], "%s: the message bytes", r.Name)
	if r.Code != "" {
		require.Equal(t, r.Code, body["code"], r.Name)
	} else {
		require.NotContains(t, body, "code", r.Name)
	}
	if r.Field != "" {
		require.Equal(t, r.Field, body["field"], r.Name)
	}
}

func TestProfilesV3RefusalParity(t *testing.T) {
	golden := p108LoadRefusals(t)

	t.Run("the shared mapping behind every REST writer and the MCP tool", func(t *testing.T) {
		cases := map[string]struct {
			err error
			p   string
			id  string
		}{
			"binding_bypassable_without_auth": {err: &internalRuntime.BindingGuardError{Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", Profile: "work"}}}, p: "work"},
			"no_client_credential":            {err: &internalRuntime.NoClientCredentialError{ClientID: "codex"}, id: "codex"},
			"profile_in_use":                  {err: &internalRuntime.ProfileInUseError{}},
			"unknown_profile":                 {err: &internalRuntime.ValidationError{Field: "profile", Message: `unknown profile "nope"`}, p: "nope"},
		}
		for name, tc := range cases {
			status, body := ProfilesErrorBody(tc.err)
			p108AssertRefusal(t, golden[name], status, body, tc.p, tc.id)
		}
	})

	t.Run("the REST handlers answer the golden", func(t *testing.T) {
		// no_client_credential: a client that was never connected.
		h := newBindingHarness(t, internalRuntime.ConservativeBindingGuard{})
		w := h.put("cursor", `{"profile":"ro"}`, nil, bindingAdminKey)
		p108AssertRefusal(t, golden["no_client_credential"], w.Code, decodeBody(t, w), "", "cursor")

		// unknown_profile: a connected client rebound to a profile that is not there.
		h.mint("cursor", "ro")
		w = h.put("cursor", `{"profile":"nope"}`, nil, bindingAdminKey)
		p108AssertRefusal(t, golden["unknown_profile"], w.Code, decodeBody(t, w), "nope", "")

		// binding_bypassable_without_auth: a write the guard refuses.
		refusal := &internalRuntime.BindingGuardError{
			Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "full", Mode: "locked"}},
			Fixes:    []internalRuntime.GuardFix{{Kind: "require_mcp_auth"}},
		}
		h.svc = internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
			Store: h.sm, HMACKey: func() ([]byte, error) { return h.key, nil },
			Config: func() *config.Config { return h.cfg }, Guard: func() internalRuntime.BindingGuard { return refusingGuard{err: refusal} },
			Activity: h.sm.SaveActivity,
		})
		h.srv.SetClientsService(h.svc)
		w = h.put("cursor", `{"profile":"full"}`, nil, bindingAdminKey)
		p108AssertRefusal(t, golden["binding_bypassable_without_auth"], w.Code, decodeBody(t, w), "full", "")
		require.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("mutation self-check: a different message or code is caught", func(t *testing.T) {
		r := golden["no_client_credential"]
		status, body := ProfilesErrorBody(&internalRuntime.NoClientCredentialError{ClientID: "codex"})
		require.NotEqual(t, r.text("", "other-client"), body["error"], "the placeholder substitution is not a wildcard")
		require.NotEqual(t, golden["profile_in_use"].Code, body["code"])
		require.Equal(t, http.StatusConflict, status)
	})
}
