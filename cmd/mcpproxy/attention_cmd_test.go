package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

func sampleAttentionResponse() *cliclient.AttentionResponse {
	return &cliclient.AttentionResponse{
		Count:       2,
		GeneratedAt: "2026-09-25T06:12:03Z",
		Items: []contracts.AttentionItem{
			{
				ID:      "sign_in_required:server:github",
				Kind:    "sign_in_required",
				Rank:    10,
				Subject: contracts.AttentionSubject{Type: "server", ID: "github", Name: "github"},
				Summary: "github: sign in required",
				Fix:     contracts.AttentionFix{Verb: "login", Label: "Sign in", Target: "/servers/github"},
			},
			{
				ID:      "server_review:server:github",
				Kind:    "server_review",
				Rank:    50,
				Subject: contracts.AttentionSubject{Type: "server", ID: "github", Name: "github"},
				Summary: "github: waiting for review",
				Fix:     contracts.AttentionFix{Verb: "review", Label: "Review", Target: "/review/github"},
			},
		},
	}
}

// TestAttentionCmd_TableOutput pins T056: `mcpproxy attention` table columns
// are exactly `#  KIND  SUBJECT  SUMMARY  FIX` (contracts/cli.md).
func TestAttentionCmd_TableOutput(t *testing.T) {
	setOutputGlobals(t, "table", false)
	output := captureOutput(func() {
		if err := printAttentionOutput(sampleAttentionResponse()); err != nil {
			t.Fatalf("printAttentionOutput: %v", err)
		}
	})

	for _, want := range []string{"KIND", "SUBJECT", "SUMMARY", "FIX", "sign_in_required", "github", "Sign in", "server_review", "Review"} {
		if !strings.Contains(output, want) {
			t.Errorf("table output missing %q, got:\n%s", want, output)
		}
	}
}

// TestAttentionCmd_JSONOutput pins T056: `-o json` round-trips the exact
// contracts/rest-api.md#attention shape.
func TestAttentionCmd_JSONOutput(t *testing.T) {
	setOutputGlobals(t, "json", false)
	output := captureOutput(func() {
		if err := printAttentionOutput(sampleAttentionResponse()); err != nil {
			t.Fatalf("printAttentionOutput: %v", err)
		}
	})

	var decoded cliclient.AttentionResponse
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("json output did not parse: %v\noutput: %s", err, output)
	}
	if decoded.Count != 2 || len(decoded.Items) != 2 {
		t.Errorf("unexpected decoded response: %+v", decoded)
	}
	if decoded.Items[0].Fix.Verb != "login" {
		t.Errorf("expected first item's fix verb 'login', got %q", decoded.Items[0].Fix.Verb)
	}
}

// TestAttentionCmd_AllClear pins T056: an empty list prints "All clear" and
// the command still succeeds (it is a report, not a check — FR-001).
func TestAttentionCmd_AllClear(t *testing.T) {
	setOutputGlobals(t, "table", false)
	output := captureOutput(func() {
		if err := printAttentionOutput(&cliclient.AttentionResponse{Count: 0, Items: []contracts.AttentionItem{}}); err != nil {
			t.Fatalf("printAttentionOutput: %v", err)
		}
	})
	if strings.TrimSpace(output) != "All clear" {
		t.Errorf("expected exactly %q, got %q", "All clear", strings.TrimSpace(output))
	}
}

// TestStatusTable_NeedsAttentionFirstLine pins T056/contracts/cli.md: the
// first line after the "MCPProxy Status" header is the FR-001 attention
// count, in both the non-zero and "none" shapes.
func TestStatusTable_NeedsAttentionFirstLine(t *testing.T) {
	nonZero := 3
	output := captureOutput(func() {
		printStatusTable(&StatusInfo{State: "Running", Edition: "personal", NeedsAttention: &nonZero})
	})
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 || lines[0] != "MCPProxy Status" {
		t.Fatalf("expected header first, got: %q", lines)
	}
	if lines[1] != "Needs attention: 3 (run 'mcpproxy attention')" {
		t.Errorf("unexpected second line: %q", lines[1])
	}

	zero := 0
	output = captureOutput(func() {
		printStatusTable(&StatusInfo{State: "Running", Edition: "personal", NeedsAttention: &zero})
	})
	lines = strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 || lines[1] != "Needs attention: none" {
		t.Errorf("unexpected second line for zero count: %q", lines)
	}

	// An older daemon / unreachable endpoint (nil) omits the line entirely
	// rather than lying with a count of 0.
	output = captureOutput(func() {
		printStatusTable(&StatusInfo{State: "Running", Edition: "personal"})
	})
	if strings.Contains(output, "Needs attention") {
		t.Errorf("expected no attention line when NeedsAttention is nil, got: %q", output)
	}
}

// TestDoctorAttentionSection_FirstAndDiagnosticsWording pins T056: doctor's
// first section is "Needs attention (N)" from GET /attention, and the old
// "Found N issues that need attention" line is now "Diagnostics: N findings"
// (FR-004 — "need attention" refers only to the FR-001 count).
func TestDoctorAttentionSection_FirstAndDiagnosticsWording(t *testing.T) {
	doctorOutput = "pretty"
	diag := map[string]interface{}{
		"total_issues":   1,
		"oauth_required": []interface{}{"github"},
	}
	output := captureOutput(func() {
		if err := outputDiagnostics(diag, nil, nil, "", sampleAttentionResponse()); err != nil {
			t.Fatalf("outputDiagnostics: %v", err)
		}
	})

	attentionIdx := strings.Index(output, "Needs attention (2)")
	diagnosticsIdx := strings.Index(output, "Diagnostics: 1 findings")
	if attentionIdx == -1 {
		t.Fatalf("missing 'Needs attention (2)' section, got:\n%s", output)
	}
	if diagnosticsIdx == -1 {
		t.Fatalf("missing 'Diagnostics: 1 findings' line, got:\n%s", output)
	}
	if attentionIdx > diagnosticsIdx {
		t.Errorf("attention section must come first, got attention@%d diagnostics@%d", attentionIdx, diagnosticsIdx)
	}
	if strings.Contains(output, "Found 1") {
		t.Errorf("stale 'Found N issues' wording must not appear, got:\n%s", output)
	}
}
