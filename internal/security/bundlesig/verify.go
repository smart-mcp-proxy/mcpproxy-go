package bundlesig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeyID returns the fingerprint of an Ed25519 public key: the lowercase hex
// SHA-256 of its 32 raw bytes. It is a property of the key itself, never an
// operator-chosen label, which is what makes "manifest names one publisher,
// bytes signed by another" detectable.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// ParsePublicKey decodes a configured signer: standard padded base64 of
// exactly 32 bytes. Like the sidecar signature it must be strict and
// canonical, so a trust anchor cannot be spelled two ways.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("public key is not strict standard base64: %w", err)
	}
	if base64.StdEncoding.EncodeToString(raw) != s {
		return nil, errors.New("public key is not canonical base64")
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// Key is one trusted signer. Authority names the root it belongs to; this
// package stores it and hands it back but attaches no meaning to it.
type Key struct {
	ID        string
	Public    ed25519.PublicKey
	Authority string
}

// TrustSet is an immutable set of trusted keys indexed by key id.
type TrustSet struct {
	keys map[string]Key
}

// NewTrustSet validates and indexes keys. It refuses a wrong-length key, a key
// whose ID is not the fingerprint of its Public bytes, and a duplicate id even
// across authorities: a single key must not be able to satisfy whichever root
// happens to be active. It copies the key bytes, so later mutation of the
// caller's slices cannot change what verifies.
func NewTrustSet(keys ...Key) (*TrustSet, error) {
	ts := &TrustSet{keys: make(map[string]Key, len(keys))}
	for _, k := range keys {
		if len(k.Public) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trust key %q: public key is %d bytes, want %d", k.ID, len(k.Public), ed25519.PublicKeySize)
		}
		if want := KeyID(k.Public); k.ID != want {
			return nil, fmt.Errorf("trust key id %q is not the fingerprint of its public key (%s)", k.ID, want)
		}
		if prev, dup := ts.keys[k.ID]; dup {
			return nil, fmt.Errorf("trust key %s listed twice (authorities %q and %q)", k.ID, prev.Authority, k.Authority)
		}
		k.Public = append(ed25519.PublicKey(nil), k.Public...)
		ts.keys[k.ID] = k
	}
	return ts, nil
}

// Lookup returns the key with the given id. The returned Public is a copy.
func (t *TrustSet) Lookup(id string) (Key, bool) {
	k, ok := t.keys[id]
	if ok {
		k.Public = append(ed25519.PublicKey(nil), k.Public...)
	}
	return k, ok
}

// Verify checks sc against bundle: row 11 (the sidecar's key id must be known
// to the WHOLE set) then row 12 (Ed25519 over the exact raw bundle bytes). On
// success it returns the verifying key.
//
// Verify deliberately does not decide which authority is active. A genuine key
// of the non-active root must verify here so the caller can report
// authority_not_active rather than a misleading key_not_trusted.
func (t *TrustSet) Verify(bundle []byte, sc *Sidecar) (Key, error) {
	k, ok := t.Lookup(sc.KeyID)
	if !ok {
		return Key{}, reject(CodeKeyNotTrusted, "key %s is not in the trust set", sc.KeyID)
	}
	if !ed25519.Verify(k.Public, bundle, sc.Signature) {
		return Key{}, reject(CodeSignatureInvalid, "signature does not verify under key %s", sc.KeyID)
	}
	return k, nil
}

// CheckKeyIDAgreement enforces row 13: the manifest's key_id, the sidecar's
// key_id and the verifying key's id must all be identical. An empty manifest
// key id is a mismatch, never "unspecified".
func CheckKeyIDAgreement(manifestKeyID string, sc *Sidecar, verified Key) error {
	if manifestKeyID == "" || manifestKeyID != sc.KeyID || sc.KeyID != verified.ID {
		return reject(CodeKeyIDMismatch, "manifest %q, sidecar %q, verifying key %q", manifestKeyID, sc.KeyID, verified.ID)
	}
	return nil
}

// SignSidecar returns the canonical sidecar for bundle: one line, keys in the
// order sidecar_version, algorithm, key_id, signature, no trailing newline.
// It exists for publishers and tests; the loader never signs anything.
func SignSidecar(priv ed25519.PrivateKey, bundle []byte) []byte {
	pub := priv.Public().(ed25519.PublicKey)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, bundle))
	return []byte(`{"sidecar_version":"` + sidecarVersion + `","algorithm":"` + sidecarAlgorithm +
		`","key_id":"` + KeyID(pub) + `","signature":"` + sig + `"}`)
}
