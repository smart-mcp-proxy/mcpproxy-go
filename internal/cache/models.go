package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// recordHeader is the FIXED-SIZE, payload-free part of a stored record:
// everything the gated read needs to decide — provenance class (version,
// caller kind), the deny-all bit, the producer's permission tier bits, expiry,
// the size the eviction stats fold out, and the DIGEST of the producer's
// effective authorization. MarshalBinary writes it in front of the record
// body so the gate can decode it alone (decodeRecordHeader) and decide every
// verdict from it in O(1) (research D16): a refusal must not do work
// proportional to the payload it refuses, nor to the producer snapshot (an
// authorization naming thousands of servers) — a nonexistent key does
// neither, and a non-disclosing refusal is indistinguishable from it in
// timing class (Spec 105 Definitions; codex rounds 2, 4 and 5). The snapshot
// itself is kept once per distinct authorization in the snapshots bucket,
// keyed by the same digest, for administrator diagnostics only: no read path
// consults it. The header is derived from the Record at marshal time, so the
// two never disagree on a record this binary wrote — and UnmarshalBinary
// refuses a value on which they do.
type recordHeader struct {
	Version  uint8
	KindCode uint8
	// Kind is the caller kind KindCode names; "" for code 0 (no producer)
	// and for a code this binary does not know.
	Kind string
	// DenyAll is Producer.DenyAll() at marshal time: a scoped snapshot that
	// could have authorized nothing, refused for scoped readers on the
	// header alone.
	DenyAll bool
	// Perms is permissionBits(Producer.Permissions): the tier set an
	// unrestricted reader must cover.
	Perms     uint8
	ExpiresAt time.Time
	TotalSize int
	// Digest is Producer.digest(): the content address of the producer's
	// effective authorization; zero for an unstamped record.
	Digest [sha256.Size]byte
}

func (c *Record) header() recordHeader {
	h := recordHeader{Version: c.Version, ExpiresAt: c.ExpiresAt, TotalSize: c.TotalSize}
	if c.Producer != nil {
		h.KindCode = callerKindCode(c.Producer.CallerKind)
		h.Kind = callerKindFromCode(h.KindCode)
		h.DenyAll = c.Producer.DenyAll()
		h.Perms = permissionBits(c.Producer.Permissions)
		h.Digest = c.Producer.digest()
	}
	return h
}

// facts is the producer side of the read gate as this header carries it.
func (h recordHeader) facts() producerFacts {
	return producerFacts{Kind: h.Kind, DenyAll: h.DenyAll, Perms: h.Perms, Digest: h.Digest}
}

// snapshotBytes is the diagnostic encoding of a producer snapshot: the JSON
// of the Authorization as the request carried it (profile name and list
// order included). It is what the snapshots bucket stores under the
// producer's digest, for an administrator to inspect; the gate never reads
// it. It is never bounded: any authorization the proxy can mint fits.
func snapshotBytes(a Authorization) []byte {
	data, err := json.Marshal(a)
	if err != nil {
		// Authorization is strings, string slices and a bool: json.Marshal
		// cannot fail on it.
		panic(fmt.Sprintf("cache: marshal authorization snapshot: %v", err))
	}
	return data
}

// HasCurrentProvenance is Record.HasCurrentProvenance decided on the header.
func (h recordHeader) HasCurrentProvenance() bool {
	return h.KindCode != 0 && h.Version == RecordVersion && IsKnownCallerKind(h.Kind)
}

func (h recordHeader) expired() bool {
	return time.Now().After(h.ExpiresAt)
}

// Stored value layout, written by MarshalBinary:
//
//	recordFrameMagic | fixed header (recordHeaderSize bytes) | record JSON
//
// Fixed header, big-endian:
//
//	[0]     version
//	[1]     caller kind code (callerKindCodes; 0 = no producer)
//	[2]     flags (recordFlagDenyAll)
//	[3]     producer permission tier bits (permissionBits)
//	[4:12]  expires_at, Unix nanoseconds
//	[12:20] total_size
//	[20:52] producer effective-authorization digest (Authorization.digest)
//
// The magic starts with a NUL byte, which no JSON document does, so a value
// without it is a record a pre-frame binary wrote as bare JSON: UnmarshalBinary
// still decodes it (the ungated readers and the cleanup sweep keep working
// across the upgrade), while the gated read treats the missing header as the
// legacy provenance it is (Spec 105 FR-002). The magic's last byte is the
// frame layout version: a frame with another one is unrecognised provenance.
var recordFrameMagic = []byte("\x00mcpproxy-cache-record\x03")

const (
	recordHeaderSize   = 4 + 8 + 8 + sha256.Size
	recordFlagDenyAll  = 1 << 0
	recordHeaderOffVer = 0
	recordHeaderOffKnd = 1
	recordHeaderOffFlg = 2
	recordHeaderOffPrm = 3
	recordHeaderOffExp = 4
	recordHeaderOffSiz = 12
	recordHeaderOffDig = 20
)

var (
	errRecordUnframed      = errors.New("cache record has no frame header (written before frame headers existed)")
	errRecordFrameCorrupt  = errors.New("cache record frame header is corrupt")
	errRecordFrameMismatch = fmt.Errorf("%w: header disagrees with the record body", errRecordFrameCorrupt)
)

