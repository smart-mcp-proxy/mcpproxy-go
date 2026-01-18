package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/configimport"
)

// ImportRequest represents a request to import servers from JSON/TOML content
type ImportRequest struct {
	Content     string   `json:"content"`               // Raw JSON or TOML content
	Format      string   `json:"format,omitempty"`      // Optional format hint
	ServerNames []string `json:"server_names,omitempty"` // Optional: import only these servers
}

// ImportResponse represents the response from an import operation
type ImportResponse struct {
	Format      string                       `json:"format"`
	FormatName  string                       `json:"format_name"`
	Summary     configimport.ImportSummary   `json:"summary"`
	Imported    []ImportedServerResponse     `json:"imported"`
	Skipped     []configimport.SkippedServer `json:"skipped"`
	Failed      []configimport.FailedServer  `json:"failed"`
	Warnings    []string                     `json:"warnings"`
}

// ImportedServerResponse represents an imported server in the response
type ImportedServerResponse struct {
	Name          string   `json:"name"`
	Protocol      string   `json:"protocol"`
	URL           string   `json:"url,omitempty"`
	Command       string   `json:"command,omitempty"`
	Args          []string `json:"args,omitempty"`
	SourceFormat  string   `json:"source_format"`
	OriginalName  string   `json:"original_name"`
	FieldsSkipped []string `json:"fields_skipped,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// CanonicalConfigPath represents a well-known config file path
type CanonicalConfigPath struct {
	Name        string `json:"name"`         // Display name (e.g., "Claude Desktop")
	Format      string `json:"format"`       // Format identifier (e.g., "claude_desktop")
	Path        string `json:"path"`         // Full path to the config file
	Exists      bool   `json:"exists"`       // Whether the file exists
	OS          string `json:"os"`           // Operating system (darwin, windows, linux)
	Description string `json:"description"`  // Brief description
}

// CanonicalConfigPathsResponse represents the response for canonical config paths
type CanonicalConfigPathsResponse struct {
	OS    string                `json:"os"`    // Current operating system
	Paths []CanonicalConfigPath `json:"paths"` // List of canonical config paths
}

// getCanonicalConfigPaths returns well-known config file paths for all supported formats
func getCanonicalConfigPaths() []CanonicalConfigPath {
	homeDir, _ := os.UserHomeDir()
	currentOS := runtime.GOOS

	// Define all canonical paths per OS
	allPaths := []CanonicalConfigPath{
		// Claude Desktop
		{Name: "Claude Desktop", Format: "claude-desktop", OS: "darwin",
			Path:        filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
			Description: "Claude Desktop app configuration"},
		{Name: "Claude Desktop", Format: "claude-desktop", OS: "windows",
			Path:        filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json"),
			Description: "Claude Desktop app configuration"},
		{Name: "Claude Desktop", Format: "claude-desktop", OS: "linux",
			Path:        filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json"),
			Description: "Claude Desktop app configuration"},

		// Claude Code
		{Name: "Claude Code (User)", Format: "claude-code", OS: "darwin",
			Path:        filepath.Join(homeDir, ".claude.json"),
			Description: "Claude Code user-level MCP servers"},
		{Name: "Claude Code (User)", Format: "claude-code", OS: "windows",
			Path:        filepath.Join(homeDir, ".claude.json"),
			Description: "Claude Code user-level MCP servers"},
		{Name: "Claude Code (User)", Format: "claude-code", OS: "linux",
			Path:        filepath.Join(homeDir, ".claude.json"),
			Description: "Claude Code user-level MCP servers"},

		// Cursor IDE
		{Name: "Cursor IDE", Format: "cursor", OS: "darwin",
			Path:        filepath.Join(homeDir, ".cursor", "mcp.json"),
			Description: "Cursor IDE global MCP configuration"},
		{Name: "Cursor IDE", Format: "cursor", OS: "windows",
			Path:        filepath.Join(homeDir, ".cursor", "mcp.json"),
			Description: "Cursor IDE global MCP configuration"},
		{Name: "Cursor IDE", Format: "cursor", OS: "linux",
			Path:        filepath.Join(homeDir, ".cursor", "mcp.json"),
			Description: "Cursor IDE global MCP configuration"},

		// Codex CLI
		{Name: "Codex CLI", Format: "codex", OS: "darwin",
			Path:        filepath.Join(homeDir, ".codex", "config.toml"),
			Description: "OpenAI Codex CLI configuration (TOML)"},
		{Name: "Codex CLI", Format: "codex", OS: "windows",
			Path:        filepath.Join(homeDir, ".codex", "config.toml"),
			Description: "OpenAI Codex CLI configuration (TOML)"},
		{Name: "Codex CLI", Format: "codex", OS: "linux",
			Path:        filepath.Join(homeDir, ".codex", "config.toml"),
			Description: "OpenAI Codex CLI configuration (TOML)"},

		// Gemini CLI
		{Name: "Gemini CLI", Format: "gemini", OS: "darwin",
			Path:        filepath.Join(homeDir, ".gemini", "settings.json"),
			Description: "Google Gemini CLI settings"},
		{Name: "Gemini CLI", Format: "gemini", OS: "windows",
			Path:        filepath.Join(homeDir, ".gemini", "settings.json"),
			Description: "Google Gemini CLI settings"},
		{Name: "Gemini CLI", Format: "gemini", OS: "linux",
			Path:        filepath.Join(homeDir, ".gemini", "settings.json"),
			Description: "Google Gemini CLI settings"},
	}

	// Filter to current OS and check existence
	var result []CanonicalConfigPath
	for _, p := range allPaths {
		if p.OS == currentOS {
			// Check if file exists
			if _, err := os.Stat(p.Path); err == nil {
				p.Exists = true
			}
			result = append(result, p)
		}
	}

	return result
}

// handleGetCanonicalConfigPaths godoc
// @Summary Get canonical config file paths
// @Description Returns well-known configuration file paths for supported formats with existence check
// @Tags servers
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Success 200 {object} CanonicalConfigPathsResponse "Canonical config paths"
// @Router /api/v1/servers/import/paths [get]
func (s *Server) handleGetCanonicalConfigPaths(w http.ResponseWriter, r *http.Request) {
	paths := getCanonicalConfigPaths()

	response := CanonicalConfigPathsResponse{
		OS:    runtime.GOOS,
		Paths: paths,
	}

	s.writeSuccess(w, response)
}

// ImportFromPathRequest represents a request to import from a file path
type ImportFromPathRequest struct {
	Path        string   `json:"path"`                   // File path to import from
	Format      string   `json:"format,omitempty"`       // Optional format hint
	ServerNames []string `json:"server_names,omitempty"` // Optional: import only these servers
}

// handleImportFromPath godoc
// @Summary Import servers from a file path
// @Description Import MCP server configurations by reading a file from the server's filesystem
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param request body ImportFromPathRequest true "Import request with file path"
// @Param preview query bool false "If true, return preview without importing"
// @Success 200 {object} ImportResponse "Import result"
// @Failure 400 {object} contracts.ErrorResponse "Bad request - invalid path or format"
// @Failure 404 {object} contracts.ErrorResponse "File not found"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/import/path [post]
func (s *Server) handleImportFromPath(w http.ResponseWriter, r *http.Request) {
	logger := s.getRequestLogger(r)

	// Parse request body
	var req ImportFromPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.Path == "" {
		s.writeError(w, r, http.StatusBadRequest, "path is required")
		return
	}

	// Expand home directory if needed
	path := req.Path
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(homeDir, path[2:])
		}
	}

	// Read the file
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("File not found: %s", path))
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to read file: %v", err))
		return
	}

	// Preview mode?
	preview := r.URL.Query().Get("preview") == "true"

	// Use the common runImport function
	result, err := s.runImport(r, content, req.Format, req.ServerNames, preview)
	if err != nil {
		logger.Error("Import from path failed", "path", path, "error", err)
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	s.writeSuccess(w, result)
}

// handleImportServers godoc
// @Summary Import servers from uploaded configuration file
// @Description Import MCP server configurations from a Claude Desktop, Claude Code, Cursor IDE, Codex CLI, or Gemini CLI configuration file
// @Tags servers
// @Accept multipart/form-data
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param file formData file true "Configuration file to import"
// @Param preview query bool false "If true, return preview without importing"
// @Param format query string false "Force format (claude-desktop, claude-code, cursor, codex, gemini)"
// @Param server_names query string false "Comma-separated list of server names to import"
// @Success 200 {object} ImportResponse "Import result"
// @Failure 400 {object} contracts.ErrorResponse "Bad request - invalid file or format"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/import [post]
func (s *Server) handleImportServers(w http.ResponseWriter, r *http.Request) {
	logger := s.getRequestLogger(r)

	// Parse multipart form (max 10MB)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Failed to parse form: %v", err))
		return
	}

	// Get the uploaded file
	file, _, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("File is required: %v", err))
		return
	}
	defer file.Close()

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Failed to read file: %v", err))
		return
	}

	// Parse query parameters
	preview := r.URL.Query().Get("preview") == "true"
	formatHint := r.URL.Query().Get("format")
	serverNamesStr := r.URL.Query().Get("server_names")

	var serverNames []string
	if serverNamesStr != "" {
		serverNames = strings.Split(serverNamesStr, ",")
		for i := range serverNames {
			serverNames[i] = strings.TrimSpace(serverNames[i])
		}
	}

	// Run import
	result, err := s.runImport(r, content, formatHint, serverNames, preview)
	if err != nil {
		logger.Error("Import failed", "error", err)
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	s.writeSuccess(w, result)
}

// handleImportServersJSON godoc
// @Summary Import servers from JSON/TOML content
// @Description Import MCP server configurations from raw JSON or TOML content (useful for pasting configurations)
// @Tags servers
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security ApiKeyQuery
// @Param request body ImportRequest true "Import request with content"
// @Param preview query bool false "If true, return preview without importing"
// @Success 200 {object} ImportResponse "Import result"
// @Failure 400 {object} contracts.ErrorResponse "Bad request - invalid content or format"
// @Failure 500 {object} contracts.ErrorResponse "Internal server error"
// @Router /api/v1/servers/import/json [post]
func (s *Server) handleImportServersJSON(w http.ResponseWriter, r *http.Request) {
	logger := s.getRequestLogger(r)

	var req ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.Content == "" {
		s.writeError(w, r, http.StatusBadRequest, "Content is required")
		return
	}

	// Parse query parameter for preview
	preview := r.URL.Query().Get("preview") == "true"

	// Run import
	result, err := s.runImport(r, []byte(req.Content), req.Format, req.ServerNames, preview)
	if err != nil {
		logger.Error("Import failed", "error", err)
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	s.writeSuccess(w, result)
}

// runImport executes the import logic and optionally applies the servers
func (s *Server) runImport(r *http.Request, content []byte, formatHint string, serverNames []string, preview bool) (*ImportResponse, error) {
	logger := s.getRequestLogger(r)

	// Build import options
	opts := &configimport.ImportOptions{
		Preview: preview,
		Now:     time.Now(),
	}

	// Parse format hint
	if formatHint != "" {
		format := parseFormat(formatHint)
		if format == configimport.FormatUnknown {
			return nil, fmt.Errorf("unknown format: %s. Valid formats: claude-desktop, claude-code, cursor, codex, gemini", formatHint)
		}
		opts.FormatHint = format
	}

	// Set server name filter
	if len(serverNames) > 0 {
		opts.ServerNames = serverNames
	}

	// Get existing servers to detect duplicates
	existingServers, err := s.controller.GetAllServers()
	if err == nil {
		existingNames := make([]string, 0, len(existingServers))
		for _, srv := range existingServers {
			if name, ok := srv["name"].(string); ok {
				existingNames = append(existingNames, name)
			}
		}
		opts.ExistingServers = existingNames
	}

	// Run import
	result, err := configimport.Import(content, opts)
	if err != nil {
		return nil, err
	}

	// Build response
	response := &ImportResponse{
		Format:     string(result.Format),
		FormatName: result.FormatDisplayName,
		Summary:    result.Summary,
		Imported:   make([]ImportedServerResponse, len(result.Imported)),
		Skipped:    result.Skipped,
		Failed:     result.Failed,
		Warnings:   result.Warnings,
	}

	for i, imported := range result.Imported {
		response.Imported[i] = ImportedServerResponse{
			Name:          imported.Server.Name,
			Protocol:      imported.Server.Protocol,
			URL:           imported.Server.URL,
			Command:       imported.Server.Command,
			Args:          imported.Server.Args,
			SourceFormat:  string(imported.SourceFormat),
			OriginalName:  imported.OriginalName,
			FieldsSkipped: imported.FieldsSkipped,
			Warnings:      imported.Warnings,
		}
	}

	// If not preview, actually add the servers
	if !preview && len(result.Imported) > 0 {
		for _, imported := range result.Imported {
			if err := s.controller.AddServer(r.Context(), imported.Server); err != nil {
				logger.Warn("Failed to add imported server", "server", imported.Server.Name, "error", err)
				// Continue with other servers
			} else {
				logger.Info("Imported server", "server", imported.Server.Name, "format", result.Format)
			}
		}
	}

	return response, nil
}

// parseFormat converts a format string to ConfigFormat
func parseFormat(format string) configimport.ConfigFormat {
	switch strings.ToLower(format) {
	case "claude-desktop", "claudedesktop":
		return configimport.FormatClaudeDesktop
	case "claude-code", "claudecode":
		return configimport.FormatClaudeCode
	case "cursor":
		return configimport.FormatCursor
	case "codex":
		return configimport.FormatCodex
	case "gemini":
		return configimport.FormatGemini
	default:
		return configimport.FormatUnknown
	}
}
