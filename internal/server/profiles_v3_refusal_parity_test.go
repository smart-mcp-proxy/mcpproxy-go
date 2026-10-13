//go:build !server

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 108-l (L14, T121): the MCP `profiles` tool half of the shared-refusal
// parity. The same golden (internal/profile/testdata/contract/refusals.json)
// is read by TestProfilesV3RefusalParity (REST, internal/httpapi) and
// TestProfilesV3RefusalParity_CLI (cmd/mcpproxy). Here each refusal is
// triggered through the tool and its error body must carry exactly the golden
// code, field and message bytes.

type p108McpRefusal struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Field  string `json:"field"`
	Error  string `json:"error"`
	Status int    `json:"status"`
}

func p108McpLoadRefusals(t *testing.T) map[string]p108McpRefusal {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "profile", "testdata", "contract", "refusals.json"))
	require.NoError(t, err)
	var doc struct {
		Refusals []p108McpRefusal `json:"refusals"`
	}
	require.NoError(t, json.Unmarshal(b, &doc))
	out := map[string]p108McpRefusal{}
	for _, r := range doc.Refusals {
		out[r.Name] = r
	}
	require.Len(t, out, 4)
	return out
}

func (r p108McpRefusal) assert(t *testing.T, body map[string]any, p, id string) {
	t.Helper()
	want := strings.NewReplacer("<p>", p, "<id>", id).Replace(r.Error)
	require.Equal(t, want, body["error"], "%s: the message bytes", r.Name)
	if r.Code != "" {
		require.Equal(t, r.Code, body["code"], r.Name)
	}
	if r.Field != "" {
		require.Equal(t, r.Field, body["field"], r.Name)
	}
}

func TestProfilesV3RefusalParity_MCP(t *testing.T) {
	golden := p108McpLoadRefusals(t)

	t.Run("profile_in_use", func(t *testing.T) {
		f := newProfilesToolFixture(t, nil)
		f.mint("ro-bot", "work-readonly")
		body := f.refused(apiKeyCtx(), map[string]any{"operation": "delete", "name": "work-readonly"})
		golden["profile_in_use"].assert(t, body, "", "")
	})

	t.Run("no_client_credential", func(t *testing.T) {
		if !clientsEdition {
			t.Skip("client credentials exist in the personal edition only")
		}
		f := newProfilesToolFixture(t, nil)
		body := f.refused(apiKeyCtx(), map[string]any{"operation": "assign", "client": "codex", "profile": "work-full"})
		golden["no_client_credential"].assert(t, body, "", "codex")
	})

	t.Run("unknown_profile", func(t *testing.T) {
		if !clientsEdition {
			t.Skip("client credentials exist in the personal edition only")
		}
		f := newProfilesToolFixture(t, nil)
		f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
		body := f.refused(apiKeyCtx(), map[string]any{"operation": "assign", "client": "cursor", "profile": "nope"})
		golden["unknown_profile"].assert(t, body, "nope", "")
	})

	t.Run("binding_bypassable_without_auth", func(t *testing.T) {
		c := guardedOps["update"][0] // narrowing the bound profile while anonymous resolves to anon-ro
		f := newProfilesToolFixture(t, c.configure)
		c.bind(f)
		body := f.refused(apiKeyCtx(), c.args)
		golden["binding_bypassable_without_auth"].assert(t, body, "work-readonly", "")
	})

	t.Run("mutation self-check: a different message is caught", func(t *testing.T) {
		f := newProfilesToolFixture(t, nil)
		f.mint("ro-bot", "work-readonly")
		body := f.refused(apiKeyCtx(), map[string]any{"operation": "delete", "name": "work-readonly"})
		require.NotEqual(t, golden["profile_in_use"].Error+"!", body["error"])
		require.NotEqual(t, golden["no_client_credential"].Code, body["code"])
	})
}
