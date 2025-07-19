package registries

import "mcpproxy-go/internal/experiments"

// RegistryEntry represents a registry in the embedded registry list
type RegistryEntry struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	URL         string      `json:"url"`
	ServersURL  string      `json:"servers_url,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Protocol    string      `json:"protocol,omitempty"`
	Count       interface{} `json:"count,omitempty"` // number or string
}

// ServerEntry represents an MCP server discovered via a registry
type ServerEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`                  // MCP endpoint for direct connection
	InstallCmd  string `json:"installCmd,omitempty"` // Command to install the server locally
	ConnectURL  string `json:"connectUrl,omitempty"` // Alternative connection URL for remote servers
	UpdatedAt   string `json:"updatedAt,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	Registry    string `json:"registry,omitempty"` // Which registry this came from

	// Repository detection information
	RepositoryInfo *experiments.GuessResult `json:"repository_info,omitempty"` // Detected npm/pypi package info
}
