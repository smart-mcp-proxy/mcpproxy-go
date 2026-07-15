package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestVersionCommandTableOutput(t *testing.T) {
	t.Setenv("MCPPROXY_OUTPUT", "")

	cmd := GetVersionCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	want := fmt.Sprintf("MCPProxy %s (%s) %s/%s\n", version, Edition, runtime.GOOS, runtime.GOARCH)
	if got := buf.String(); got != want {
		t.Errorf("table output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestPrintVersionOutputJSON(t *testing.T) {
	var buf bytes.Buffer
	info := VersionInfo{Version: "v1.2.3", Edition: "personal", OS: "darwin", Arch: "arm64"}

	if err := printVersionOutput(&buf, info, "json"); err != nil {
		t.Fatalf("printVersionOutput() error: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	expected := map[string]string{
		"version": "v1.2.3",
		"edition": "personal",
		"os":      "darwin",
		"arch":    "arm64",
	}
	for key, want := range expected {
		if got, ok := decoded[key]; !ok || got != want {
			t.Errorf("JSON key %q = %q, want %q", key, got, want)
		}
	}

	raw := buf.String()
	if strings.Contains(raw, `"commit"`) {
		t.Errorf("JSON output should omit empty commit, got: %s", raw)
	}
	if strings.Contains(raw, `"date"`) {
		t.Errorf("JSON output should omit empty date, got: %s", raw)
	}
}

func TestPrintVersionOutputJSONWithBuildMeta(t *testing.T) {
	var buf bytes.Buffer
	info := VersionInfo{
		Version: "v1.2.3",
		Edition: "server",
		OS:      "linux",
		Arch:    "amd64",
		Commit:  "abc1234",
		Date:    "2026-07-15T00:00:00Z",
	}

	if err := printVersionOutput(&buf, info, "json"); err != nil {
		t.Fatalf("printVersionOutput() error: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	if decoded["commit"] != "abc1234" {
		t.Errorf("JSON commit = %q, want %q", decoded["commit"], "abc1234")
	}
	if decoded["date"] != "2026-07-15T00:00:00Z" {
		t.Errorf("JSON date = %q, want %q", decoded["date"], "2026-07-15T00:00:00Z")
	}
}

func TestPrintVersionOutputCaseInsensitiveFormat(t *testing.T) {
	var buf bytes.Buffer
	info := VersionInfo{Version: "v1.2.3", Edition: "personal", OS: "darwin", Arch: "arm64"}

	if err := printVersionOutput(&buf, info, "JSON"); err != nil {
		t.Fatalf("printVersionOutput(JSON) error: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("-o JSON should produce JSON output, got: %s (err: %v)", buf.String(), err)
	}
	if decoded["version"] != "v1.2.3" {
		t.Errorf("JSON version = %q, want %q", decoded["version"], "v1.2.3")
	}
}

func TestPrintVersionOutputRejectsUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	info := VersionInfo{Version: "v1.2.3", Edition: "personal", OS: "darwin", Arch: "arm64"}

	err := printVersionOutput(&buf, info, "xml")
	if err == nil {
		t.Fatalf("printVersionOutput(xml) should return an error, got output: %s", buf.String())
	}
	if !strings.Contains(err.Error(), "xml") {
		t.Errorf("error should name the invalid format, got: %v", err)
	}
}

func TestPrintVersionOutputYAML(t *testing.T) {
	var buf bytes.Buffer
	info := VersionInfo{Version: "v1.2.3", Edition: "personal", OS: "darwin", Arch: "arm64"}

	if err := printVersionOutput(&buf, info, "yaml"); err != nil {
		t.Fatalf("printVersionOutput() error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "version:") {
		t.Errorf("YAML output missing version key: %s", out)
	}
	if !strings.Contains(out, "edition:") {
		t.Errorf("YAML output missing edition key: %s", out)
	}
}

func TestVersionCommandRejectsArgs(t *testing.T) {
	cmd := GetVersionCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"extra"})

	if err := cmd.Execute(); err == nil {
		t.Error("Execute() with extra args should return an error")
	}
}
