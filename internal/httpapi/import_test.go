package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"go.uber.org/zap"
)

// mockImportController is a mock controller for import tests
type mockImportController struct {
	baseController
	apiKey string
}

func (m *mockImportController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockImportController) AddServer(_ context.Context, _ *config.ServerConfig) error {
	return nil
}

// wrappedImportResponse represents the API response wrapper
type wrappedImportResponse struct {
	Success bool           `json:"success"`
	Data    ImportResponse `json:"data"`
}

func TestImportServersJSON_Preview(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	// Build request body
	reqBody := ImportRequest{
		Content: `{
			"mcpServers": {
				"github": {
					"command": "uvx",
					"args": ["mcp-server-github"],
					"env": {"GITHUB_TOKEN": "token"}
				}
			},
			"globalShortcut": "Ctrl+Shift+M"
		}`,
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	response := wrapped.Data

	if response.Format != "claude_desktop" {
		t.Errorf("Expected format 'claude_desktop', got '%s'", response.Format)
	}
	if response.Summary.Imported != 1 {
		t.Errorf("Expected 1 imported, got %d", response.Summary.Imported)
	}
	if len(response.Imported) != 1 {
		t.Errorf("Expected 1 imported server, got %d", len(response.Imported))
	}
	if response.Imported[0].Name != "github" {
		t.Errorf("Expected server name 'github', got '%s'", response.Imported[0].Name)
	}
}

func TestImportServersJSON_InvalidContent(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{
		Content: `{invalid json`,
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestImportServersJSON_EmptyContent(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{
		Content: "",
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestImportServersJSON_WithFormatHint(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	// This is a generic JSON that could match multiple formats
	reqBody := ImportRequest{
		Content: `{
			"mcpServers": {
				"test": {
					"command": "node",
					"args": ["server.js"]
				}
			}
		}`,
		Format: "claude-desktop", // Use hyphenated format for input
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if wrapped.Data.Format != "claude_desktop" {
		t.Errorf("Expected format 'claude_desktop', got '%s'", wrapped.Data.Format)
	}
}

func TestImportServersJSON_WithServerNamesFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{
		Content: `{
			"mcpServers": {
				"github": {"command": "cmd1"},
				"filesystem": {"command": "cmd2"},
				"memory": {"command": "cmd3"}
			},
			"globalShortcut": "Ctrl+M"
		}`,
		ServerNames: []string{"github"},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if wrapped.Data.Summary.Imported != 1 {
		t.Errorf("Expected 1 imported, got %d", wrapped.Data.Summary.Imported)
	}
	if wrapped.Data.Summary.Skipped != 2 {
		t.Errorf("Expected 2 skipped, got %d", wrapped.Data.Summary.Skipped)
	}
}

// TestRunImport_ConflictRenameSanitizableName covers the latent mismatch widened
// by #729 (MCP-3003): after #729, ImportedServer.OriginalName carries the raw
// source name ("Figma Desktop") while the wizard keys its conflict-rename map by
// the sanitized preview name ("Figma_Desktop"). The rename map must resolve when
// keyed by EITHER the raw OriginalName (CLI/documented contract) or the sanitized
// Server.Name (Web UI wizard), otherwise the conflict rename is silently dropped.
func TestRunImport_ConflictRenameSanitizableName(t *testing.T) {
	const content = `{
		"mcpServers": {
			"Figma Desktop": {"command": "figma-mcp"}
		},
		"globalShortcut": "Ctrl+M"
	}`

	tests := []struct {
		name     string
		renameBy string // map key the caller used
	}{
		{name: "wizard keys by sanitized Server.Name", renameBy: "Figma_Desktop"},
		{name: "CLI keys by raw OriginalName", renameBy: "Figma Desktop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			mock := &mockImportController{apiKey: "test-key"}
			server := NewServer(mock, logger, nil)

			req := httptest.NewRequest("POST", "/api/v1/servers/import/path", http.NoBody)
			req.Header.Set("X-API-Key", "test-key")

			const want = "Figma_Desktop_claude_desktop"
			rename := map[string]string{tt.renameBy: want}

			resp, err := server.runImport(req, []byte(content), "claude-desktop", nil, true, rename)
			if err != nil {
				t.Fatalf("runImport returned error: %v", err)
			}
			if len(resp.Imported) != 1 {
				t.Fatalf("expected 1 imported server, got %d", len(resp.Imported))
			}
			got := resp.Imported[0]
			// OriginalName remains the raw source name (post-#729 contract).
			if got.OriginalName != "Figma Desktop" {
				t.Errorf("OriginalName = %q, want %q", got.OriginalName, "Figma Desktop")
			}
			// The conflict rename must have been applied regardless of which key
			// the caller used.
			if got.Name != want {
				t.Errorf("renamed Server.Name = %q, want %q (rename keyed by %q was dropped)", got.Name, want, tt.renameBy)
			}
		})
	}
}

func TestImportServers_FileUpload(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file part
	fileContent := `{
		"mcpServers": {
			"test-server": {
				"command": "node",
				"args": ["server.js"]
			}
		},
		"globalShortcut": "Ctrl+M"
	}`
	part, _ := writer.CreateFormFile("file", "config.json")
	io.WriteString(part, fileContent)
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/servers/import?preview=true", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if wrapped.Data.Summary.Imported != 1 {
		t.Errorf("Expected 1 imported, got %d", wrapped.Data.Summary.Imported)
	}
}

func TestImportServers_MissingFile(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	// Create empty multipart form (no file)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/servers/import?preview=true", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestImportServersJSON_CodexTOML(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{
		Content: `
[mcp_servers.github]
command = "uvx"
args = ["mcp-server-github"]
env_vars = ["GITHUB_TOKEN"]
`,
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var wrapped wrappedImportResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if wrapped.Data.Format != "codex" {
		t.Errorf("Expected format 'codex', got '%s'", wrapped.Data.Format)
	}
	if wrapped.Data.Summary.Imported != 1 {
		t.Errorf("Expected 1 imported, got %d", wrapped.Data.Summary.Imported)
	}
}

func TestImportServersJSON_UnknownFormat(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mock := &mockImportController{apiKey: "test-key"}
	server := NewServer(mock, logger, nil)

	reqBody := ImportRequest{
		Content: `{"mcpServers": {"test": {"command": "node"}}}`,
		Format:  "unknown-format",
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/json?preview=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}