// encodeExpiry is the header's encoding of an expiry instant. A zero time
// (never written by storeRecord) encodes as 0 — the Unix epoch, long expired
// — rather than the undefined UnixNano of the year 1.
func encodeExpiry(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func (h recordHeader) encode() []byte {
	out := make([]byte, recordHeaderSize)
	out[recordHeaderOffVer] = h.Version
	out[recordHeaderOffKnd] = h.KindCode
	if h.DenyAll {
		out[recordHeaderOffFlg] |= recordFlagDenyAll
	}
	out[recordHeaderOffPrm] = h.Perms
	binary.BigEndian.PutUint64(out[recordHeaderOffExp:], uint64(encodeExpiry(h.ExpiresAt)))
	binary.BigEndian.PutUint64(out[recordHeaderOffSiz:], uint64(int64(h.TotalSize)))
	copy(out[recordHeaderOffDig:], h.Digest[:])
	return out
}

func decodeHeaderBytes(raw []byte) (recordHeader, error) {
	if len(raw) != recordHeaderSize {
		return recordHeader{}, errRecordFrameCorrupt
	}
	h := recordHeader{
		Version:  raw[recordHeaderOffVer],
		KindCode: raw[recordHeaderOffKnd],
		DenyAll:  raw[recordHeaderOffFlg]&recordFlagDenyAll != 0,
		Perms:    raw[recordHeaderOffPrm],
	}
	h.Kind = callerKindFromCode(h.KindCode)
	h.ExpiresAt = time.Unix(0, int64(binary.BigEndian.Uint64(raw[recordHeaderOffExp:])))
	size := int64(binary.BigEndian.Uint64(raw[recordHeaderOffSiz:]))
	if size < 0 || size > math.MaxInt {
		return recordHeader{}, errRecordFrameCorrupt
	}
	h.TotalSize = int(size)
	copy(h.Digest[:], raw[recordHeaderOffDig:])
	return h, nil
}

// encodeRecordFrame lays header and body out as MarshalBinary stores them.
func encodeRecordFrame(header, body []byte) []byte {
	prefix := len(recordFrameMagic) + len(header)
	capHint := prefix
	if len(body) <= math.MaxInt-prefix {
		capHint += len(body)
	}
	out := make([]byte, 0, capHint)
	out = append(out, recordFrameMagic...)
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
	if len(rest) < recordHeaderSize {
		return nil, nil, errRecordFrameCorrupt
	}
	return rest[:recordHeaderSize], rest[recordHeaderSize:], nil
}

// decodeRecordHeader decodes only the frame header of a stored value — a
// fixed number of bytes, never O(payload) and never O(snapshot). A value
// without a frame, or with a frame this binary cannot decode, is reported as
// an error with a zero header (TotalSize 0: the size of such a record is
// unknown without decoding it, which the gate must not do — the cleanup
// sweep reconciles the size statistics from the bucket instead).
func decodeRecordHeader(data []byte) (recordHeader, error) {
	raw, _, err := splitRecordFrame(data)
	if err != nil {
		return recordHeader{}, err
	}
	return decodeHeaderBytes(raw)
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

// MarshalBinary implements encoding.BinaryMarshaler for Record: the fixed
// frame header (derived from the record) followed by the record as JSON.
// There is no size bound: the header is fixed-size and names the producer
// by digest, so any authorization the proxy can mint fits (codex round 4).
// The diagnostic snapshot is persisted by the store path
// (Manager.storeRecord) in the same transaction — see marshalFrame.
func (c *Record) MarshalBinary() ([]byte, error) {
	data, _, err := c.marshalFrame()
	return data, err
}

// marshalFrame is MarshalBinary plus the diagnostic snapshot bytes for the
// producer the header names (nil for an unstamped record), so a store can
// persist both from one encoding.
func (c *Record) marshalFrame() (data, snapshot []byte, err error) {
	body, err := json.Marshal(c)
	if err != nil {
		return nil, nil, err
	}
	if c.Producer != nil {
		snapshot = snapshotBytes(*c.Producer)
	}
	return encodeRecordFrame(c.header().encode(), body), snapshot, nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler for Record. It
// accepts both the framed layout and the bare JSON a pre-frame binary wrote.
// The body carries every field; the header, when present, must AGREE with
// it — exactly, on every field it carries — or the value is corrupt. The
// gated read admits a reader on the header alone, so a header that promised
// a narrower producer, a later expiry or another size than the body it
// fronts would hand that reader a body the header never authorized (codex
// round 3, finding 1). No binary of this repository writes such a value
// (MarshalBinary derives the header from the record), so it is refused like
// any other undecodable frame: errRecordFrameMismatch, and the record is
// left zero — a caller never sees the body.
func (c *Record) UnmarshalBinary(data []byte) error {
	header, body, err := splitRecordFrame(data)
	if err != nil && !errors.Is(err, errRecordUnframed) {
		return err
	}
	if err := json.Unmarshal(body, c); err != nil {
		return err
	}
	if header == nil {
		return nil
	}
	h, err := decodeHeaderBytes(header)
	if err != nil {
		*c = Record{}
		return err
	}
	if !h.agreesWith(c.header()) {
		*c = Record{}
		return errRecordFrameMismatch
	}
	return nil
}

// agreesWith reports whether two headers are equal field for field: the same
// version, kind, deny-all bit, tier bits, expiry instant and size, and the
// same producer digest — so every dimension the gate decides on (kind,
// principal, server grant, permissions, pin, profile scope and profile
// server set) must match; an unstamped record digests to zero and agrees
// only with an unstamped header.
func (h recordHeader) agreesWith(o recordHeader) bool {
	return h.Version == o.Version &&
		h.KindCode == o.KindCode &&
		h.DenyAll == o.DenyAll &&
		h.Perms == o.Perms &&
		encodeExpiry(h.ExpiresAt) == encodeExpiry(o.ExpiresAt) &&
		h.TotalSize == o.TotalSize &&
		h.Digest == o.Digest
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
