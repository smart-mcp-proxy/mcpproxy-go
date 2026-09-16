//go:build !server

package config

import "encoding/json"

// ServerEditionConfig is the personal-edition carrier for the `server_edition`
// block. Server edition features are not available here, but the block must
// survive load → save → PATCH → save byte-for-byte in meaning (Spec 107
// FR-040): it is held as the raw JSON it was read from, never decoded, never
// validated, never normalised and never warned about.
//
// Omission is the parent pointer's job: Config.ServerEdition is `*T,omitempty`,
// so a document without the key leaves the pointer nil and the key stays
// absent on write, and a JSON `null` also decodes to a nil pointer. A non-nil
// carrier with no raw bytes (only constructible from Go code) marshals as `{}`.
type ServerEditionConfig struct {
	raw json.RawMessage
}

// UnmarshalJSON stores the document verbatim.
func (c *ServerEditionConfig) UnmarshalJSON(data []byte) error {
	c.raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON emits the stored document verbatim; an empty carrier is `{}`.
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
