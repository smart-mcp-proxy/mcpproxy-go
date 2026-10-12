package runtime

import (
	"crypto/sha256"
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
// the raw tool name, the approval-hashed contract (CurrentHash: description,
// input schema, output schema, effective safety hints) and the four safety
// hints by their RAW value (unset, false, true), so a hint change is always
// visible to a review even where the rug-pull hash treats an unset hint and
// its spelled-out default alike. title, status, disabled, timestamps and
// scan evidence are not part of it. The rug-pull hash
// (calculateToolApprovalHash*) is unchanged.

// reviewFingerprintVersion prefixes every fingerprint. A fingerprint of
// another version never matches, which only makes the client reload.
const reviewFingerprintVersion = "rv1"

// reviewFingerprint returns the review fingerprint of rec: "rv1-" followed by
// 64 hex characters. It is non-empty for every record, including one with an
// empty CurrentHash (a toggle-synthesized record), whose stored definition
// then stands in for the hash (FR-003).
func reviewFingerprint(rec *storage.ToolApprovalRecord) string {
	if rec == nil {
		return ""
	}
	base := rec.CurrentHash
	if base == "" {
		sum := sha256.Sum256([]byte(rec.CurrentDescription + "\x00" + rec.CurrentSchema + "\x00" + rec.CurrentOutputSchema))
		base = "nohash:" + hex.EncodeToString(sum[:])
	}
	h := sha256.New()
	for i, part := range []string{reviewFingerprintVersion, rec.ServerName, rec.ToolName, base, reviewHintString(rec.CurrentAnnotations)} {
		if i > 0 {
			h.Write([]byte{0})
		}
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
