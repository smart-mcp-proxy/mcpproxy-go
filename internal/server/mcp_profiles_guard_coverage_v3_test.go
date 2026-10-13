//go:build !server

package server

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108-h T086 / H14 (FR-008a): every MUTATING operation of the `profiles`
// tool calls the binding guard before writing. The operations are enumerated from
// the BUILT schema's `operation` enum, each must be in the guarded table (a
// fixture that makes a binding bypassable, expected to answer
// binding_bypassable_without_auth and write nothing) or in the read table, and an
// operation in neither fails - so a new operation cannot ship without deciding
// which it is. The httpapi route-coverage test (T070a) is not edited: this is its
// MCP counterpart.

// guardAnon makes an anonymous caller resolve to anon-ro (the same scope as
// work-readonly, no switch to work-full), so a locked cursor on work-readonly is
// not bypassable until a write changes that.
func guardAnon(cfg *config.Config) {
	cfg.RequireMCPAuth = false
	ro := cfg.Profiles[0]
	ro.Name = "anon-ro"
	ro.Title = ""
	ro.SwitchableTo = nil
	cfg.Profiles = append(cfg.Profiles, ro)
	cfg.AnonymousProfile = "anon-ro"
}

type guardedOpCase struct {
	name      string
	configure func(*config.Config)
	bind      func(f *profilesToolFixture)
	args      map[string]any
}

// guardedOps: the six mutating operations, each with a write that makes a
// binding bypassable while require_mcp_auth is off.
var guardedOps = map[string][]guardedOpCase{
	"update": {
		{
			name: "narrowing the bound profile", configure: guardAnon,
			bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
			args: map[string]any{"operation": "update", "name": "work-readonly", "servers": []any{"github"}, "max_tier": "read"},
		},
		{
			// enforcement-matrix delta case (a): anonymous resolves to wf-anon, a
			// copy of work-full, and the switchable cursor reaches work-readonly
			// plus work-full, so wf-anon is not wider until work-full narrows.
			name: "narrowing a member of a switchable binding's switchable_to",
			configure: func(cfg *config.Config) {
				cfg.RequireMCPAuth = false
				wf := cfg.Profiles[1]
				wf.Name = "wf-anon"
				cfg.Profiles = append(cfg.Profiles, wf)
				cfg.AnonymousProfile = "wf-anon"
			},
			bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeSwitchable) },
			args: map[string]any{"operation": "update", "name": "work-full", "servers": []any{"github"}, "max_tier": "read"},
		},
		{
			name: "widening the anonymous-reachable profile", configure: guardAnon,
			bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
			args: map[string]any{"operation": "update", "name": "anon-ro", "servers": []any{"github", "notion", "filesystem"}, "max_tier": "read"},
		},
	},
	"delete": {{
		name: "reassign_to a narrower profile", configure: guardAnon,
		bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
		args: map[string]any{"operation": "delete", "name": "work-readonly", "reassign_to": "legacy"},
	}},
	"create": {{
		name: "a name the anonymous_profile dangles on",
		configure: func(cfg *config.Config) {
			cfg.RequireMCPAuth = false
			cfg.AnonymousProfile = "future" // dangling: anonymous is deny-all today
		},
		bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
		args: map[string]any{"operation": "create", "name": "future", "servers": []any{"github", "notion", "filesystem"}},
	}, {
		name: "a name the anonymous profile's switchable_to lists",
		configure: func(cfg *config.Config) {
			guardAnon(cfg)
			dangling := []string{"wide"}
			cfg.Profiles[len(cfg.Profiles)-1].SwitchableTo = &dangling
		},
		bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
		args: map[string]any{"operation": "create", "name": "wide", "servers": []any{"github", "notion", "filesystem"}},
	}},
	"rename": {{
		name: "onto a name the anonymous profile's switchable_to lists",
		configure: func(cfg *config.Config) {
			guardAnon(cfg)
			dangling := []string{"wide"}
			cfg.Profiles[len(cfg.Profiles)-1].SwitchableTo = &dangling
		},
		bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
		args: map[string]any{"operation": "rename", "name": "work-full", "new_name": "wide"},
	}},
	"classify": {{
		name: "admitting a tool to the anonymous-reachable profile", configure: guardAnon,
		bind: func(f *profilesToolFixture) { f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked) },
		// search_code is unannotated: denied under the read cap until classified.
		args: map[string]any{"operation": "classify", "name": "anon-ro", "tool": "github:search_code", "tier": "read"},
	}},
	"assign": {{
		name: "a named binding with no anonymous_profile",
		configure: func(cfg *config.Config) {
			cfg.RequireMCPAuth = false
		},
		bind: func(f *profilesToolFixture) { f.mintClient("cursor", "", auth.ProfileModeSwitchable) },
		args: map[string]any{"operation": "assign", "client": "cursor", "profile": "work-readonly", "mode": "locked"},
	}},
}

