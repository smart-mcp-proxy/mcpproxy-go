package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

// ACT-02: after `upstream add`, a quarantined server's output and the command
// help must name the CLI commands that review and admit it, so a terminal user
// never has to leave the CLI to find the next step.
func TestQuarantineNextStepsNamesReviewAndApprove(t *testing.T) {
	for _, daemon := range []bool{true, false} {
		var buf bytes.Buffer
		printQuarantineNextSteps(&buf, "fs", daemon)
		out := buf.String()
		for _, want := range []string{
			"mcpproxy review fetch fs",
			"mcpproxy review show fs",
			"mcpproxy security scan fs",
			"mcpproxy security report fs",
			"mcpproxy review approve fs",
			"mcpproxy security approve fs",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("daemon=%v: next steps missing %q:\n%s", daemon, want, out)
			}
		}
		if strings.Contains(out, "web UI") && !strings.Contains(out, "mcpproxy review") {
			t.Errorf("daemon=%v: still web-UI only", daemon)
		}
		if got := strings.Contains(out, "mcpproxy serve"); got == daemon {
			t.Errorf("daemon=%v: 'mcpproxy serve' hint presence = %v", daemon, got)
		}
	}
}

func TestUpstreamAddHelpNamesReviewCommands(t *testing.T) {
	for _, want := range []string{"mcpproxy review show", "mcpproxy security scan", "mcpproxy security report", "mcpproxy review approve", "mcpproxy security approve"} {
		if !strings.Contains(upstreamAddCmd.Long, want) {
			t.Errorf("upstream add --help missing %q", want)
		}
	}
}

// The daemon's POST /servers response is a ServerActionResponse without a
// "quarantined" field, so the old `result.Quarantined` check never fired and a
// daemon-mode add printed no hint at all. The CLI must read the server's real
// state from the daemon after the add.
func TestUpstreamAddDaemonModePrintsNextStepsForQuarantinedServer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		listRow  string
		wantHint bool
	}{
		{"quarantined by admin_state", `{"name":"fx","quarantined":true,"health":{"admin_state":"quarantined","status":"needs_review"}}`, true},
		{"needs_review without flag", `{"name":"fx","health":{"status":"needs_review"}}`, true},
		{"auto-admitted (no hint)", `{"name":"fx","quarantined":false,"health":{"admin_state":"enabled","status":"healthy"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/servers":
					_, _ = w.Write([]byte(`{"success":true,"data":{"server":"fx","action":"add","success":true}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/servers":
					_, _ = w.Write([]byte(`{"success":true,"data":{"servers":[` + tc.listRow + `]}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			oldFormat, oldJSON := globalOutputFormat, globalJSONOutput
			globalOutputFormat, globalJSONOutput = "table", false
			defer func() { globalOutputFormat, globalJSONOutput = oldFormat, oldJSON }()

			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w
			client := cliclient.NewClient(ts.URL, nil)
			added, err := runUpstreamAddDaemonMode(context.Background(), client, &cliclient.AddServerRequest{Name: "fx", Command: "x", Protocol: "stdio"})
			w.Close()
			os.Stdout = oldStdout
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			if err != nil || !added {
				t.Fatalf("added=%v err=%v", added, err)
			}
			out := buf.String()
			if got := strings.Contains(out, "mcpproxy review approve fx"); got != tc.wantHint {
				t.Errorf("next-steps hint present=%v want %v:\n%s", got, tc.wantHint, out)
			}
		})
	}
}
