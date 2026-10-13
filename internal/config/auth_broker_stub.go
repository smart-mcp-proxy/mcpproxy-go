//go:build !server

package config

import "encoding/json"

// AuthBrokerConfig is the personal-edition carrier for a server's
// `auth_broker` block. The connect flow is a server-edition feature (spec
// 074); the personal edition keeps the field on ServerConfig so configs round
// trip (Spec 107 FR-040): the block is held as the canonical JSON of what was
// read (canonicalRawJSON — every key and value kept, key order and whitespace
// normalised so jsonEqual-based change detection stays quiet), never
// interpreted, never validated and never warned about.
//
// Omission is the parent pointer's job: ServerConfig.AuthBroker is
// `*T,omitempty`, so a server without the key leaves the pointer nil and the
// key stays absent on write. A non-nil carrier with no raw bytes marshals as
// `{}`.
type AuthBrokerConfig struct {
	raw json.RawMessage
}

// UnmarshalJSON stores the document in canonical form (every key and value
// kept; key order and whitespace are not part of the contract).
func (a *AuthBrokerConfig) UnmarshalJSON(data []byte) error {
	raw, err := canonicalRawJSON(data)
	if err != nil {
		return err
	}
	a.raw = raw
	return nil
}

// MarshalJSON emits the stored document; an empty carrier is `{}`.
func (a AuthBrokerConfig) MarshalJSON() ([]byte, error) {
	if len(a.raw) == 0 {
		return []byte("{}"), nil
	}
	return append([]byte(nil), a.raw...), nil
}

// Clone returns a deep copy of the carrier (nil-safe). CopyServerConfig uses
// it so a copied server never aliases the source's backing array.
func (a *AuthBrokerConfig) Clone() *AuthBrokerConfig {
	if a == nil {
		return nil
	}
	return &AuthBrokerConfig{raw: append(json.RawMessage(nil), a.raw...)}
}

// validateServerAuthBroker is a no-op in the personal edition.
func validateServerAuthBroker(_ *ServerConfig, _ string) []ValidationError {
	return nil
}
