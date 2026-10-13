package corpusio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoSnapshot is the committed LiveMCPTool frozen snapshot (Spec 083 T029,
// research D6). Provenance + license in ATTRIBUTION.md next to it.
const repoSnapshot = "../../specs/083-discovery-profiler/datasets/livemcptool_snapshot/tools.json"

// Counts pinned to the committed snapshot (HF dataset ICIP/LiveMCPBench,
// revision ddea2d24196638bc4026c4cb891f679d0357bfd0). The snapshot is frozen:
// if these change, the snapshot was regenerated and ATTRIBUTION.md must be
// updated in the same commit.
const (
	snapshotServers = 70
	snapshotTools   = 527
	snapshotVersion = "livemcptool@ddea2d24"
)

func TestLoadLiveMCPToolSnapshot(t *testing.T) {
	corpus, golden, reason, err := LoadLiveMCPTool(repoSnapshot)
	if err != nil {
		t.Fatalf("LoadLiveMCPTool(%s): %v", repoSnapshot, err)
	}
	if corpus.Version != snapshotVersion {
		t.Errorf("version = %q, want %q", corpus.Version, snapshotVersion)
	}
	if len(corpus.Tools) != snapshotTools {
		t.Errorf("tool count = %d, want %d", len(corpus.Tools), snapshotTools)
	}
	servers := map[string]bool{}
	seen := map[string]bool{}
	for _, tl := range corpus.Tools {
		if tl.Server == "" || tl.Name == "" {
			t.Fatalf("tool %q has empty server or name", tl.ToolID)
		}
		if want := tl.Server + ":" + tl.Name; tl.ToolID != want {
			t.Fatalf("tool_id %q, want %q", tl.ToolID, want)
		}
		if seen[tl.ToolID] {
			t.Fatalf("duplicate tool_id %q", tl.ToolID)
		}
		seen[tl.ToolID] = true
		servers[tl.Server] = true
		// LiveMCPTool is schema-bearing: every record in the committed
		// snapshot carries an inputSchema (verified at capture).
		if len(tl.Schema) == 0 {
			t.Errorf("tool %q has no input schema", tl.ToolID)
		}
	}
	if len(servers) != snapshotServers {
		t.Errorf("server count = %d, want %d", len(servers), snapshotServers)
	}

	// FR-011: relevance labels are NOT derivable from the LiveMCPBench task
	// annotations — the loader must record explicit absence.
	if golden != nil {
		t.Errorf("golden set = %+v, want nil (labels not derivable)", golden)
	}
	if reason == "" {
		t.Error("golden-absence reason is empty; FR-011 requires explicit absence")
	}
}

// TestLoadLiveMCPToolDeterminism guards FR-010/FR-021: two loads of the same
// snapshot yield byte-identical tool order and schemas.
func TestLoadLiveMCPToolDeterminism(t *testing.T) {
	a, _, _, err := LoadLiveMCPTool(repoSnapshot)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	b, _, _, err := LoadLiveMCPTool(repoSnapshot)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(a.Tools) != len(b.Tools) {
		t.Fatalf("tool counts differ: %d vs %d", len(a.Tools), len(b.Tools))
	}
	for i := range a.Tools {
		if a.Tools[i].ToolID != b.Tools[i].ToolID {
			t.Fatalf("tool order differs at %d: %q vs %q", i, a.Tools[i].ToolID, b.Tools[i].ToolID)
		}
		if string(a.Tools[i].Schema) != string(b.Tools[i].Schema) {
			t.Fatalf("schema bytes differ for %q", a.Tools[i].ToolID)
		}
	}
	// Snapshot order is canonical: sorted by tool_id.
	for i := 1; i < len(a.Tools); i++ {
		if a.Tools[i-1].ToolID >= a.Tools[i].ToolID {
			t.Fatalf("tools not in sorted tool_id order at %d: %q >= %q",
				i, a.Tools[i-1].ToolID, a.Tools[i].ToolID)
		}
	}
}

// TestLoadLiveMCPToolValidation exercises per-record validation (FR-011) on
// synthetic snapshots.
func TestLoadLiveMCPToolValidation(t *testing.T) {
	valid := `{
  "version": "livemcptool@test",
  "server_count": 2,
  "tool_count": 3,
  "tools": [
    {"server": "alpha", "tool": "add", "description": "adds", "inputSchema": {"type": "object"}},
    {"server": "alpha", "tool": "sub", "description": "", "inputSchema": {"type": "object"}},
    {"server": "beta", "tool": "get", "description": "gets", "inputSchema": {"type": "object", "properties": {"id": {"type": "string"}}}}
  ]
}`

	cases := []struct {
		name    string
		json    string
		wantErr string // substring; "" means load must succeed
	}{
		{name: "valid", json: valid, wantErr: ""},
		{
			name:    "invalid json",
			json:    `{"version": "x", "tools": [`,
			wantErr: "parse",
		},
		{
			name:    "empty tools",
			json:    `{"version": "livemcptool@test", "server_count": 0, "tool_count": 0, "tools": []}`,
			wantErr: "no tools",
		},
		{
			name:    "missing version",
			json:    `{"server_count": 1, "tool_count": 1, "tools": [{"server": "a", "tool": "t", "description": "d"}]}`,
			wantErr: "version",
		},
		{
			name:    "missing server",
			json:    `{"version": "livemcptool@test", "server_count": 1, "tool_count": 1, "tools": [{"server": "", "tool": "t", "description": "d"}]}`,
			wantErr: "record 0",
		},
		{
			name:    "missing tool name",
			json:    `{"version": "livemcptool@test", "server_count": 1, "tool_count": 1, "tools": [{"server": "a", "tool": "", "description": "d"}]}`,
			wantErr: "record 0",
		},
		{
			name: "duplicate server:tool",
			json: `{"version": "livemcptool@test", "server_count": 1, "tool_count": 2, "tools": [
				{"server": "a", "tool": "t", "description": "d"},
				{"server": "a", "tool": "t", "description": "d2"}
			]}`,
			wantErr: "duplicate",
		},
		{
			name: "tool_count mismatch",
			json: `{"version": "livemcptool@test", "server_count": 1, "tool_count": 5, "tools": [
				{"server": "a", "tool": "t", "description": "d"}
			]}`,
			wantErr: "tool_count",
		},
		{
			name: "server_count mismatch",
			json: `{"version": "livemcptool@test", "server_count": 3, "tool_count": 1, "tools": [
				{"server": "a", "tool": "t", "description": "d"}
			]}`,
			wantErr: "server_count",
		},
		{
			name: "invalid schema json",
			json: `{"version": "livemcptool@test", "server_count": 1, "tool_count": 1, "tools": [
				{"server": "a", "tool": "t", "description": "d", "inputSchema": {"type": }}
			]}`,
			wantErr: "parse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snap.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o644); err != nil {
				t.Fatal(err)
			}
			corpus, golden, reason, err := LoadLiveMCPTool(path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(corpus.Tools) != 3 {
					t.Errorf("tool count = %d, want 3", len(corpus.Tools))
				}
				// Schema-less records are legal in the format ("inputSchema
				// when present"); empty descriptions are legal too.
				if corpus.Tools[1].Description != "" {
					t.Errorf("expected empty description preserved, got %q", corpus.Tools[1].Description)
				}
				// Schemas are compacted at load (canonical in-memory bytes).
				if got := string(corpus.Tools[0].Schema); got != `{"type":"object"}` {
					t.Errorf("schema not compacted: %q", got)
				}
				if golden != nil || reason == "" {
					t.Errorf("golden = %v, reason = %q; want nil + non-empty reason", golden, reason)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestLoadLiveMCPToolMissingFile: a missing snapshot is an actionable error,
// not a panic.
func TestLoadLiveMCPToolMissingFile(t *testing.T) {
	_, _, _, err := LoadLiveMCPTool(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil {
		t.Fatal("expected error for missing snapshot file")
	}
}

// TestLiveMCPToolSchemalessRecord: a record without inputSchema loads with a
// nil schema (the normalized format carries inputSchema only when present).
func TestLiveMCPToolSchemalessRecord(t *testing.T) {
	snap := `{
  "version": "livemcptool@test",
  "server_count": 1,
  "tool_count": 1,
  "tools": [{"server": "a", "tool": "t", "description": "d"}]
}`
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := os.WriteFile(path, []byte(snap), 0o644); err != nil {
		t.Fatal(err)
	}
	corpus, _, _, err := LoadLiveMCPTool(path)
	if err != nil {
		t.Fatalf("LoadLiveMCPTool: %v", err)
	}
	if len(corpus.Tools[0].Schema) != 0 {
		t.Errorf("schema = %q, want empty", corpus.Tools[0].Schema)
	}
}