// readOps never write and never consult the guard.
var readOps = map[string]bool{
	"list": true, "get": true, "list_clients": true, "effective_tools": true, "explain": true,
}

// tokenSnapshot is the token store as (name, pin, mode) lines, for a
// nothing-was-written comparison.
func tokenSnapshot(t *testing.T, f *profilesToolFixture) string {
	t.Helper()
	tokens, err := f.rt.StorageManager().ListAgentTokens()
	require.NoError(t, err)
	lines := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		lines = append(lines, tok.Name+"|"+tok.ProfilePin+"|"+tok.ProfileMode+"|"+tok.Kind)
	}
	sort.Strings(lines)
	raw, _ := json.Marshal(lines)
	return string(raw)
}

func builtOperationEnum(t *testing.T) []string {
	t.Helper()
	op, ok := buildProfilesTool().InputSchema.Properties["operation"].(map[string]any)
	require.True(t, ok)
	enum, ok := op["enum"].([]string)
	require.True(t, ok)
	return enum
}

// TestProfilesTool_EveryMutatingOpGuarded: every operation of the built schema is
// classified; every mutating one is refused by the guard with nothing written.
func TestProfilesTool_EveryMutatingOpGuarded(t *testing.T) {
	for _, op := range builtOperationEnum(t) {
		_, guarded := guardedOps[op]
		switch {
		case guarded && readOps[op]:
			t.Fatalf("operation %q is in both the guarded and the read table", op)
		case !guarded && !readOps[op]:
			t.Fatalf("operation %q is in neither table: add a guarded fixture (a write that makes a binding bypassable) or list it as a read", op)
		case guarded != profilesMutatingOps[op]:
			t.Fatalf("operation %q: the tool's mutating set and the guarded table disagree", op)
		}
	}
	for op := range guardedOps {
		assert.Contains(t, builtOperationEnum(t), op, "a guarded table entry for an operation that no longer exists")
	}

	for op, cases := range guardedOps {
		for _, tc := range cases {
			t.Run(op+" "+tc.name, func(t *testing.T) {
				f := newProfilesToolFixture(t, tc.configure)
				tc.bind(f)
				configBefore, tokensBefore, changesBefore := f.configBytes(), tokenSnapshot(t, f), len(f.profileChanges())

				body := f.refused(apiKeyCtx(), tc.args)
				assert.Equal(t, "binding_bypassable_without_auth", body["code"], "%v", body)
				bindings, _ := body["bindings"].([]any)
				require.NotEmpty(t, bindings)
				assert.Equal(t, "cursor", bindings[0].(map[string]any)["client_id"])
				assert.NotEmpty(t, body["fixes"])

				assert.Equal(t, configBefore, f.configBytes(), "a refused %s wrote the config", op)
				assert.Equal(t, tokensBefore, tokenSnapshot(t, f), "a refused %s changed the token store", op)
				assert.Len(t, f.profileChanges(), changesBefore, "a refusal writes no profile_change record")

				// The same body REST answers for the same write.
				f.assertErrorEqualsRESTByArgs(tc.args)
			})
		}
	}
}

// assertErrorEqualsRESTByArgs replays a guarded MCP call against its REST route.
func (f *profilesToolFixture) assertErrorEqualsRESTByArgs(args map[string]any) {
	f.t.Helper()
	method, path, body := restRequestFor(args)
	f.assertErrorEqualsREST(apiKeyCtx(), args, method, path, body)
}

func restRequestFor(args map[string]any) (method, path string, body any) {
	name, _ := args["name"].(string)
	switch args["operation"] {
	case "create":
		return "POST", "/api/v1/profiles", restProfileBody(args)
	case "update":
		return "PUT", "/api/v1/profiles/" + name, restProfileBody(args)
	case "rename":
		return "POST", "/api/v1/profiles/" + name + "/rename", map[string]any{"new_name": args["new_name"]}
	case "delete":
		return "DELETE", "/api/v1/profiles/" + name + "?reassign_to=" + args["reassign_to"].(string), nil
	case "classify":
		// classify is a guarded PUT of the profile with one classification added.
		return "PUT", "/api/v1/profiles/" + name, map[string]any{
			"name": name, "servers": []string{"github", "notion"}, "max_tier": "read",
			"tools": map[string]any{"allow": []string{"notion:update_page", "github:get_secret_scanning_alert", "filesystem:read_text_file"},
				"deny": []string{"github:*secret*"}, "classify": map[string]string{args["tool"].(string): args["tier"].(string)}},
			"code_execution": false, "management_tools": false,
		}
	default: // assign
		return "PUT", "/api/v1/clients/" + args["client"].(string) + "/binding",
			map[string]any{"profile": args["profile"], "mode": args["mode"]}
	}
}

func restProfileBody(args map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range args {
		if k != "operation" {
			out[k] = v
		}
	}
	return out
}
