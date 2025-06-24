package cache

import (
	"encoding/json"
	"time"
)

// Record represents a cached tool response
type Record struct {
	Key          string                 `json:"key"`
	ToolName     string                 `json:"tool_name"`
	Args         map[string]interface{} `json:"args"`
	Timestamp    time.Time              `json:"timestamp"`
	FullContent  string                 `json:"full_content"`
	RecordPath   string                 `json:"record_path,omitempty"`   // JSON path to records array
	TotalRecords int                    `json:"total_records,omitempty"` // Total number of records
	TotalSize    int                    `json:"total_size"`              // Full response size in characters
	ExpiresAt    time.Time              `json:"expires_at"`
	AccessCount  int                    `json:"access_count"`
	LastAccessed time.Time              `json:"last_accessed"`
	CreatedAt    time.Time              `json:"created_at"`
}

// Stats represents cache statistics
type Stats struct {
	TotalEntries   int `json:"total_entries"`
	TotalSizeBytes int `json:"total_size_bytes"`
	HitCount       int `json:"hit_count"`
	MissCount      int `json:"miss_count"`
	EvictedCount   int `json:"evicted_count"`
	CleanupCount   int `json:"cleanup_count"`
}

// ReadCacheResponse represents the response structure for read_cache tool
type ReadCacheResponse struct {
	Records []interface{} `json:"records"`
	Meta    Meta          `json:"meta"`
}

// Meta represents metadata about the cached response
type Meta struct {
	Key          string `json:"key"`
	TotalRecords int    `json:"total_records"`
	Limit        int    `json:"limit"`
	Offset       int    `json:"offset"`
	TotalSize    int    `json:"total_size"`
	RecordPath   string `json:"record_path,omitempty"`
}

// MarshalBinary implements encoding.BinaryMarshaler for Record
func (c *Record) MarshalBinary() ([]byte, error) {
	return json.Marshal(c)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler for Record
func (c *Record) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, c)
}

// MarshalBinary implements encoding.BinaryMarshaler for Stats
func (s *Stats) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler for Stats
func (s *Stats) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}

// IsExpired checks if the cache record has expired
func (c *Record) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}
