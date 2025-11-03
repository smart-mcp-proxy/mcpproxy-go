package storage

import (
	"encoding/json"
	"mcpproxy-go/internal/config"
	"time"
)

// Bucket names for bbolt database
const (
	UpstreamsBucket       = "upstreams"
	ToolStatsBucket       = "toolstats"
	ToolHashBucket        = "toolhash"
	OAuthTokenBucket      = "oauth_tokens" //nolint:gosec // bucket name, not a credential
	OAuthCompletionBucket = "oauth_completion"
	MetaBucket            = "meta"
	CacheBucket           = "cache"
	CacheStatsBucket      = "cache_stats"
)

// Meta keys
const (
	SchemaVersionKey       = "schema"
	DockerRecoveryStateKey = "docker_recovery_state"
)

// Current schema version
const CurrentSchemaVersion = 2

// UpstreamRecord represents an upstream server record in storage
type UpstreamRecord struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	URL         string                  `json:"url,omitempty"`
	Protocol    string                  `json:"protocol,omitempty"` // stdio, http, sse, streamable-http, auto
	Command     string                  `json:"command,omitempty"`
	Args        []string                `json:"args,omitempty"`
	WorkingDir  string                  `json:"working_dir,omitempty"` // Working directory for stdio servers
	Env         map[string]string       `json:"env,omitempty"`
	Headers     map[string]string       `json:"headers,omitempty"` // For HTTP authentication
	OAuth       *config.OAuthConfig     `json:"oauth,omitempty"`   // OAuth configuration
	Enabled     bool                    `json:"enabled"`
	Quarantined bool                    `json:"quarantined"` // Security quarantine status
	Created     time.Time               `json:"created"`
	Updated     time.Time               `json:"updated"`
	Isolation   *config.IsolationConfig `json:"isolation,omitempty"` // Per-server isolation settings
}

// ToolStatRecord represents tool usage statistics
type ToolStatRecord struct {
	ToolName string    `json:"tool_name"`
	Count    uint64    `json:"count"`
	LastUsed time.Time `json:"last_used"`
}

// ToolHashRecord represents a tool hash for change detection
type ToolHashRecord struct {
	ToolName string    `json:"tool_name"`
	Hash     string    `json:"hash"`
	Updated  time.Time `json:"updated"`
}

// OAuthTokenRecord represents stored OAuth tokens for a server
type OAuthTokenRecord struct {
	ServerName   string    `json:"server_name"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}

// OAuthCompletionEvent represents an OAuth completion event for cross-process notification
type OAuthCompletionEvent struct {
	ServerName  string     `json:"server_name"`
	CompletedAt time.Time  `json:"completed_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"` // Nil if not yet processed by server
}

// DockerRecoveryState represents the persistent state of Docker recovery
// This allows recovery to resume after application restart
type DockerRecoveryState struct {
	LastAttempt      time.Time `json:"last_attempt"`
	FailureCount     int       `json:"failure_count"`
	DockerAvailable  bool      `json:"docker_available"`
	RecoveryMode     bool      `json:"recovery_mode"`
	LastError        string    `json:"last_error,omitempty"`
	AttemptsSinceUp  int       `json:"attempts_since_up"` // Attempts since Docker was last available
	LastSuccessfulAt time.Time `json:"last_successful_at,omitempty"`
}

// MarshalBinary implements encoding.BinaryMarshaler
func (u *UpstreamRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(u)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (u *UpstreamRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, u)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (t *ToolStatRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(t)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (t *ToolStatRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, t)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (h *ToolHashRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(h)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (h *ToolHashRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, h)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (o *OAuthTokenRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(o)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (o *OAuthTokenRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, o)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (e *OAuthCompletionEvent) MarshalBinary() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (e *OAuthCompletionEvent) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, e)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (d *DockerRecoveryState) MarshalBinary() ([]byte, error) {
	return json.Marshal(d)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (d *DockerRecoveryState) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, d)
}
