// This file demonstrates swaggo/swag annotation patterns for REST API endpoints.
// These annotations generate OpenAPI 3.x specifications automatically.
//
// Reference: https://github.com/swaggo/swag
//
// Location examples:
// - cmd/mcpproxy/main.go (API metadata)
// - internal/httpapi/server.go (endpoint handlers)

package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger"
)

// ==============================================================================
// Main API Metadata (cmd/mcpproxy/main.go)
// ==============================================================================

// @title MCPProxy API
// @version 1.0.0
// @description Smart proxy for Model Context Protocol (MCP) servers with BM25 search, quarantine, and tool routing
// @termsOfService https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/LICENSE

// @contact.name MCPProxy Support
// @contact.url https://github.com/smart-mcp-proxy/mcpproxy-go/issues
// @contact.email support@mcpproxy.dev

// @license.name MIT
// @license.url https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/LICENSE

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description API key authentication via X-API-Key header

// @securityDefinitions.apikey ApiKeyQuery
// @in query
// @name apikey
// @description API key authentication via apikey query parameter (for SSE/browser)

// @tag.name servers
// @tag.description Upstream MCP server management

// @tag.name diagnostics
// @tag.description Health checks and diagnostics

// @tag.name tools
// @tag.description Tool search and execution

func main() {
	// Router setup with Swagger UI
	r := chi.NewRouter()

	// Mount Swagger UI at /swagger/
	r.Mount("/swagger", httpSwagger.WrapHandler)

	// API routes...
}

// ==============================================================================
// Endpoint Handler Examples (internal/httpapi/server.go)
// ==============================================================================

// ErrorResponse represents standard error response
type ErrorResponse struct {
	Error   string `json:"error" example:"management operations disabled"`
	Code    string `json:"code,omitempty" example:"MGMT_DISABLED"`
	Details string `json:"details,omitempty" example:"Set disable_management=false in config"`
}

// Server represents an upstream MCP server
type Server struct {
	Name        string `json:"name" example:"github-server"`
	Enabled     bool   `json:"enabled" example:"true"`
	Connected   bool   `json:"connected" example:"true"`
	ToolCount   int    `json:"tool_count" example:"25"`
	Error       string `json:"error,omitempty" example:""`
	Quarantined bool   `json:"quarantined" example:"false"`
}

// ServerStats represents aggregate statistics
type ServerStats struct {
	Total       int `json:"total" example:"10"`
	Enabled     int `json:"enabled" example:"8"`
	Disabled    int `json:"disabled" example:"2"`
	Connected   int `json:"connected" example:"7"`
	Errors      int `json:"errors" example:"1"`
	Quarantined int `json:"quarantined" example:"1"`
}

// ServersResponse wraps server list with stats
type ServersResponse struct {
	Servers []*Server    `json:"servers"`
	Stats   *ServerStats `json:"stats"`
}

// ==============================================================================
// GET /api/v1/servers - List all servers
// ==============================================================================

// handleGetServers lists all upstream servers
// @Summary List all upstream servers
// @Description Returns all configured MCP servers with connection status, tool counts, and aggregate statistics
// @Tags servers
// @Accept json
// @Produce json
// @Param apikey query string false "API Key (alternative to header for browser/SSE)"
// @Success 200 {object} ServersResponse "List of servers with statistics"
// @Failure 401 {object} ErrorResponse "Unauthorized - missing or invalid API key"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /servers [get]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleGetServers(w http.ResponseWriter, r *http.Request) {
	// Implementation calls management service...
}

// ==============================================================================
// POST /api/v1/servers/{name}/restart - Restart server
// ==============================================================================

// handleRestartServer restarts a single server
// @Summary Restart an upstream server
// @Description Stops and restarts the connection to a specific upstream MCP server. Emits servers.changed event.
// @Tags servers
// @Accept json
// @Produce json
// @Param name path string true "Server name" example:"github-server"
// @Param apikey query string false "API Key (alternative to header)"
// @Success 200 {object} map[string]string "Success message"
// @Failure 400 {object} ErrorResponse "Bad request - invalid server name"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 403 {object} ErrorResponse "Forbidden - read-only mode or management disabled"
// @Failure 404 {object} ErrorResponse "Server not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /servers/{name}/restart [post]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
	// Implementation...
}

// ==============================================================================
// POST /api/v1/servers/restart_all - Restart all servers (NEW)
// ==============================================================================

// RestartAllResponse represents bulk operation result
type RestartAllResponse struct {
	SuccessCount int      `json:"success_count" example:"9"`
	FailedCount  int      `json:"failed_count" example:"1"`
	Errors       []string `json:"errors,omitempty" example:"[\"server-1: connection timeout\"]"`
}

