package bench

import (
	"os"
	"path/filepath"
	"testing"
)

// The fixture bench/testdata/lap_sample.json is a REAL artifact captured on
// 2026-07-14 by running the pinned linter against a live local mcpproxy:
//
//	uvx --from 'lap-score[mcp]==0.8.0' lap lint \
//	    --mcp-url http://127.0.0.1:8080/mcp --json > bench/testdata/lap_sample.json
//
// Its shape matches the MCP branch of lap/lint.py in lap-score 0.8.0 (keys:
// api, source, grade{score,letter,subscores}, menu_tokens, heaviest_tools,
// next_grade, findings, warnings, suggestions). See the comment on
// ParseLapJSON in lapcheck.go.
const lapSamplePath = "testdata/lap_sample.json"

func TestParseLapJSONSample(t *testing.T) {
	v := ParseLapJSON(lapSamplePath)

	if !v.Executed {
		t.Fatalf("Executed = false (skip reason %q), want true", v.SkipReason)
	}
	if v.SkipReason != "" {
		t.Errorf("SkipReason = %q, want empty on success", v.SkipReason)
	}
	if v.Version != LapPinnedVersion {
		t.Errorf("Version = %q, want %q", v.Version, LapPinnedVersion)
	}
	// Values pinned in the committed fixture (11-tool mcpproxy builtin menu).
	if v.MenuTokens != 4741 {
		t.Errorf("MenuTokens = %d, want 4741", v.MenuTokens)
	}
	if v.Grade != "C" {
		t.Errorf("Grade = %q, want \"C\"", v.Grade)
	}
	if v.ArtifactPath != lapSamplePath {
		t.Errorf("ArtifactPath = %q, want %q", v.ArtifactPath, lapSamplePath)
	}
}

func TestParseLapJSONFailures(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name string
		path string
	}{
		{"missing file", filepath.Join(dir, "does-not-exist.json")},
		{"corrupt JSON", write("corrupt.json", "{ not json !!")},
		{"empty file", write("empty.json", "")},
		{"valid JSON, no menu_tokens", write("nomenu.json", `{"api":"MCP server","grade":{"letter":"A","score":95}}`)},
		{"zero menu_tokens", write("zeromenu.json", `{"menu_tokens":0,"grade":{"letter":"A","score":95}}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := ParseLapJSON(c.path)
			if v.Executed {
				t.Fatalf("Executed = true, want false")
			}
			if v.SkipReason == "" {
				t.Errorf("SkipReason empty, want a reason")
			}
			if v.MenuTokens != 0 || v.Grade != "" {
				t.Errorf("skipped verdict carries data: MenuTokens=%d Grade=%q", v.MenuTokens, v.Grade)
			}
		})
	}
}

func TestLapVerdictCompare(t *testing.T) {
	cases := []struct {
		name     string
		executed bool
		lap      int // LAP's menu_tokens
		inHouse  int
		wantPct  float64
		wantWarn bool
	}{
		{"exact agreement", true, 4741, 4741, 0, false},
		{"small divergence within tolerance", true, 5000, 4741, 100 * (5000.0 - 4741.0) / 4741.0, false},
		{"exactly at +15% is not beyond", true, 1150, 1000, 15.0, false},
		{"beyond +15% warns", true, 1151, 1000, 15.1, true},
		{"beyond -15% warns", true, 800, 1000, -20.0, true},
		{"not executed: no comparison", false, 0, 4741, 0, false},
		{"in-house zero: no comparison", true, 4741, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := LapVerdict{Executed: c.executed, MenuTokens: c.lap}
			pct, warn := v.Compare(c.inHouse)
			if !almostEqual(pct, c.wantPct) {
				t.Errorf("Compare pct = %v, want %v", pct, c.wantPct)
			}
			if warn != c.wantWarn {
				t.Errorf("Compare warn = %v, want %v", warn, c.wantWarn)
			}
			if !almostEqual(v.DivergencePct, c.wantPct) {
				t.Errorf("v.DivergencePct = %v, want %v (Compare must record it)", v.DivergencePct, c.wantPct)
			}
			if c.executed && c.inHouse > 0 && v.InHouseMenuTokens != c.inHouse {
				t.Errorf("v.InHouseMenuTokens = %d, want %d", v.InHouseMenuTokens, c.inHouse)
			}
		})
	}
}

// Compare on the parsed committed fixture: mcpproxy's own count of the same
// 11-tool surface should land within tolerance (both sides use cl100k_base).
func TestLapCompareOnSample(t *testing.T) {
	v := ParseLapJSON(lapSamplePath)
	if !v.Executed {
		t.Fatalf("sample did not parse: %s", v.SkipReason)
	}
	pct, warn := v.Compare(4600) // ~3% under LAP's 4741
	if warn {
		t.Errorf("warn = true for %.2f%% divergence, want false (tolerance ±%v%%)", pct, LapDivergenceTolerancePct)
	}
	pct, warn = v.Compare(1000) // wildly off
	if !warn {
		t.Errorf("warn = false for %.2f%% divergence, want true", pct)
	}
}
