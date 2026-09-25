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

	body, _ := json.Marshal(ImportRequest{Content: "https://api.githubcopilot.com/mcp/"})
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

	body, _ := json.Marshal(ImportRequest{Content: "npx -y @modelcontextprotocol/server-filesystem /tmp"})
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
	if !found {
		t.Errorf("expected 'needs secret' tag, got %v", imported.Tags)
	}
}

func bytesContains(haystack []byte, needle string) bool {
	return bytes.Contains(haystack, []byte(needle))
}
