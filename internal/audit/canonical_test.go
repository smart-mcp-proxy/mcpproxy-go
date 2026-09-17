package audit_test

// canonical_test.go — Phase D.1 / T096 (Spec 107 PR-D).
//
// Compile-red until T099: this file references audit.CanonicalizeArgs and
// audit.HashArgs, which do not exist yet (internal/audit/canonical.go is
// implemented in T099). Until then `go test ./internal/audit` fails to
// build — that is the expected state for this task.
//
// The vectors live under testdata/canonical/*.json (RFC 8785 / JCS
// appendix-style samples: nested maps/arrays, non-ASCII and escaped
// strings, number equivalence 1 == 1.0 == 1e0, -0 == 0, the 2^53+1
// precision-loss case, and UTF-16 code-unit key ordering including the
// surrogate-pair quirk). Per data-model.md §5 and research.md D5, the
// expected output is computed by an INDEPENDENT implementation in this
// test package (referenceCanonicalize / referenceFormatNumber below),
// never by calling the production code under test. `_auth_*` exclusion
// is asserted through the real security.StripInternalArgs (already
// implemented, not part of this package).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// ---------------------------------------------------------------------
// Vector file format
// ---------------------------------------------------------------------

// canonicalVectorFile is the on-disk shape of testdata/canonical/*.json.
// Every object in `variants` must canonicalize to an identical byte
// sequence (and therefore an identical SHA-256 digest) once decoded and
// run through JCS — that is the property every vector in this suite
// tests, whether the variants differ by member order, number spelling,
// or (for the auth_keys_stripped fixture) the presence of `_auth_*`
// members that security.StripInternalArgs must remove first.
type canonicalVectorFile struct {
	Description       string                   `json:"description"`
	StripInternalArgs bool                     `json:"strip_internal_args"`
	Variants          []map[string]interface{} `json:"variants"`
}

func loadVectors(t *testing.T) map[string]canonicalVectorFile {
	t.Helper()

	dir := filepath.Join("testdata", "canonical")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	out := make(map[string]canonicalVectorFile)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}

		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var vf canonicalVectorFile
		if err := dec.Decode(&vf); err != nil {
			t.Fatalf("decoding %s: %v", entry.Name(), err)
		}
		if len(vf.Variants) == 0 {
			t.Fatalf("%s: no variants", entry.Name())
		}
		out[entry.Name()] = vf
	}

	if len(out) == 0 {
		t.Fatalf("no vector files found under %s", dir)
	}
	return out
}

// ---------------------------------------------------------------------
// T096: every variant in a vector file canonicalizes identically, and
// the production implementation agrees byte-for-byte with the
// independent reference implementation below.
// ---------------------------------------------------------------------

func TestCanonicalizeArgs_MatchesIndependentReference(t *testing.T) {
	for name, vf := range loadVectors(t) {
		name, vf := name, vf
		t.Run(name, func(t *testing.T) {
			var first []byte
			var firstHash string

			for i, variant := range vf.Variants {
				input := variant
				if vf.StripInternalArgs {
					// The vector already exercises StripInternalArgs itself
					// (it is the production, already-shipped function); the
					// point under test is that audit.CanonicalizeArgs applies
					// it before serialising, not that this test reimplements
					// stripping.
					input = security.StripInternalArgs(variant)
				}

				want, err := referenceCanonicalize(input)
				if err != nil {
					t.Fatalf("variant %d: reference implementation failed: %v", i, err)
				}
				wantHash := sha256Hex(want)

				got, err := audit.CanonicalizeArgs(variant)
				if err != nil {
					t.Fatalf("variant %d: audit.CanonicalizeArgs failed: %v", i, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("variant %d: canonical mismatch\n production: %s\n reference:  %s", i, got, want)
				}

				gotHash, gotLen, err := audit.HashArgs(variant)
				if err != nil {
					t.Fatalf("variant %d: audit.HashArgs failed: %v", i, err)
				}
				if gotHash != wantHash {
					t.Fatalf("variant %d: hash mismatch: production=%s reference=%s", i, gotHash, wantHash)
				}
				if gotLen != len(want) {
					t.Fatalf("variant %d: args_bytes=%d, want len(canonical)=%d", i, gotLen, len(want))
				}

				if i == 0 {
					first, firstHash = want, wantHash
					continue
				}
				if !bytes.Equal(want, first) {
					t.Fatalf("variant %d canonicalizes differently from variant 0, but the vector file declares them equivalent:\n0: %s\n%d: %s", i, first, i, want)
				}
				if gotHash != firstHash {
					t.Fatalf("variant %d: args_sha256 differs from variant 0's, but the vector file declares them equivalent", i)
				}
			}
		})
	}
}

// TestCanonicalizeArgs_StripsInternalArgs is the T096-scoped structural
// assertion for FR-015's "never raw args" invariant at the canonical
// layer: the canonical bytes (and therefore the hash) must never contain
// an `_auth_` key, regardless of where in the input map it appears.
func TestCanonicalizeArgs_StripsInternalArgs(t *testing.T) {
	args := map[string]interface{}{
		"repo":             "mcpproxy-go",
		"issue":            json.Number("1107"),
		"_auth_user_id":    "u-1",
		"_auth_user_email": "alice@example.com",
		"_auth_auth_type":  "session_user",
	}

	got, err := audit.CanonicalizeArgs(args)
	if err != nil {
		t.Fatalf("CanonicalizeArgs: %v", err)
	}
	if bytes.Contains(got, []byte("_auth_")) {
		t.Fatalf("canonical output leaked an _auth_ key: %s", got)
	}

	stripped := security.StripInternalArgs(args)
	want, err := referenceCanonicalize(stripped)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical output does not match StripInternalArgs(args) canonicalized independently:\n got:  %s\n want: %s", got, want)
	}
}

