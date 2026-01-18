package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
