package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CanonicalJSON re-encodes raw JSON bytes into the canonical form used
// everywhere in the bench (research D7b): object keys sorted lexicographically
// at every depth, array order preserved, number literals preserved verbatim
// (via json.Number — no float round-trip), compact (no insignificant
// whitespace), and no HTML escaping. Identical JSON content in any key order
// canonicalizes to identical bytes (FR-010).
//
// It lives in the bench package (not bench/arms) because canonicalization
// happens at every schema INGESTION boundary — LoadCorpusV2, the live
// /api/v1/tools fetch, and the corpusio loaders — so the plain
// Tokenizer.CountToolWithSchema and the arm renderers always count the same
// canonical bytes (contract parity). bench/arms re-exports it for the arm
// encoders (arms imports bench, never the reverse).
func CanonicalJSON(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	// Reject trailing bytes after the first value — a silently half-parsed
	// schema would be a silently truncated encoding (contract rule 2).
	if err := RequireEOF(dec); err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	// Encoder appends a newline; the canonical form is the bare value.
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// RequireEOF verifies dec has no data left after the first decoded top-level
// value. dec.More() is NOT a valid trailing-data check at the top level: it
// merely peeks for a byte other than ']' or '}', so streams like `{}}` or
// `{}]` slip through (and `{} {}` is a second value, not "more of" the
// first). Draining one more token and requiring io.EOF rejects them all.
func RequireEOF(dec *json.Decoder) error {
	tok, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("trailing data after value: %w", err)
	}
	return fmt.Errorf("trailing data after value: unexpected %v", tok)
}
