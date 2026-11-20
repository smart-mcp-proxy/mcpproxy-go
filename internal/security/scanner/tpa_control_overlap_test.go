package scanner

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// TestBundleControl_NoProductionBuiltinHardFinding: the D9 control sample
// (TPA-2026-0044) must not be hard-flagged by the production built-in checks
// alone, i.e. baselineEngineChecks() without the bundle. If a built-in ever
// catches it, removing the control signature would no longer breach the gate and
// the non-vacuity proof (cmd/scan-eval bundle_control_test.go) would be hollow.
func TestBundleControl_NoProductionBuiltinHardFinding(t *testing.T) {
	data, err := os.ReadFile("../../../cmd/scan-eval/testdata/bundle_control_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Entries []struct {
			ID     string `json:"id"`
			Label  string `json:"label"`
			TPAID  string `json:"tpa_id"`
			Server string `json:"server"`
			Tool   struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tool"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range c.Entries {
		if e.Label != "malicious" {
			continue
		}
		checked++
		view := detect.ToolView{Server: e.Server, Name: e.Tool.Name, Description: e.Tool.Description}
		res := detect.NewEngine(detect.Options{Checks: baselineEngineChecks()}).Scan(detect.NewRegistryView([]detect.ToolView{view}))
		for _, f := range res.Findings {
			if f.ThreatLevel == detect.ThreatLevelDangerous {
				t.Errorf("%s: production built-ins hard-flag the control sample (signals %v)", e.ID, f.Signals)
			}
		}
	}
	if checked != 1 {
		t.Fatalf("control corpus has %d malicious entries, want exactly 1", checked)
	}
}
