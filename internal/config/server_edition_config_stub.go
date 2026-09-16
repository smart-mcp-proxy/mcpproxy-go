//go:build !server

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// canonicalRawJSON re-encodes one JSON value in canonical form: object keys
// sorted, whitespace removed, numbers kept as their exact decimal text
// (json.Number). The personal-build carriers store THIS rather than the source
// bytes, so two documents that differ only in key order or layout marshal
// identically. That matters because DetectConfigChanges compares mcpServers
// with jsonEqual (the bytes of json.Marshal) while PATCH /api/v1/config
// re-emits every block from a generic map in lexical order: a verbatim carrier
// made the first unrelated PATCH after boot report "mcpServers" changed and
// reconnect every upstream (codex round 1 on PR-A). Semantic content — every
// key and every value — is unchanged, which is the FR-040 contract.
func canonicalRawJSON(data []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode opaque block: %w", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("re-encode opaque block: %w", err)
	}
	return out, nil
}

// ServerEditionConfig is the personal-edition carrier for the `server_edition`
// block. Server edition features are not available here, but the block must
// survive load → save → PATCH → save intact in meaning (Spec 107 FR-040): it
// is held as the canonical JSON of what was read (canonicalRawJSON), never
// interpreted, never validated, never normalised and never warned about.
//
// Omission is the parent pointer's job: Config.ServerEdition is `*T,omitempty`,
// so a document without the key leaves the pointer nil and the key stays
// absent on write, and a JSON `null` also decodes to a nil pointer. A non-nil
// carrier with no raw bytes (only constructible from Go code) marshals as `{}`.
type ServerEditionConfig struct {
	raw json.RawMessage
}

// UnmarshalJSON stores the document in canonical form (every key and value
// kept; key order and whitespace are not part of the contract).
func (c *ServerEditionConfig) UnmarshalJSON(data []byte) error {
	raw, err := canonicalRawJSON(data)
	if err != nil {
		return err
	}
	c.raw = raw
	return nil
}

// MarshalJSON emits the stored document; an empty carrier is `{}`.
func (c ServerEditionConfig) MarshalJSON() ([]byte, error) {
	if len(c.raw) == 0 {
		return []byte("{}"), nil
	}
	return append([]byte(nil), c.raw...), nil
}

// Clone returns a deep copy of the carrier (nil-safe). It is provided for
// symmetry with the server build and pinned by a unit test; nothing copies the
// top-level block today (the config snapshot shares the pointer and nothing
// mutates it in the personal build).
func (c *ServerEditionConfig) Clone() *ServerEditionConfig {
	if c == nil {
		return nil
	}
	return &ServerEditionConfig{raw: append(json.RawMessage(nil), c.raw...)}
}
