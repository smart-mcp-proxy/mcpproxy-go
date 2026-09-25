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

// TestImportedServer_ExplicitStdioProtocolWinsOverLeftoverURL asserts a
// hand-edited entry that explicitly declares `"type": "stdio"` (protocol)
// but also still carries a `url` field (e.g. left over from converting a
// remote entry to a local one) is summarized as "local process", matching
// how the runtime itself resolves protocol — internal/config/config.go's own
// validation treats `Protocol == "stdio"` as authoritative regardless of
// URL. The old Command!=""&&URL=="" heuristic disagreed with the runtime and
// tagged this "remote".
func TestImportedServer_ExplicitStdioProtocolWinsOverLeftoverURL(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"weird": {
				"type": "stdio",
				"command": "myserver",
				"url": "http://leftover.example/mcp"
			}
		}
	}`)

	result, err := Import(content, &ImportOptions{FormatHint: FormatCursor})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(result.Imported))
	}
	imported := result.Imported[0]

	if !containsTag(imported.Tags, "local process") {
		t.Errorf("Tags = %v, want to contain %q for an explicit stdio protocol", imported.Tags, "local process")
	}
	if containsTag(imported.Tags, "remote") {
		t.Errorf("Tags = %v, must not contain %q when protocol is explicitly stdio", imported.Tags, "remote")
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

// TestImportedServer_AutoProtocolWithCommandIsStdio: review round 2 finding —
// a hand-edited entry declaring `"type": "auto"` (a value Cursor/Claude-Code
// parsers pass through verbatim when present) with a command is resolved to
// stdio by the runtime's own transport selector,
// internal/transport.DetermineTransportType, which treats "auto" identically
// to an empty protocol and checks Command before URL. The preview's isStdio
// heuristic disagreed (it only special-cased "stdio" and ""), tagging this
// "remote" instead of "local process".
func TestImportedServer_AutoProtocolWithCommandIsStdio(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"weird": {
				"type": "auto",
				"command": "myserver"
			}
		}
	}`)

	result, err := Import(content, &ImportOptions{FormatHint: FormatCursor})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(result.Imported))
	}
	imported := result.Imported[0]

	if !containsTag(imported.Tags, "local process") {
		t.Errorf("Tags = %v, want to contain %q for protocol \"auto\" with a command, matching DetermineTransportType", imported.Tags, "local process")
	}
	if containsTag(imported.Tags, "remote") {
		t.Errorf("Tags = %v, must not contain %q for protocol \"auto\" with a command", imported.Tags, "remote")
	}
}

// TestImportedServer_NeitherCommandNorURLIsStdio is review round 3's
// isStdio finding: a hand-edited entry declaring `"type": "auto"` (or an
// empty protocol) with NEITHER a command NOR a url still resolves to stdio
// under the runtime's own transport selector,
// internal/transport.DetermineTransportType, whose final fallback ("default
// to stdio") fires whenever neither Command nor URL is set. The preview's
// isStdio heuristic disagreed here — it required Command!="" for the
// ""/"auto" branch — tagging this "remote" instead of "local process" even
// though the entry can't connect as either.
func TestImportedServer_NeitherCommandNorURLIsStdio(t *testing.T) {
	content := []byte(`{
		"mcpServers": {
			"weird": {
				"type": "auto"
			}
		}
	}`)

	result, err := Import(content, &ImportOptions{FormatHint: FormatCursor})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(result.Imported))
	}
	imported := result.Imported[0]

	if !containsTag(imported.Tags, "local process") {
		t.Errorf("Tags = %v, want to contain %q for protocol \"auto\" with neither command nor url, matching DetermineTransportType's stdio default", imported.Tags, "local process")
	}
	if containsTag(imported.Tags, "remote") {
		t.Errorf("Tags = %v, must not contain %q for protocol \"auto\" with neither command nor url", imported.Tags, "remote")
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

// TestDescribeFields_HeaderUsesHeaderSpecificMatcher asserts the isHeader=true
// path actually classifies through oauth.IsSensitiveHeaderName rather than
// silently reusing the env-name matcher (IsSensitiveKeyName). "Cookie" is a
// clean discriminator: it is header-sensitive (sensitiveHeaders) but its
// uppercase form contains none of the env markers (TOKEN, SECRET, KEY,
// PASSWORD, AUTH, …), so this fails if describeFields(..., isHeader=true)
// were ever pointed at the wrong matcher.
func TestDescribeFields_HeaderUsesHeaderSpecificMatcher(t *testing.T) {
	headers := map[string]string{
		"Cookie":       "",
		"X-Request-Id": "abc-123",
	}
	fields := describeFields(headers, true)
	byName := map[string]ImportedField{}
	for _, f := range fields {
		byName[f.Name] = f
	}

	if !byName["Cookie"].SecretLike {
		t.Errorf("Cookie = %+v, want SecretLike=true via the header-specific matcher", byName["Cookie"])
	}
	if !byName["Cookie"].EmptyOrPlaceholder {
		t.Errorf("Cookie = %+v, want EmptyOrPlaceholder=true for an empty value", byName["Cookie"])
	}
	if byName["X-Request-Id"].SecretLike {
		t.Errorf("X-Request-Id = %+v, must not be classified as secret-like", byName["X-Request-Id"])
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