// handleRestartAll restarts all servers
// @Summary Restart all upstream servers
// @Description Sequentially restarts all configured servers. Returns partial success if some servers fail.
// @Tags servers
// @Accept json
// @Produce json
// @Param apikey query string false "API Key"
// @Success 200 {object} RestartAllResponse "Restart results with success/failure counts"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 403 {object} ErrorResponse "Forbidden - read-only mode"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /servers/restart_all [post]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request) {
	// Implementation calls service.RestartAll()...
}

// ==============================================================================
// GET /api/v1/doctor - Health diagnostics (NEW)
// ==============================================================================

// Diagnostics represents system health information
type Diagnostics struct {
	TotalIssues      int                  `json:"total_issues" example:"3"`
	UpstreamErrors   []UpstreamError      `json:"upstream_errors"`
	OAuthRequired    []OAuthRequirement   `json:"oauth_required"`
	MissingSecrets   []MissingSecret      `json:"missing_secrets"`
	RuntimeWarnings  []string             `json:"runtime_warnings"`
	DockerStatus     *DockerStatus        `json:"docker_status,omitempty"`
	Timestamp        string               `json:"timestamp" example:"2025-11-23T10:35:12Z"`
}

type UpstreamError struct {
	ServerName   string `json:"server_name" example:"weather-api"`
	ErrorMessage string `json:"error_message" example:"connection refused"`
	Timestamp    string `json:"timestamp" example:"2025-11-23T09:15:30Z"`
}

type OAuthRequirement struct {
	ServerName string  `json:"server_name" example:"github-server"`
	State      string  `json:"state" example:"expired"`
	ExpiresAt  *string `json:"expires_at,omitempty" example:"2025-11-22T18:00:00Z"`
	Message    string  `json:"message" example:"Run: mcpproxy auth login --server=github-server"`
}

type MissingSecret struct {
	SecretName string   `json:"secret_name" example:"GITHUB_TOKEN"`
	UsedBy     []string `json:"used_by" example:"[\"github-server\",\"gh-issues\"]"`
}

type DockerStatus struct {
	Available bool   `json:"available" example:"true"`
	Version   string `json:"version,omitempty" example:"24.0.7"`
	Error     string `json:"error,omitempty" example:""`
}

// handleGetDiagnostics runs health checks
// @Summary Run health diagnostics
// @Description Aggregates health information from all servers, checks OAuth status, missing secrets, and Docker availability
// @Tags diagnostics
// @Accept json
// @Produce json
// @Param apikey query string false "API Key"
// @Success 200 {object} Diagnostics "Comprehensive health diagnostics"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /doctor [get]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleGetDiagnostics(w http.ResponseWriter, r *http.Request) {
	// Implementation calls service.Doctor()...
}

// ==============================================================================
// GET /api/v1/servers/{name}/logs - Get server logs
// ==============================================================================

// LogEntry represents a single log line
type LogEntry struct {
	Timestamp string `json:"timestamp" example:"2025-11-23T10:30:00Z"`
	Level     string `json:"level" example:"INFO"`
	Server    string `json:"server" example:"github-server"`
	Message   string `json:"message" example:"Tool call: create_issue"`
}

// handleGetServerLogs retrieves recent logs
// @Summary Get server logs
// @Description Returns recent log entries for a specific upstream server
// @Tags servers
// @Accept json
// @Produce json
// @Param name path string true "Server name" example:"github-server"
// @Param tail query int false "Number of lines to return (default 50, max 1000)" minimum:1 maximum:1000 default:50
// @Param apikey query string false "API Key"
// @Success 200 {array} LogEntry "Log entries"
// @Failure 400 {object} ErrorResponse "Bad request - invalid tail parameter"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 404 {object} ErrorResponse "Server not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /servers/{name}/logs [get]
// @Security ApiKeyAuth || ApiKeyQuery
func (s *Server) handleGetServerLogs(w http.ResponseWriter, r *http.Request) {
	// Implementation...
}

// ==============================================================================
// Generating the OpenAPI spec
// ==============================================================================

// To generate the OpenAPI specification, run:
//
//   go install github.com/swaggo/swag/cmd/swag@latest
//   swag init -g cmd/mcpproxy/main.go --output docs --outputTypes yaml
//
// This creates:
//   docs/swagger.yaml  - OpenAPI 3.x spec
//   docs/swagger.json  - JSON format
//   docs/docs.go       - Go code for embedding
//
// The spec is then served at:
//   http://localhost:8080/swagger/index.html
