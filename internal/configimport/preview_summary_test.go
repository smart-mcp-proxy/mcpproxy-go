package configimport

import (
	"testing"
)

// TestImportedServer_StdioSummary is T030: a stdio server's preview row gets
// a "command args…" summary and the "local process" tag.
func TestImportedServer_StdioSummary(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"filesystem": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
			}
		}
	}`)

	result, err := Import(content, &ImportOptions{FormatHint: FormatClaudeDesktop})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(result.Imported))
	}
	imported := result.Imported[0]

	wantSummary := "npx -y @modelcontextprotocol/server-filesystem /tmp"
	if imported.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", imported.Summary, wantSummary)
	}
	if !containsTag(imported.Tags, "local process") {
		t.Errorf("Tags = %v, want to contain %q", imported.Tags, "local process")
	}
	if containsTag(imported.Tags, "remote") {
		t.Errorf("Tags = %v, must not contain %q for a stdio server", imported.Tags, "remote")
	}
}

// TestImportedServer_RemoteSummaryAndOAuthTag asserts a remote (URL) server's
// summary shows the auth type and gets the "remote" tag, plus "oauth" when
// the server declares OAuth.
func TestImportedServer_RemoteSummaryAndOAuthTag(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"github": {
				"httpUrl": "https://api.githubcopilot.com/mcp/",
				"oauth": {"enabled": true, "clientId": "test-client"}
			}
		}
	}`)

	result, err := Import(content, &ImportOptions{FormatHint: FormatGemini})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(result.Imported))
	}
	imported := result.Imported[0]

	if !containsTag(imported.Tags, "remote") {
		t.Errorf("Tags = %v, want to contain %q", imported.Tags, "remote")
	}
	if !containsTag(imported.Tags, "oauth") {
		t.Errorf("Tags = %v, want to contain %q", imported.Tags, "oauth")
	}
	if imported.Summary == "" {
		t.Error("Summary must not be empty for a remote server")
	}
}

// TestDescribeFields_SecretLikeAndEmptyOrPlaceholder is T030's per-env/header
// classification: a secret-shaped name with an empty or placeholder value is
// flagged on both axes; an ordinary populated value is flagged on neither.
func TestDescribeFields_SecretLikeAndEmptyOrPlaceholder(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN": "",
		"API_KEY":      "YOUR_API_KEY",
		"LOG_LEVEL":    "debug",
	}
	fields := describeFields(env, false)
	byName := map[string]ImportedField{}
	for _, f := range fields {
		byName[f.Name] = f
	}

	if !byName["GITHUB_TOKEN"].SecretLike || !byName["GITHUB_TOKEN"].EmptyOrPlaceholder {
		t.Errorf("GITHUB_TOKEN = %+v, want SecretLike=true EmptyOrPlaceholder=true", byName["GITHUB_TOKEN"])
	}
	if byName["GITHUB_TOKEN"].ValuePresent {
		t.Error("GITHUB_TOKEN.ValuePresent should be false for an empty value")
	}
	if !byName["API_KEY"].SecretLike || !byName["API_KEY"].EmptyOrPlaceholder {
		t.Errorf("API_KEY = %+v, want SecretLike=true EmptyOrPlaceholder=true", byName["API_KEY"])
	}
	if byName["LOG_LEVEL"].SecretLike {
		t.Error("LOG_LEVEL should not be classified as secret-like")
	}
	if byName["LOG_LEVEL"].EmptyOrPlaceholder {
		t.Error("LOG_LEVEL has a real value, should not be flagged empty_or_placeholder")
	}
}

// TestImportedServer_NeedsSecretTag asserts the "needs secret" tag appears
// only when a secret-like field is empty/placeholder, and is absent when a
// secret-like field already carries a real-looking value.
func TestImportedServer_NeedsSecretTag(t *testing.T) {
	t.Run("empty secret-like env triggers the tag", func(t *testing.T) {
		content := []byte(`{"mcpServers":{"github":{"command":"uvx","args":["mcp-server-github"],"env":{"GITHUB_TOKEN":""}}}}`)
		result, err := Import(content, &ImportOptions{FormatHint: FormatClaudeDesktop})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if !containsTag(result.Imported[0].Tags, "needs secret") {
			t.Errorf("Tags = %v, want to contain %q", result.Imported[0].Tags, "needs secret")
		}
	})

	t.Run("populated secret-like env does not trigger the tag", func(t *testing.T) {
		content := []byte(`{"mcpServers":{"github":{"command":"uvx","args":["mcp-server-github"],"env":{"GITHUB_TOKEN":"ghp_realtoken1234567890"}}}}`)
		result, err := Import(content, &ImportOptions{FormatHint: FormatClaudeDesktop})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if containsTag(result.Imported[0].Tags, "needs secret") {
			t.Errorf("Tags = %v, must not contain %q when the value looks real", result.Imported[0].Tags, "needs secret")
		}
	})
}

// TestSummarizeServer_NeverEmbedsRawSecretArg asserts a stdio server whose
// argv carries a secret-shaped value renders it masked in Summary, never
// raw — the same rule the existing CLI/REST redaction already applies to
// Args, now also reflected in the new Summary field.
func TestSummarizeServer_NeverEmbedsRawSecretArg(t *testing.T) {
	content := []byte(`{"mcpServers":{"svc":{"command":"myserver","args":["--api-key","sk-supersecretvalue1234567890"]}}}`)
	result, err := Import(content, &ImportOptions{FormatHint: FormatClaudeDesktop})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	summary := result.Imported[0].Summary
	if containsSubstring(summary, "sk-supersecretvalue1234567890") {
		t.Errorf("Summary leaked the raw secret argv value: %q", summary)
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) > 0 && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
