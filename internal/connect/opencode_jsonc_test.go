package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Issue #922: recent OpenCode versions bootstrap ~/.config/opencode/opencode.jsonc
// (not opencode.json). mcpproxy must detect, read, and write the file OpenCode
// actually loads — preferring .jsonc, which shadows .json for the same keys.

func opencodeDir(home string) string {
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

func writeOpencodeFile(t *testing.T, home, name, content string) string {
	t.Helper()
	dir := opencodeDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const jsoncStub = "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"

func TestOpencodeConfigPathResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"no files -> default .json for create-new", nil, "opencode.json"},
		{"jsonc only -> jsonc", []string{"opencode.jsonc"}, "opencode.jsonc"},
		{"json only -> json", []string{"opencode.json"}, "opencode.json"},
		{"both -> jsonc (it shadows .json)", []string{"opencode.json", "opencode.jsonc"}, "opencode.jsonc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for _, f := range tc.files {
				writeOpencodeFile(t, home, f, jsoncStub)
			}
			s := NewServiceWithHome("127.0.0.1:8080", "key", home)
			got := s.configPath("opencode")
			if filepath.Base(got) != tc.want {
				t.Fatalf("configPath = %s, want basename %s", got, tc.want)
			}
			if filepath.Dir(got) != opencodeDir(home) {
				t.Fatalf("configPath dir = %s, want %s", filepath.Dir(got), opencodeDir(home))
			}
		})
	}
}

func TestConfigPathResolverLeavesOtherClientsAlone(t *testing.T) {
	home := t.TempDir()
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	if got, want := s.configPath("gemini"), ConfigPath("gemini", home); got != want {
		t.Fatalf("gemini configPath = %s, want %s", got, want)
	}
}

func TestUnmarshalLenientJSONComments(t *testing.T) {
	raw := []byte(`{
  // line comment
  "$schema": "https://opencode.ai/config.json", /* block comment */
  "note": "a // string with slashes and /* not a comment */",
  "mcp": {
    "x": { "url": "http://example/mcp", },
  }
}`)
	var data map[string]interface{}
	if err := unmarshalLenientJSON(raw, &data); err != nil {
		t.Fatalf("unmarshalLenientJSON: %v", err)
	}
	if data["note"] != "a // string with slashes and /* not a comment */" {
		t.Fatalf("string content mangled: %q", data["note"])
	}
	if _, ok := data["mcp"].(map[string]interface{})["x"]; !ok {
		t.Fatal("nested key lost")
	}
}

func TestConnectOpencodeJsoncOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	jsoncPath := writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	res, err := s.Connect("opencode", "mcpproxy", false)
	if err != nil {
		t.Fatalf("Connect on a .jsonc-only install must succeed: %v", err)
	}
	if !res.Success {
		t.Fatalf("Connect not successful: %+v", res)
	}
	if res.ConfigPath != jsoncPath {
		t.Fatalf("wrote to %s, want the existing %s", res.ConfigPath, jsoncPath)
	}
	// The entry must land INSIDE the .jsonc; no stray opencode.json created.
	if _, err := os.Stat(filepath.Join(opencodeDir(home), "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("a stray opencode.json was created next to opencode.jsonc")
	}
	raw, _ := os.ReadFile(jsoncPath)
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("rewritten .jsonc is not valid JSON: %v", err)
	}
	mcp, _ := data["mcp"].(map[string]interface{})
	if _, ok := mcp["mcpproxy"]; !ok {
		t.Fatalf("mcp.mcpproxy entry missing in %s: %s", jsoncPath, raw)
	}
	if data["$schema"] != "https://opencode.ai/config.json" {
		t.Fatal("$schema stub key lost on rewrite")
	}

	// Detection: status must report the client as installed + connected.
	st, err := s.GetStatus("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || !st.Connected {
		t.Fatalf("GetStatus after connect: Exists=%v Connected=%v, want true/true", st.Exists, st.Connected)
	}

	// Disconnect must also target the .jsonc.
	dres, err := s.Disconnect("opencode", "mcpproxy")
	if err != nil || !dres.Success {
		t.Fatalf("Disconnect: %v %+v", err, dres)
	}
}

func TestGetAllStatusSeesJsoncOnlyInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	for _, st := range s.GetAllStatus() {
		if st.ID == "opencode" {
			if !st.Exists {
				t.Fatal("GetAllStatus: opencode .jsonc-only install reported as not installed")
			}
			if filepath.Base(st.ConfigPath) != "opencode.jsonc" {
				t.Fatalf("GetAllStatus ConfigPath = %s, want the existing opencode.jsonc", st.ConfigPath)
			}
			return
		}
	}
	t.Fatal("opencode not in GetAllStatus")
}

func TestConnectOpencodeCommentedJsoncRefusedSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	content := "{\n  // keep my comments\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"
	p := writeOpencodeFile(t, home, "opencode.jsonc", content)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	_, err := s.Connect("opencode", "mcpproxy", false)
	if err == nil {
		t.Fatal("Connect must refuse to rewrite a commented .jsonc (comments would be lost)")
	}
	if !strings.Contains(err.Error(), "comment") {
		t.Fatalf("error should explain the comment refusal, got: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(after) != content {
		t.Fatal("commented .jsonc was modified despite the refusal")
	}
}

