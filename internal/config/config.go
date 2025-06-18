package config

import (
	"encoding/json"
	"time"
)

// Config represents the main configuration structure
type Config struct {
	Listen     string          `json:"listen" mapstructure:"listen"`
	DataDir    string          `json:"data_dir" mapstructure:"data-dir"`
	EnableTray bool            `json:"enable_tray" mapstructure:"tray"`
	Servers    []*ServerConfig `json:"mcpServers" mapstructure:"servers"`
	TopK       int             `json:"top_k" mapstructure:"top-k"`
	ToolsLimit int             `json:"tools_limit" mapstructure:"tools-limit"`
}

// ServerConfig represents upstream MCP server configuration
type ServerConfig struct {
	Name    string            `json:"name,omitempty" mapstructure:"name"`
	URL     string            `json:"url,omitempty" mapstructure:"url"`
	Type    string            `json:"type,omitempty" mapstructure:"type"` // http, stdio, auto
	Command string            `json:"command,omitempty" mapstructure:"command"`
	Args    []string          `json:"args,omitempty" mapstructure:"args"`
	Env     map[string]string `json:"env,omitempty" mapstructure:"env"`
	Enabled bool              `json:"enabled" mapstructure:"enabled"`
	Created time.Time         `json:"created" mapstructure:"created"`
}

// ToolMetadata represents tool information stored in the index
type ToolMetadata struct {
	Name        string    `json:"name"`
	ServerName  string    `json:"server_name"`
	Description string    `json:"description"`
	ParamsJSON  string    `json:"params_json"`
	Hash        string    `json:"hash"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// ToolRegistration represents a tool registration
type ToolRegistration struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	ServerName   string                 `json:"server_name"`
	OriginalName string                 `json:"original_name"`
}

// SearchResult represents a search result with score
type SearchResult struct {
	Tool  *ToolMetadata `json:"tool"`
	Score float64       `json:"score"`
}

// ToolStats represents tool statistics
type ToolStats struct {
	TotalTools int             `json:"total_tools"`
	TopTools   []ToolStatEntry `json:"top_tools"`
}

// ToolStatEntry represents a single tool stat entry
type ToolStatEntry struct {
	ToolName string `json:"tool_name"`
	Count    uint64 `json:"count"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Listen:     ":8080",
		DataDir:    "", // Will be set to ~/.mcpproxy by loader
		EnableTray: true,
		Servers:    []*ServerConfig{},
		TopK:       5,
		ToolsLimit: 15,
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.TopK <= 0 {
		c.TopK = 5
	}
	if c.ToolsLimit <= 0 {
		c.ToolsLimit = 15
	}
	return nil
}

// MarshalJSON implements json.Marshaler interface
func (c *Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	return json.Marshal((*Alias)(c))
}

// UnmarshalJSON implements json.Unmarshaler interface
func (c *Config) UnmarshalJSON(data []byte) error {
	type Alias Config
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	}
	return json.Unmarshal(data, aux)
}
