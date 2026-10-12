package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 117 PR-2 (#1566): the review fingerprint.
//
// A review reports, per tool, an opaque fingerprint of the definition it
// shows (wire name current_hash, A1). A client sends the fingerprints back in
// expected_hashes, and the approval is refused as out of date when the stored
// definition no longer matches, so an operator can only approve what they
// saw. The fingerprint is derived from the stored approval record and never
// stored itself.
//
// It covers the server name (a binding for server A never matches server B),
// the raw tool name, the storage CurrentHash, the description, the input
// schema, the output schema and the four safety hints by their RAW value
// (unset, false, true), so a hint change is always visible to a review even
// where the rug-pull hash treats an unset hint and its spelled-out default
// alike. Each field is encoded on its own (length-prefixed, Sol r2 P1): the
// rug-pull hash joins description and schemas with an unescaped '|' and
// leaves out an absent output schema, so (D, I, O) and ("D|I", O, none) share
// one CurrentHash, but never one fingerprint. Schemas are JSON-normalized, as
// the rug-pull hash does, so a key-order-only difference is not a change.
// title, status, disabled, timestamps and scan evidence are not part of it.
// The rug-pull hash (calculateToolApprovalHash*) is unchanged.

// reviewFingerprintVersion prefixes every fingerprint. A fingerprint of
// another version never matches, which only makes the client reload. rv1
// (PR-2 before Sol r2) hashed CurrentHash in place of the definition fields.
const reviewFingerprintVersion = "rv2"

// reviewFingerprint returns the review fingerprint of rec: "rv2-" followed by
// 64 hex characters. It is non-empty for every record, including one with an
// empty CurrentHash (a toggle-synthesized record), whose stored definition
// still determines it (FR-003).
func reviewFingerprint(rec *storage.ToolApprovalRecord) string {
	if rec == nil {
		return ""
	}
	h := sha256.New()
	for _, part := range []string{
		reviewFingerprintVersion,
		rec.ServerName,
		rec.ToolName,
		rec.CurrentHash,
		rec.CurrentDescription,
		normalizeJSON(rec.CurrentSchema),
		normalizeJSON(rec.CurrentOutputSchema),
		reviewHintString(rec.CurrentAnnotations),
	} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		h.Write(n[:])
		h.Write([]byte(part))
	}
	return reviewFingerprintVersion + "-" + hex.EncodeToString(h.Sum(nil))
}

// reviewHintString renders the four safety hints as
// "ro=<v>;de=<v>;id=<v>;ow=<v>" with v "-" (unset), "0" or "1".
func reviewHintString(a *config.ToolAnnotations) string {
	v := func(b *bool) string {
		switch {
		case b == nil:
			return "-"
		case *b:
			return "1"
		default:
			return "0"
		}
	}
	if a == nil {
		return "ro=-;de=-;id=-;ow=-"
	}
	return strings.Join([]string{
		"ro=" + v(a.ReadOnlyHint),
		"de=" + v(a.DestructiveHint),
		"id=" + v(a.IdempotentHint),
		"ow=" + v(a.OpenWorldHint),
	}, ";")
}