func TestUnmarshalLenientJSONEdgeCases(t *testing.T) {
	t.Run("unterminated block comment is an error, not silently valid", func(t *testing.T) {
		var data map[string]interface{}
		if err := unmarshalLenientJSON([]byte(`{"mcp":{}} /* unterminated`), &data); err == nil {
			t.Fatal("unterminated /* must not parse as valid JSONC")
		}
	})
	t.Run("escaped quote inside string does not end string state", func(t *testing.T) {
		var data map[string]interface{}
		raw := []byte(`{"k": "quote \" then // not a comment", "n": 1}`)
		if err := unmarshalLenientJSON(raw, &data); err != nil {
			t.Fatalf("escaped-quote input failed: %v", err)
		}
		if data["k"] != `quote " then // not a comment` {
			t.Fatalf("string mangled: %q", data["k"])
		}
	})
	t.Run("line comment at EOF without newline", func(t *testing.T) {
		var data map[string]interface{}
		if err := unmarshalLenientJSON([]byte("{\"n\": 1} // trailing"), &data); err != nil {
			t.Fatalf("EOF line comment failed: %v", err)
		}
	})
}

func TestDisconnectOpencodeCommentedJsoncRefusedWithoutBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	content := "{\n  // comments\n  \"mcp\": {\"mcpproxy\": {\"type\": \"remote\", \"url\": \"http://127.0.0.1:8080/mcp\"}}\n}\n"
	writeOpencodeFile(t, home, "opencode.jsonc", content)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	_, err := s.Disconnect("opencode", "mcpproxy")
	if err == nil {
		t.Fatal("Disconnect must refuse a commented .jsonc")
	}
	entries, _ := os.ReadDir(opencodeDir(home))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Fatalf("refusal must not leave a backup behind, found %s", e.Name())
		}
	}
}

func TestDisconnectOpencodeFindsEntryInOtherCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	// Entry written into opencode.json first; opencode.jsonc bootstrapped later
	// (resolver now prefers it). Disconnect must still find and remove the entry.
	writeOpencodeFile(t, home, "opencode.json",
		`{"mcp":{"mcpproxy":{"type":"remote","url":"http://127.0.0.1:8080/mcp","enabled":true}}}`)
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	res, err := s.Disconnect("opencode", "mcpproxy")
	if err != nil || !res.Success {
		t.Fatalf("Disconnect across candidates: err=%v res=%+v", err, res)
	}
	raw, _ := os.ReadFile(filepath.Join(opencodeDir(home), "opencode.json"))
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if mcp, _ := data["mcp"].(map[string]interface{}); mcp != nil {
		if _, still := mcp["mcpproxy"]; still {
			t.Fatal("entry still present in opencode.json after cross-candidate disconnect")
		}
	}
}

func TestDisconnectOpencodeAlternateCandidateErrorPropagates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	// Resolved target: comment-free .jsonc without the entry. Alternate: a
	// commented .json... comments only guard .jsonc, so use malformed JSON to
	// force a real error on the alternate path instead.
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)
	writeOpencodeFile(t, home, "opencode.json", `{"mcp": not-valid-json`)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	_, err := s.Disconnect("opencode", "mcpproxy")
	if err == nil {
		t.Fatal("an unreadable/malformed alternate candidate must surface an error, not a silent not_found")
	}
}

func TestUndoOpencodeBackupTargetsItsOwnFileAfterDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	writeOpencodeFile(t, home, "opencode.json", `{"theme":"dark"}`)
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)

	res, err := s.Connect("opencode", "mcpproxy", false)
	if err != nil || !res.Success || res.BackupPath == "" {
		t.Fatalf("connect: err=%v res=%+v", err, res)
	}
	backupName := filepath.Base(res.BackupPath)

	// OpenCode upgrade bootstraps a .jsonc — resolver drift.
	writeOpencodeFile(t, home, "opencode.jsonc", jsoncStub)

	ures, err := s.Undo("opencode", "mcpproxy", backupName)
	if err != nil || !ures.Success {
		t.Fatalf("undo after drift must accept the opencode.json backup: err=%v res=%+v", err, ures)
	}
	raw, _ := os.ReadFile(filepath.Join(opencodeDir(home), "opencode.json"))
	if string(raw) != `{"theme":"dark"}` {
		t.Fatalf("opencode.json not restored to pre-connect content: %s", raw)
	}
	jsoncRaw, _ := os.ReadFile(filepath.Join(opencodeDir(home), "opencode.jsonc"))
	if string(jsoncRaw) != jsoncStub {
		t.Fatal("undo must not touch the unrelated opencode.jsonc")
	}
}

func TestConnectOpencodeNoConfigMentionsBothCandidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	}
	home := t.TempDir()
	s := NewServiceWithHome("127.0.0.1:8080", "key", home)
	_, err := s.Connect("opencode", "mcpproxy", false)
	if err == nil {
		t.Fatal("Connect with no OpenCode install must still refuse")
	}
	if !strings.Contains(err.Error(), "opencode.jsonc") || !strings.Contains(err.Error(), "opencode.json") {
		t.Fatalf("refusal should name both probed files, got: %v", err)
	}
}
