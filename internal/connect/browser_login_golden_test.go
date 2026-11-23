//go:build !server

package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With browser login off the connect payloads are byte for byte what they were
// before Spec 132 PR-3. testdata/feature_off_payloads.golden.json was recorded
// from the code of origin/feat/client-oauth-login-tray (set GOLDEN_RECORD=<path>
// to record; never re-record from this branch).
func goldenPayloads(t *testing.T) []byte {
	t.Helper()
	svc, home := testService(t)
	claude := ConfigPath("claude-code", home)
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(`{"mcpServers":{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp"}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedClientConfig(t, home, "cursor")

	claudeStatus, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	cursorStatus, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatal(err)
	}
	claudePreview, err := svc.PreviewWithIntent("claude-code", "", CredentialIntent{})
	if err != nil {
		t.Fatal(err)
	}
	cursorPreview, err := svc.PreviewWithIntent("cursor", "", CredentialIntent{})
	if err != nil {
		t.Fatal(err)
	}
	claudePreview.PreconditionToken, cursorPreview.PreconditionToken = "<token>", "<token>"
	raw, err := json.MarshalIndent(map[string]interface{}{
		"list":           svc.GetAllStatus(),
		"claude_status":  claudeStatus,
		"cursor_status":  cursorStatus,
		"claude_preview": claudePreview,
		"cursor_preview": cursorPreview,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(raw), home, "<HOME>"))
}

func TestFeatureOffPayloadsAreUnchanged(t *testing.T) {
	got := goldenPayloads(t)
	if p := os.Getenv("GOLDEN_RECORD"); p != "" {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filepath.Join("testdata", "feature_off_payloads.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(string(got), "\n") != strings.TrimRight(string(want), "\n") { // the end-of-file hook adds a final newline
		t.Fatalf("feature-off payloads changed.\n--- want\n%s\n--- got\n%s", want, got)
	}
}
