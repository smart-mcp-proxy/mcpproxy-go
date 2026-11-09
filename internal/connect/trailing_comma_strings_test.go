package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The trailing-comma cleanup must not touch ", }" / ", ]" inside string
// values: connect writes the parsed document back, so a rewritten shell
// snippet in another server's args would silently change on disk.
func TestConnect_TrailingCommaCleanupPreservesStringContents(t *testing.T) {
	const snippet = `echo {a, } [b, ]`
	svc, home := testService(t)
	cfgPath := ConfigPath("opencode", home)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{
	  "mcp": {
	    "other": {"type": "local", "command": ["sh", "-c", "echo {a, } [b, ]"]},
	  },
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if res, err := svc.Connect("opencode", "mcpproxy", false); err != nil || !res.Success {
		t.Fatalf("connect: %+v, %v", res, err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		MCP map[string]struct {
			Command []string `json:"command"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	cmd := data.MCP["other"].Command
	if len(cmd) != 3 || cmd[2] != snippet {
		t.Fatalf("other server's command rewritten: got %q, want [sh -c %q]", cmd, snippet)
	}
}
