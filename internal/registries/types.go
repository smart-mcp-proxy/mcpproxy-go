package registries

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/experiments"

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
	// RequiresKey marks a registry that needs an API key to be queried. When
	// true and no key is configured, SearchServers skips it with
	// ErrRegistryKeyMissing so the calling surface can mark it unavailable
	// instead of failing the whole search (FR-008).
	RequiresKey bool `json:"requires_key,omitempty"`
}

// ServerEntry represents an MCP server discovered via a registry
type ServerEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	URL           string `json:"url"`                       // MCP endpoint for remote server connections only
	SourceCodeURL string `json:"source_code_url,omitempty"` // URL to source code repository
	InstallCmd    string `json:"installCmd,omitempty"`      // Command to install the server locally
	ConnectURL    string `json:"connectUrl,omitempty"`      // Alternative connection URL for remote servers
	UpdatedAt     string `json:"updatedAt,omitempty"`
	CreatedAt     string `json:"createdAt,omitempty"`
	Registry      string `json:"registry,omitempty"` // Which registry this came from

	// Repository detection information
	RepositoryInfo *experiments.GuessResult `json:"repository_info,omitempty"` // Detected npm/pypi package info

	// RequiredInputs are env vars / keys the user must supply before the server
	// can run (FR-003 plumbing). Best-effort: populated either from explicit
	// registry payload fields or via a heuristic scan of the install command for
	// ${VAR} / $VAR placeholders (see DetectRequiredInputs). Empty for most
	// servers in this spec — no rich per-registry schema yet (decision O1).
	RequiredInputs []RequiredInput `json:"required_inputs,omitempty"`
}

// RequiredInput declares a single env var / key a server needs before it will
// work. Surfaces use this to prompt the user (FR-003).
type RequiredInput struct {
	Name        string `json:"name"`                  // Env var name (e.g. GITHUB_TOKEN)
	Description string `json:"description,omitempty"` // Optional human hint
	Secret      bool   `json:"secret,omitempty"`      // Mask in UI/logs when true
}
