package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 109-m SC-002 (T145, M6): the CLI leg of the attention parity chain. An
// httptest daemon serves the REST golden
// internal/runtime/testdata/attention_parity_rest.json; `attention -o json`
// gives the expected ids in order, the first line of `status` gives the count,
// and `doctor`'s first section lists the same items in the same order.

type p109AttentionFixtureIDs struct {
	ExpectedIDs []string `json:"expected_ids"`
}

func p109AttentionGoldenServer(t *testing.T) (*cliclient.Client, []string, string) {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "runtime", "testdata")
	golden, err := os.ReadFile(filepath.Join(root, "attention_parity_rest.json"))
	if err != nil {
		t.Fatalf("missing golden (run the httpapi parity test with UPDATE_GOLDEN=1): %v", err)
	}
	fixture, err := os.ReadFile(filepath.Join(root, "attention_parity_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ids p109AttentionFixtureIDs
	if err := json.Unmarshal(fixture, &ids); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/attention" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"success":true,"data":%s}`, golden)
	}))
	t.Cleanup(srv.Close)
	return cliclient.NewClient(srv.URL, nil), ids.ExpectedIDs, string(golden)
}

func TestAttentionParityCLI(t *testing.T) {
	client, expected, _ := p109AttentionGoldenServer(t)
	resp, err := client.GetAttention(context.Background())
	if err != nil {
		t.Fatalf("GetAttention: %v", err)
	}

	// attention -o json: the ids, in order.
	setOutputGlobals(t, "json", false)
	out := captureOutput(func() {
		if err := printAttentionOutput(resp); err != nil {
			t.Fatalf("printAttentionOutput: %v", err)
		}
	})
	var decoded struct {
		Count int                       `json:"count"`
		Items []contracts.AttentionItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("attention -o json did not parse: %v\n%s", err, out)
	}
	var ids []string
	for _, it := range decoded.Items {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != strings.Join(expected, ",") {
		t.Errorf("attention -o json ids = %v, want %v", ids, expected)
	}
	if decoded.Count != len(expected) {
		t.Errorf("attention count = %d, want %d", decoded.Count, len(expected))
	}

	// status: the first line after the header carries the same count.
	count := resp.Count
	statusOut := captureOutput(func() {
		printStatusTable(&StatusInfo{State: "Running", Edition: "personal", NeedsAttention: &count})
	})
	lines := strings.Split(strings.TrimRight(statusOut, "\n"), "\n")
	wantLine := fmt.Sprintf("Needs attention: %d (run 'mcpproxy attention')", len(expected))
	if len(lines) < 2 || lines[1] != wantLine {
		t.Errorf("status second line = %q, want %q", lines, wantLine)
	}

	// doctor: the first section lists the same items in the same order.
	doctorOutput = "pretty"
	doctorOut := captureOutput(func() {
		if err := outputDiagnostics(map[string]interface{}{"total_issues": 1, "oauth_required": []interface{}{"github-oauth"}}, nil, nil, "", resp); err != nil {
			t.Fatalf("outputDiagnostics: %v", err)
		}
	})
	header := fmt.Sprintf("Needs attention (%d)", len(expected))
	start := strings.Index(doctorOut, header)
	if start < 0 {
		t.Fatalf("doctor output lacks %q:\n%s", header, doctorOut)
	}
	if diag := strings.Index(doctorOut, "Diagnostics:"); diag >= 0 && diag < start {
		t.Errorf("the needs-attention section must come before the diagnostics sections")
	}
	pos := start
	for _, item := range decoded.Items {
		line := fmt.Sprintf("[%s] %s (%s)", item.Kind, item.Summary, item.Fix.Label)
		idx := strings.Index(doctorOut[pos:], line)
		if idx < 0 {
			t.Fatalf("doctor is missing or reorders %q (searching after offset %d):\n%s", line, pos, doctorOut)
		}
		pos += idx + len(line)
	}
}
