package configimport

import (
	"os"
	"testing"
)

// TestDetectFormat_URL pins FR-064: a single http(s):// token detects as the
// new "url" format.
func TestDetectFormat_URL(t *testing.T) {
	cases := []string{
		"https://api.githubcopilot.com/mcp/",
		"  http://localhost:8080/mcp  ",
	}
	for _, c := range cases {
		result, err := DetectFormat([]byte(c))
		if err != nil {
			t.Fatalf("DetectFormat(%q) error: %v", c, err)
		}
		if result.Format != FormatURL {
			t.Errorf("DetectFormat(%q).Format = %q, want %q", c, result.Format, FormatURL)
		}
	}
}

// TestDetectFormat_Command pins FR-064: a single non-JSON/TOML line detects as
// the new "command" format.
func TestDetectFormat_Command(t *testing.T) {
	cases := []string{
		"npx -y @modelcontextprotocol/server-filesystem /tmp",
		`docker run -i --rm -e API_KEY="abc def" mcp/sqlite`,
	}
	for _, c := range cases {
		result, err := DetectFormat([]byte(c))
		if err != nil {
			t.Fatalf("DetectFormat(%q) error: %v", c, err)
		}
		if result.Format != FormatCommand {
			t.Errorf("DetectFormat(%q).Format = %q, want %q", c, result.Format, FormatCommand)
		}
	}
}

// TestDetectFormat_JSONTOMLUnchanged pins that the existing JSON/TOML
// detection paths are untouched by the new url/command fallbacks.
func TestDetectFormat_JSONTOMLUnchanged(t *testing.T) {
	json := `{"mcpServers": {"fs": {"command": "npx", "args": ["-y", "server-filesystem"]}}}`
	result, err := DetectFormat([]byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Format == FormatURL || result.Format == FormatCommand {
		t.Errorf("JSON content misdetected as %q", result.Format)
	}

	toml := "[mcp_servers.fs]\ncommand = \"npx\"\n"
	result, err = DetectFormat([]byte(toml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Format != FormatCodex {
		t.Errorf("TOML content misdetected as %q, want %q", result.Format, FormatCodex)
	}
}

// TestURLParser_Parse pins the URL → http server mapping (FR-064).
func TestURLParser_Parse(t *testing.T) {
	p := &URLParser{}
	parsed, err := p.Parse([]byte("https://api.githubcopilot.com/mcp/"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 parsed server, got %d", len(parsed))
	}
	srv := parsed[0]
	if srv.Fields["url"] != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("unexpected url field: %v", srv.Fields["url"])
	}
	if srv.Fields["protocol"] != "http" {
		t.Errorf("unexpected protocol field: %v", srv.Fields["protocol"])
	}
	if srv.Name == "" {
		t.Error("expected a non-empty derived name")
	}
}

// TestURLParser_RejectsInvalid pins that preview never executes anything: an
// invalid URL is rejected rather than guessed at.
func TestURLParser_RejectsInvalid(t *testing.T) {
	p := &URLParser{}
	if _, err := p.Parse([]byte("not a url")); err == nil {
		t.Fatal("expected an error for a non-URL input")
	}
}

// TestCommandParser_Parse pins the command line → stdio command/args mapping
// (FR-064), including quote-aware splitting.
func TestCommandParser_Parse(t *testing.T) {
	p := &CommandParser{}
	parsed, err := p.Parse([]byte(`docker run -e API_KEY="abc def" -i --rm mcp/sqlite`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 parsed server, got %d", len(parsed))
	}
	srv := parsed[0]
	if srv.Fields["command"] != "docker" {
		t.Errorf("unexpected command field: %v", srv.Fields["command"])
	}
	args, ok := srv.Fields["args"].([]string)
	if !ok {
		t.Fatalf("expected args to be []string, got %T", srv.Fields["args"])
	}
	want := []string{"run", "-e", "API_KEY=abc def", "-i", "--rm", "mcp/sqlite"}
	if len(args) != len(want) {
		t.Fatalf("unexpected args: %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestCommandParser_PreviewNeverExecutes pins FR-064: a command that would
// create a file (if actually run) leaves no file behind — Parse only
// tokenizes text, it never spawns anything.
func TestCommandParser_PreviewNeverExecutes(t *testing.T) {
	dir := t.TempDir()
	marker := dir + "/should-not-exist"
	p := &CommandParser{}
	if _, err := p.Parse([]byte("touch " + marker)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("preview must never execute the pasted command")
	}
}

// TestSuggestNameFromCommand_PrefersPackageForRunners pins that npx/uvx/pipx
// runners suggest the package name, not the runner binary, as the server
// name.
func TestSuggestNameFromCommand_PrefersPackageForRunners(t *testing.T) {
	name := SuggestNameFromCommand([]string{"npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"})
	if name != "server-filesystem" {
		t.Errorf("SuggestNameFromCommand = %q, want %q", name, "server-filesystem")
	}
}
