package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// TestImportPreview_URLFormat pins FR-064: a pasted http(s) URL previews as a
// remote server via auto-detection, with no format hint.
func TestImportPreview_URLFormat(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	body, _ := json.Marshal(ImportRequest{Content: "https://api.githubcopilot.com/mcp/", AllowPasteFallback: true})
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrapped.Data.Format != "url" {
		t.Errorf("expected format 'url', got %q", wrapped.Data.Format)
	}
	if len(wrapped.Data.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(wrapped.Data.Imported))
	}
	imported := wrapped.Data.Imported[0]
	if imported.Protocol != "http" || imported.URL != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("unexpected imported server: %+v", imported)
	}
	if imported.Command != "" {
		t.Errorf("expected no command for a URL import, got %q", imported.Command)
	}
}

// TestImportPreview_CommandFormat pins FR-064: a pasted command line previews
// as a local/stdio server via auto-detection.
func TestImportPreview_CommandFormat(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	body, _ := json.Marshal(ImportRequest{Content: "npx -y @modelcontextprotocol/server-filesystem /tmp", AllowPasteFallback: true})
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrapped.Data.Format != "command" {
		t.Errorf("expected format 'command', got %q", wrapped.Data.Format)
	}
	if len(wrapped.Data.Imported) != 1 {
		t.Fatalf("expected 1 imported server, got %d", len(wrapped.Data.Imported))
	}
	imported := wrapped.Data.Imported[0]
	if imported.Command != "npx" {
		t.Errorf("expected command 'npx', got %q", imported.Command)
	}
	if imported.Summary != "npx -y @modelcontextprotocol/server-filesystem /tmp" {
		t.Errorf("unexpected summary: %q", imported.Summary)
	}
	found := false
	for _, tag := range imported.Tags {
		if tag == "local process" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'local process' tag, got %v", imported.Tags)
	}
}

// TestImportPreview_EnvHeaderSecretLikeAndPlaceholder pins the contract
// example shape (contracts/rest-api.md "Import preview"): env/header entries
// carry secret_like and empty_or_placeholder, env additionally carries
// value_present, and no raw secret value appears in the response.
func TestImportPreview_EnvHeaderSecretLikeAndPlaceholder(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{Content: `{
		"mcpServers": {
			"github": {
				"command": "uvx",
				"args": ["mcp-server-github"],
				"env": {"GITHUB_TOKEN": "sk-live-abc123", "WORKDIR": ""}
			}
		}
	}`}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if bytesContains(rr.Body.Bytes(), "sk-live-abc123") {
		t.Fatal("raw secret value must never appear in the import preview response")
	}

	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	imported := wrapped.Data.Imported[0]

	var tokenField, workdirField *EnvFieldPreview
	for i := range imported.Env {
		switch imported.Env[i].Name {
		case "GITHUB_TOKEN":
			tokenField = &imported.Env[i]
		case "WORKDIR":
			workdirField = &imported.Env[i]
		}
	}
	if tokenField == nil || workdirField == nil {
		t.Fatalf("expected both env fields present, got %+v", imported.Env)
	}
	if !tokenField.SecretLike || !tokenField.ValuePresent || tokenField.EmptyOrPlaceholder {
		t.Errorf("unexpected GITHUB_TOKEN preview: %+v", tokenField)
	}
	if workdirField.SecretLike {
		t.Errorf("WORKDIR must not be secret_like: %+v", workdirField)
	}
	if workdirField.ValuePresent || !workdirField.EmptyOrPlaceholder {
		t.Errorf("empty WORKDIR must read value_present=false, empty_or_placeholder=true: %+v", workdirField)
	}

	found := false
	for _, tag := range imported.Tags {
		if tag == "needs secret" {
			found = true
		}
	}
	if found {
		t.Errorf("populated real secret must not need replacement, got %v", imported.Tags)
	}
}

// A real credential must be classified identically by the HTTP preview and
// the shared configimport result consumed by the CLI. Only empty or
// placeholder credentials need the "needs secret" tag.
func TestImportPreview_UsesSharedCredentialEnrichment(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{Content: `{"mcpServers":{"github":{"url":"https://example.test/mcp","headers":{"Authorization":"Bearer live-secret-value"},"env":{"GITHUB_TOKEN":"ghp_live-token-value"}}}}`}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if bytesContains(rr.Body.Bytes(), "live-secret-value") || bytesContains(rr.Body.Bytes(), "ghp_live-token-value") {
		t.Fatal("preview response exposed a credential value")
	}
	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(wrapped.Data.Imported) != 1 {
		t.Fatalf("expected one imported server, got %d", len(wrapped.Data.Imported))
	}
	got := wrapped.Data.Imported[0]
	if got.Summary != "https://example.test/mcp (header auth)" {
		t.Errorf("summary = %q, want shared auth-aware summary", got.Summary)
	}
	for _, tag := range got.Tags {
		if tag == "needs secret" {
			t.Errorf("real populated credentials must not produce needs secret: %v", got.Tags)
		}
	}
	if len(got.Headers) != 1 || !got.Headers[0].SecretLike || got.Headers[0].EmptyOrPlaceholder {
		t.Errorf("unexpected Authorization preview: %+v", got.Headers)
	} else if !got.Headers[0].ValuePresent {
		t.Error("populated Authorization header must report value_present=true")
	}
}

func TestImportPreview_RedactsCredentialShapedCommandFromSummary(t *testing.T) {
	const credentialCommand = "ghp_1234567890abcdefghijABCDEFGHIJ123456"
	logger := zap.NewNop().Sugar()
	server := NewServer(&mockImportController{apiKey: "test-key"}, logger, nil)
	body, _ := json.Marshal(ImportRequest{Content: `{"mcpServers":{"x":{"type":"stdio","command":"` + credentialCommand + `"}}}`})
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if bytesContains(rr.Body.Bytes(), credentialCommand) {
		t.Fatal("preview response exposed credential-shaped command")
	}
	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(wrapped.Data.Imported) != 1 {
		t.Fatalf("expected one imported server, got %d", len(wrapped.Data.Imported))
	}
	if got := wrapped.Data.Imported[0]; got.Command == credentialCommand || got.Summary != got.Command {
		t.Errorf("command and summary must share a redacted value, got command=%q summary=%q", got.Command, got.Summary)
	}
}

func bytesContains(haystack []byte, needle string) bool {
	return bytes.Contains(haystack, []byte(needle))
}

func TestImportPreview_EmptyObjectWithoutFormatHint(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	body, _ := json.Marshal(ImportRequest{Content: "{}"})
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(wrapped.Data.Imported) != 0 {
		t.Fatalf("expected 0 imported servers, got %d", len(wrapped.Data.Imported))
	}
}
