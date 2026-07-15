// LAP independent verdict (Spec 083 US4, FR-015/016, SC-006).
//
// CI runs the version-pinned external linter against the booted proxy:
//
//	uvx --from 'lap-score[mcp]==0.8.0' lap lint --mcp-url "$PROXY/mcp" --json > lap.json
//
// and this file turns that artifact into the report's LapVerdict: parse the
// JSON, extract LAP's menu-token count (bucket A) and letter grade, and
// compare against the in-house count for the same surface. LAP being absent
// or broken must never fail the benchmark (FR-015): every failure path yields
// Executed=false with a skip reason instead of an error.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
)

// LapPinnedVersion is the lap-score release the CI step installs (research
// D9). The lint artifact itself carries no version field, so the pin is
// recorded here and stamped into the verdict on successful parse.
const LapPinnedVersion = "0.8.0"

// LapDivergenceTolerancePct is the documented tolerance (FR-016) between
// LAP's menu-token count and the in-house count of the same surface. Both use
// cl100k_base, but LAP frames the menu its own way, so small divergence is
// expected; beyond ±15% the report shows a (non-blocking) warning.
const LapDivergenceTolerancePct = 15.0

// lapLintOutput mirrors the fields we consume from `lap lint --mcp-url --json`
// (lap-score 0.8.0, lap/lint.py MCP branch). Shape verified against the real
// tool on 2026-07-14 — see bench/testdata/lap_sample.json, captured from a
// live mcpproxy, and the capture command in lapcheck_test.go. The full output
// also carries api/source/heaviest_tools/next_grade/findings/warnings/
// suggestions, which the verdict does not need.
type lapLintOutput struct {
	Grade      *lapGrade `json:"grade"`
	MenuTokens int       `json:"menu_tokens"`
}

// lapGrade is LAP's composite grade object: {"score": 60, "letter": "C",
// "subscores": {...}}.
type lapGrade struct {
	Score  int    `json:"score"`
	Letter string `json:"letter"`
}

// ParseLapJSON reads a LAP lint artifact from disk and returns the verdict.
// It never returns an error: a missing, unreadable, corrupt, or
// menu-token-free artifact yields Executed=false with a SkipReason (SC-006
// requires an explicit skip reason over a silent absence).
func ParseLapJSON(path string) LapVerdict {
	skip := func(format string, args ...any) LapVerdict {
		return LapVerdict{Executed: false, SkipReason: fmt.Sprintf(format, args...)}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return skip("LAP artifact unreadable: %v", err)
	}
	var out lapLintOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return skip("LAP artifact %q is not valid JSON: %v", path, err)
	}
	if out.MenuTokens <= 0 {
		return skip("LAP artifact %q has no positive menu_tokens (got %d)", path, out.MenuTokens)
	}

	v := LapVerdict{
		Executed:     true,
		Version:      LapPinnedVersion,
		MenuTokens:   out.MenuTokens,
		ArtifactPath: path,
	}
	if out.Grade != nil {
		v.Grade = out.Grade.Letter
	}
	return v
}

// Compare records the in-house menu-token count for the same proxy surface on
// the verdict and computes the divergence of LAP's count from it, in percent
// (positive = LAP counted more). It returns the divergence and whether it
// exceeds LapDivergenceTolerancePct (FR-016 warning; non-blocking). When the
// verdict was not executed or the in-house count is not positive there is
// nothing to compare: divergence is 0 and no warning is raised.
func (v *LapVerdict) Compare(inHouseMenuTokens int) (divergencePct float64, warn bool) {
	if !v.Executed || inHouseMenuTokens <= 0 {
		v.DivergencePct = 0
		return 0, false
	}
	v.InHouseMenuTokens = inHouseMenuTokens
	divergencePct = 100 * float64(v.MenuTokens-inHouseMenuTokens) / float64(inHouseMenuTokens)
	v.DivergencePct = divergencePct
	warn = divergencePct > LapDivergenceTolerancePct || divergencePct < -LapDivergenceTolerancePct
	return divergencePct, warn
}