// TestCanonicalizeArgs_DeterministicNoWhitespace pins the wire shape RFC
// 8785 requires: no insignificant whitespace and object members separated
// by a bare comma/colon (JCS §3.2).
func TestCanonicalizeArgs_DeterministicNoWhitespace(t *testing.T) {
	args := map[string]interface{}{"b": json.Number("2"), "a": json.Number("1")}
	got, err := audit.CanonicalizeArgs(args)
	if err != nil {
		t.Fatalf("CanonicalizeArgs: %v", err)
	}
	if want := []byte(`{"a":1,"b":2}`); !bytes.Equal(got, want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// TestHashArgs_EmptyArgs pins the boundary case: no arguments still
// produces a valid (non-empty-string) SHA-256 over the empty JCS object.
func TestHashArgs_EmptyArgs(t *testing.T) {
	hash, n, err := audit.HashArgs(map[string]interface{}{})
	if err != nil {
		t.Fatalf("HashArgs: %v", err)
	}
	wantHash := sha256Hex([]byte("{}"))
	if hash != wantHash {
		t.Fatalf("hash = %s, want %s (sha256 of \"{}\")", hash, wantHash)
	}
	if n != len("{}") {
		t.Fatalf("args_bytes = %d, want %d", n, len("{}"))
	}
}

// ---------------------------------------------------------------------
// Independent reference implementation (test package only — never
// shares code with internal/audit/canonical.go).
// ---------------------------------------------------------------------

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// referenceCanonicalize serialises v (as decoded by encoding/json with
// UseNumber) per RFC 8785: object members sorted by UTF-16 code units of
// the key, arrays left in input order, ES6-style number formatting, and
// minimal string escaping.
func referenceCanonicalize(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := referenceEncode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func referenceEncode(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return fmt.Errorf("number %q: %w", val, err)
		}
		buf.WriteString(referenceFormatNumber(f))
	case float64:
		buf.WriteString(referenceFormatNumber(val))
	case string:
		referenceEncodeString(buf, val)
	case []interface{}:
		buf.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := referenceEncode(buf, elem); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return utf16Less(keys[i], keys[j])
		})
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			referenceEncodeString(buf, k)
			buf.WriteByte(':')
			if err := referenceEncode(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("referenceEncode: unsupported type %T", v)
	}
	return nil
}

// utf16Less compares a and b by their UTF-16 code units (RFC 8785 §3.2.3),
// which is NOT the same as comparing Unicode code points once either
// string contains a character above the Basic Multilingual Plane: a
// supplementary-plane character encodes as a surrogate pair whose first
// unit (0xD800-0xDBFF) is numerically below the 0xE000-0xFFFF BMP range,
// so e.g. U+1F600 sorts before U+E000 under this rule despite being the
// larger code point.
func utf16Less(a, b string) bool {
	au := utf16.Encode([]rune(a))
	bu := utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}

// referenceEncodeString applies RFC 8785 §3.2.2.2 minimal escaping: only
// U+0022, U+005C and the C0 control range U+0000-U+001F are escaped
// (using the JSON short forms where defined, else \u00XX lowercase hex);
// everything else, U+002F and non-ASCII included, is emitted verbatim.
func referenceEncodeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// referenceFormatNumber implements ES6 Number::toString for a float64 as
// RFC 8785 requires: shortest round-tripping decimal digits, "0" for
// +/-0, and an exponent written without the zero-padding Go's strconv
// applies (Go: "1e+05"; ES6/JCS: "1e+5").
func referenceFormatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		panic(fmt.Sprintf("referenceFormatNumber: non-finite value %v (cannot occur from encoding/json input)", f))
	}
	if f == 0 {
		return "0"
	}

	s := strconv.FormatFloat(f, 'g', -1, 64)
	mantissa, exp, hasExp := strings.Cut(s, "e")
	if !hasExp {
		return mantissa
	}

	sign := "+"
	digits := exp
	switch digits[0] {
	case '+', '-':
		sign = string(digits[0])
		digits = digits[1:]
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	return mantissa + "e" + sign + digits
}

// TestReferenceFormatNumber_KnownValues pins the reference helper itself
// against values whose ES6 spelling is unambiguous, so a bug here can't
// silently rubber-stamp a matching bug in the production implementation.
func TestReferenceFormatNumber_KnownValues(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{4.5, "4.5"},
		{0.002, "0.002"},
		{100, "100"},
	}
	for _, tc := range cases {
		if got := referenceFormatNumber(tc.in); got != tc.want {
			t.Errorf("referenceFormatNumber(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
