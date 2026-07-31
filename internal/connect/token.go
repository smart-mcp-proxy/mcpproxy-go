package connect

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
)

// tokenDomain namespaces the HMAC input so a token can never be confused with a
// digest computed elsewhere over similar-looking material.
const tokenDomain = "mcpproxy/connect/precondition/v1"

// preconditionKeyLen is the per-core-instance random key size (256 bits, matching
// the HMAC-SHA256 block security level).
const preconditionKeyLen = 32

// DerivePreconditionToken computes the opaque precondition token that binds a
// rendered connect preview to the exact pre-write state it described (Spec 091
// FR-005).
//
// It is an HMAC-SHA256, hex-encoded, over a canonical LENGTH-PREFIXED encoding
// of every input, so:
//
//   - no two distinct states share a preimage — a byte moved from one field into
//     the next changes the encoding, which a delimiter- or concatenation-based
//     scheme would not catch;
//   - the token is KEYED with a per-core-instance random in-memory key, so it is
//     not an offline confirmation oracle: an attacker who knows everything about
//     the state except a masked or weak credential still cannot test guesses.
//
// The hashed state is exactly what the write must find unchanged:
//
//	configPath          — which file is being written
//	fileExists          — create vs. update (a file appearing or vanishing)
//	resolvedEntryName   — the key the write would replace, AFTER the same
//	                      equivalent-entry adoption the write performs
//	rawResolvedEntry    — that entry's value; nil when there is none. Includes
//	                      values the sanitized summary deliberately hides, so a
//	                      credential rotation inside the existing entry drifts.
//	pendingEntry        — the entry the proxy would write right now, so
//	                      proxy-side drift (API-key rotation, require_mcp_auth
//	                      toggle, listen-address change) invalidates the preview
//	                      too — otherwise a credential could be embedded without
//	                      the FR-004 notice ever having been shown.
//
// The token is never persisted and never leaves this process except as an opaque
// string in the preview response.
func DerivePreconditionToken(key []byte, configPath string, fileExists bool, resolvedEntryName string, rawResolvedEntry, pendingEntry json.RawMessage) string {
	mac := hmac.New(sha256.New, key)
	writeTokenField(mac, []byte(tokenDomain))
	writeTokenField(mac, []byte(configPath))
	writeTokenField(mac, []byte{boolByte(fileExists)})
	// Presence is its own field so "entry absent" and "entry present but empty"
	// can never encode identically.
	writeTokenField(mac, []byte{boolByte(rawResolvedEntry != nil)})
	writeTokenField(mac, []byte(resolvedEntryName))
	writeTokenField(mac, rawResolvedEntry)
	writeTokenField(mac, pendingEntry)
	return hex.EncodeToString(mac.Sum(nil))
}

// writeTokenField appends one length-prefixed field (8-byte big-endian length,
// then the bytes) to the running MAC. hash.Hash writes never fail.
func writeTokenField(h hash.Hash, field []byte) {
	var lengthPrefix [8]byte
	binary.BigEndian.PutUint64(lengthPrefix[:], uint64(len(field)))
	_, _ = h.Write(lengthPrefix[:])
	_, _ = h.Write(field)
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// preconditionKey returns this Service's in-memory HMAC key, generating it on
// first use. The key lives only for the life of the process, which is precisely
// the intended token lifetime: a preview minted by one core instance is
// meaningless to another (and to a restarted one), so a stale token can never
// authorize a write against state it never observed.
func (s *Service) preconditionKey() []byte {
	s.tokenKeyOnce.Do(func() {
		key := make([]byte, preconditionKeyLen)
		if _, err := rand.Read(key); err != nil {
			// crypto/rand failure is fatal for the guarantee; fall back to a
			// key that still differs per Service value rather than a constant.
			panic("connect: cannot generate precondition key: " + err.Error())
		}
		s.tokenKey = key
	})
	return s.tokenKey
}

// preconditionToken derives the token for a resolved pre-write state under this
// Service's key. existing is nil when no entry would be replaced.
//
// The raw entry is re-marshaled canonically (encoding/json sorts object keys)
// rather than hashed as source bytes, so reformatting the config alone does not
// invalidate a preview while any semantic change to the entry does.
func (s *Service) preconditionToken(cfgPath string, fileExists bool, existing *existingEntry, pendingEntry map[string]interface{}) string {
	var resolvedName string
	var rawResolved json.RawMessage
	if existing != nil {
		resolvedName = existing.name
		rawResolved = canonicalJSON(existing.entry)
	}
	return DerivePreconditionToken(s.preconditionKey(), cfgPath, fileExists, resolvedName, rawResolved, canonicalJSON(pendingEntry))
}

// actionPreconditionFailed is the machine-readable discriminator for a write
// refused because the preview it echoed no longer describes reality. It is
// deliberately distinct from "already_exists": that one means "an entry is
// there, pass force"; this one means "your view is stale, re-preview" — a client
// that confused the two would either loop forever or force a write over state
// the user never saw (research D9).
const actionPreconditionFailed = "precondition_failed"

// checkPrecondition recomputes the token for the CURRENT pre-write state and
// compares it with the one the caller echoed from its preview. It returns a
// refusal result on drift, (nil, nil) when the precondition holds, and an error
// only when the state cannot be resolved at all (e.g. a macOS App-Data denial,
// which surfaces with its remediation as usual).
func (s *Service) checkPrecondition(client *ClientDef, cfgPath, serverName, token string) (*ConnectResult, error) {
	fileExists, existing, _, err := s.preWriteState(client, cfgPath, serverName)
	if err != nil {
		return nil, err
	}
	current := s.preconditionToken(cfgPath, fileExists, existing,
		buildServerEntry(client.ID, s.entryParams(false)))
	// Constant-time: the token is a MAC, and a byte-at-a-time comparison would
	// leak enough to forge one.
	if hmac.Equal([]byte(current), []byte(token)) {
		return nil, nil
	}
	return &ConnectResult{
		Success:    false,
		Client:     client.ID,
		ConfigPath: cfgPath,
		ServerName: serverName,
		Action:     actionPreconditionFailed,
		Message: fmt.Sprintf(
			"%s or the entry MCPProxy would write changed since the preview was generated; nothing was written — re-run the preview and confirm the new change",
			cfgPath),
	}, nil
}

// canonicalJSON marshals a parsed entry deterministically. A nil map still
// yields a non-nil "null" payload so a present-but-unparsable entry is
// distinguishable from an absent one (which passes nil directly).
func canonicalJSON(v map[string]interface{}) json.RawMessage {
	encoded, err := json.Marshal(v)
	if err != nil {
		// Values here come from encoding/json or toml decoding, so this is
		// unreachable in practice; hash the error text rather than silently
		// hashing nothing (which would weaken drift detection).
		return json.RawMessage("marshal-error:" + err.Error())
	}
	return encoded
}
