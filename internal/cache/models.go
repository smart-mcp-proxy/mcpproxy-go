package cache

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
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
// no version, a version this binary does not know, a caller kind it does not
// know — is legacy provenance (Spec 105 FR-002): the gated read refuses it
// for every caller kind and invalidates it. The kind is checked structurally
// rather than trusting the version alone: a later binary that adds a kind
// without bumping RecordVersion, followed by a rollback, must not leave an
// entry the administrator gate would wave through (it accepts any
// non-internal kind for an administrator reader).
func (c *Record) HasCurrentProvenance() bool {
	return c.header().HasCurrentProvenance()
}

// recordHeader is the fixed, payload-free part of a stored record: everything
// the gated read needs to refuse — provenance class, internal kind, expiry,
// the producer snapshot for the guard, and the size the eviction stats fold
// in. MarshalBinary writes it in front of the record body so the gate can
// decode it alone (see decodeRecordHeader); a refusal must not do work
// proportional to the payload it refuses, or a nonexistent key and a refused
// one fall into different timing classes (Spec 105 Definitions,
// "non-disclosing refusal"). It is derived from the Record at marshal time,
// so the two never disagree on a record this binary wrote.
type recordHeader struct {
	Version   uint8          `json:"version,omitempty"`
	Producer  *Authorization `json:"producer,omitempty"`
	ExpiresAt time.Time      `json:"expires_at"`
	TotalSize int            `json:"total_size"`
}

func (c *Record) header() recordHeader {
	return recordHeader{Version: c.Version, Producer: c.Producer, ExpiresAt: c.ExpiresAt, TotalSize: c.TotalSize}
}

// HasCurrentProvenance is Record.HasCurrentProvenance decided on the header.
func (h recordHeader) HasCurrentProvenance() bool {
	return h.Producer != nil && h.Version == RecordVersion && IsKnownCallerKind(h.Producer.CallerKind)
}

func (h recordHeader) expired() bool {
	return time.Now().After(h.ExpiresAt)
}

// Stored value layout, written by MarshalBinary:
//
//	recordFrameMagic | uint32 big-endian header length | header JSON | record JSON
//
// The magic starts with a NUL byte, which no JSON document does, so a value
// without it is a record a pre-frame binary wrote as bare JSON: UnmarshalBinary
// still decodes it (the ungated readers and the cleanup sweep keep working
// across the upgrade), while the gated read treats the missing header as the
// legacy provenance it is (Spec 105 FR-002).
var recordFrameMagic = []byte("\x00mcpproxy-cache-record\x01")

const (
	recordFrameLenSize = 4
	// maxRecordHeaderLen bounds the header decode: a header is a version, a
	// producer snapshot (a few server names and permissions) and two small
	// scalars — kilobytes at the very most. A frame claiming more is
	// corrupt, and decoding it would be work proportional to a caller-chosen
	// length rather than to the header.
	maxRecordHeaderLen = 64 << 10
)

var (
	errRecordUnframed       = errors.New("cache record has no frame header (written before frame headers existed)")
	errRecordFrameCorrupt   = errors.New("cache record frame header is corrupt")
	errRecordHeaderOversize = fmt.Errorf("%w: header length exceeds %d bytes", errRecordFrameCorrupt, maxRecordHeaderLen)
)

// encodeRecordFrame lays header and body out as MarshalBinary stores them.
func encodeRecordFrame(header, body []byte) []byte {
	out := make([]byte, 0, len(recordFrameMagic)+recordFrameLenSize+len(header)+len(body))
	out = append(out, recordFrameMagic...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(header)))
	out = append(out, header...)
	return append(out, body...)
}

// splitRecordFrame separates a stored value into its header and body bytes.
// It touches only the fixed-size prefix: the returned slices alias data. A
// value without the magic is unframed — the whole value is the body.
func splitRecordFrame(data []byte) (header, body []byte, err error) {
	if !bytes.HasPrefix(data, recordFrameMagic) {
		return nil, data, errRecordUnframed
	}
	rest := data[len(recordFrameMagic):]
	if len(rest) < recordFrameLenSize {
		return nil, nil, errRecordFrameCorrupt
	}
	n := binary.BigEndian.Uint32(rest)
	if n > maxRecordHeaderLen {
		return nil, nil, errRecordHeaderOversize
	}
	rest = rest[recordFrameLenSize:]
	if uint64(n) > uint64(len(rest)) {
		return nil, nil, errRecordFrameCorrupt
	}
	return rest[:n], rest[n:], nil
}

// decodeRecordHeader decodes only the frame header of a stored value — O(header),
// never O(payload). A value without a frame, or with a frame this binary
// cannot decode, is reported as an error with a zero header (TotalSize 0: the
// size of such a record is unknown without decoding it, which the gate must
// not do).
func decodeRecordHeader(data []byte) (recordHeader, error) {
	var h recordHeader
	raw, _, err := splitRecordFrame(data)
	if err != nil {
		return recordHeader{}, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return recordHeader{}, fmt.Errorf("%w: %w", errRecordFrameCorrupt, err)
	}
	return h, nil
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

// MarshalBinary implements encoding.BinaryMarshaler for Record: the frame
// header (derived from the record) followed by the record as JSON.
func (c *Record) MarshalBinary() ([]byte, error) {
	header, err := json.Marshal(c.header())
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return encodeRecordFrame(header, body), nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler for Record. It
// accepts both the framed layout and the bare JSON a pre-frame binary wrote;
// the header, when present, is skipped — the body carries every field.
func (c *Record) UnmarshalBinary(data []byte) error {
	_, body, err := splitRecordFrame(data)
	if err != nil && !errors.Is(err, errRecordUnframed) {
		return err
	}
	return json.Unmarshal(body, c)
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
