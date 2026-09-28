package server

// T001 (Phase 1, shared setup): the enforcement-matrix fixture tool files for
// preflight_fixture_server.js — internal/server/testdata/profiles_v3/{github,
// notion,filesystem}.tools.json — read by FIXTURE_TOOLS_FILE and required by
// quickstart.md's §2/§3 108-a (and later 108-*) live recipe. This test proves
// the files exist, parse and match contracts/enforcement-matrix.md's fixture
// table exactly (name, description = name so BM25 matches are deterministic,
// annotations), so a hand-edit cannot silently drift the live-QA fixture away
// from the table the matrix and the Go-side fixture (profiles_v3_fixture_test.go)
// both encode.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// nodeFixtureTool mirrors the MCP tool definition shape
// preflight_fixture_server.js serves verbatim from FIXTURE_TOOLS_FILE
// (internal/server/preflight_e2e_test.go's fixtureTool).
type nodeFixtureTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

// wantNodeFixtureTool is one expected row of contracts/enforcement-matrix.md's
// fixture table, keyed by "server/tool".
type wantNodeFixtureTool struct {
	server      string
	readOnly    *bool
	destructive *bool
}

// wantNodeFixtureTools is the enforcement-matrix.md fixture table (the
// "Servers" section): server, tool, annotations. A nil pointer means the
// annotation is absent (github:search_code is genuinely unannotated).
var wantNodeFixtureTools = map[string]wantNodeFixtureTool{
	"github/list_issues":               {server: "github", readOnly: boolPtr(true)},
	"github/create_issue":              {server: "github", readOnly: boolPtr(false)},
	"github/delete_repo":               {server: "github", destructive: boolPtr(true)},
	"github/search_code":               {server: "github"},
	"github/get_secret_scanning_alert": {server: "github", readOnly: boolPtr(true)},
	"notion/update_page":               {server: "notion", readOnly: boolPtr(false)},
	"filesystem/read_text_file":        {server: "filesystem", readOnly: boolPtr(true)},
}

// nodeFixtureFileTools is which tools each per-server file must contain, in
// order (matches enforcementMatrixProfiles' upstream registration order in
// profiles_v3_fixture_test.go, so the two fixtures never drift apart on
// content even though they are read by different runtimes).
var nodeFixtureFileTools = map[string][]string{
	"github":     {"list_issues", "create_issue", "delete_repo", "search_code", "get_secret_scanning_alert"},
	"notion":     {"update_page"},
	"filesystem": {"read_text_file"},
}

func TestProfilesV3NodeFixtureFiles_MatchEnforcementMatrix(t *testing.T) {
	dir := "testdata/profiles_v3"

	for server, names := range nodeFixtureFileTools {
		t.Run(server, func(t *testing.T) {
			path := filepath.Join(dir, server+".tools.json")
			raw, err := os.ReadFile(path)
			require.NoError(t, err, "quickstart.md's §2 live instance recipe and preflight_fixture_server.js's FIXTURE_TOOLS_FILE both require %s", path)

			var tools []nodeFixtureTool
			require.NoError(t, json.Unmarshal(raw, &tools), "%s must be a valid JSON array of MCP tool definitions", path)
			require.Len(t, tools, len(names), "%s must list exactly the enforcement-matrix tools for %s", path, server)

			for i, tool := range tools {
				wantName := names[i]
				require.Equal(t, wantName, tool.Name, "%s[%d]: tool order/name must match the fixture table", path, i)
				require.Equal(t, wantName, tool.Description,
					"%s: description must equal the tool name so BM25 matches are deterministic (enforcement-matrix.md note)", path)
				require.NotEmpty(t, tool.InputSchema, "%s: %s must carry an inputSchema (MCP tool definition shape)", path, wantName)

				want, ok := wantNodeFixtureTools[server+"/"+wantName]
				require.True(t, ok, "%s: %s is not in the enforcement-matrix fixture table; fix wantNodeFixtureTools", path, wantName)

				switch {
				case want.readOnly != nil:
					got, present := tool.Annotations["readOnlyHint"]
					require.True(t, present, "%s: %s must set readOnlyHint", path, wantName)
					require.Equal(t, *want.readOnly, got, "%s: %s readOnlyHint", path, wantName)
					require.NotContains(t, tool.Annotations, "destructiveHint", "%s: %s must not also set destructiveHint", path, wantName)
				case want.destructive != nil:
					got, present := tool.Annotations["destructiveHint"]
					require.True(t, present, "%s: %s must set destructiveHint", path, wantName)
					require.Equal(t, *want.destructive, got, "%s: %s destructiveHint", path, wantName)
					require.NotContains(t, tool.Annotations, "readOnlyHint", "%s: %s must not also set readOnlyHint", path, wantName)
				default:
					require.Empty(t, tool.Annotations, "%s: %s must carry no annotations at all (unannotated row)", path, wantName)
				}
			}
		})
	}
}
