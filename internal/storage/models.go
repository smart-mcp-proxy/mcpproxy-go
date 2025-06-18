package storage

import (
	"encoding/json"
	"time"
)

// Bucket names for bbolt database
const (
	UpstreamsBucket = "upstreams"
	ToolStatsBucket = "toolstats"
	ToolHashBucket  = "toolhash"
	MetaBucket      = "meta"
)

// Meta keys
const (
	SchemaVersionKey = "schema"
)

// Current schema version
const CurrentSchemaVersion = 1

// UpstreamRecord represents an upstream server record in storage
type UpstreamRecord struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
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
