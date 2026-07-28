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
