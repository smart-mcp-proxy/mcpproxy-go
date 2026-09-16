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
	// Producer is the authorization the entry was produced under (Spec 104
	// FR-016a). nil on entries persisted before stamping existed or written
	// through Store; together with Version it decides the entry's provenance
	// class (see HasCurrentProvenance).
	Producer *Authorization `json:"producer,omitempty"`
	// Version is the provenance schema the entry was written under (Spec 105
	// FR-002). 0/absent marks a record persisted before this field existed;
	// any value other than RecordVersion is provenance this binary does not
	// recognise. Both are legacy: refused for every caller and invalidated on
	// the first gated read.
	Version uint8 `json:"version,omitempty"`
}

// RecordVersion is the provenance schema current binaries stamp on every
// record they write. Bump it only when the meaning of Producer changes in a
// way older readers must not trust — a bump makes every existing entry legacy.
const RecordVersion uint8 = 1

// HasCurrentProvenance reports whether the record carries a producer stamp
// written under the current provenance schema. Anything else — no producer,
// no version, a version this binary does not know — is legacy provenance
// (Spec 105 FR-002): the gated read refuses it for every caller kind and
// invalidates it.
func (c *Record) HasCurrentProvenance() bool {
	return c.Producer != nil && c.Version == RecordVersion
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
	// Producer is the authorization snapshot the paged entry was produced
	// under. It never reaches the wire: the read_cache handler carries it to
	// the store of a recursively re-truncated page so provenance stays
	// monotone down the chain — a child page is stamped with its PARENT's
	// snapshot, never with the (possibly broader) redeemer's (Spec 105
	// FR-001).
	Producer *Authorization `json:"-"`
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
