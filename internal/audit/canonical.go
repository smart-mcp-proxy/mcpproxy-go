package audit

// canonical.go: stdlib-only RFC 8785 (JCS) canonicalisation for tool-call
// arguments (research.md D5). Independent of, and never shared with, the
// reference implementation in canonical_test.go.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// CanonicalizeArgs strips `_auth_*` keys (FR-015) and serialises the rest
// per RFC 8785: keys sorted by UTF-16 code unit, arrays in input order, ES6
// number formatting, minimal string escaping, no insignificant whitespace.
func CanonicalizeArgs(args map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, security.StripInternalArgs(args)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// HashArgs returns the lowercase-hex SHA-256 of CanonicalizeArgs(args) and
// its byte length (schema `args_sha256` / `args_bytes`).
func HashArgs(args map[string]interface{}) (hash string, argsBytes int, err error) {
	canonical, err := CanonicalizeArgs(args)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), len(canonical), nil
}

// toFloat64 normalises the two numeric Go representations a decoded args
// map can hold — json.Number (encoding/json with UseNumber) or float64
// (without) — to the float64 RFC 8785 numbers are defined over.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func encodeCanonical(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		encodeCanonicalString(buf, val)
	case []interface{}:
		buf.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeCanonical(buf, elem); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16CodeUnitLess(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encodeCanonicalString(buf, k)
			buf.WriteByte(':')
			if err := encodeCanonical(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		f, ok := toFloat64(v)
		if !ok {
			return fmt.Errorf("audit.CanonicalizeArgs: unsupported type %T", v)
		}
		// RFC 8785 numbers are defined only over finite values (ES6
		// Number::toString has no representation for NaN/Infinity as a JSON
		// number token); a non-finite value reaching here (e.g. a
		// code_execution script computing 0/0 or 1/0 before the call is
		// dispatched) MUST be refused, never silently coerced to "null" —
		// that would make args_sha256 collide across distinguishable inputs
		// (round-1 cross-review finding, PR-D).
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("audit.CanonicalizeArgs: non-finite number cannot be canonicalised")
		}
		buf.WriteString(formatNumberJCS(f))
	}
	return nil
}

// utf16CodeUnitLess compares by UTF-16 code unit (RFC 8785 §3.2.3), which
// diverges from code-point order for supplementary-plane characters.
func utf16CodeUnitLess(a, b string) bool {
	au, bu := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}

// namedEscapes are the RFC 8785 §3.2.2.2 short escapes; every other C0
// control uses \u00XX and everything else (U+002F, non-ASCII) is verbatim.
var namedEscapes = map[rune]string{
	'"': `\"`, '\\': `\\`, '\b': `\b`, '\f': `\f`, '\n': `\n`, '\r': `\r`, '\t': `\t`,
}

func encodeCanonicalString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch {
		case namedEscapes[r] != "":
			buf.WriteString(namedEscapes[r])
		case r < 0x20:
			fmt.Fprintf(buf, `\u%04x`, r)
		default:
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
}

// formatNumberJCS is ES6 Number::toString per RFC 8785 (ECMA-262
// Number::toString, "Number Prototype Object" toString algorithm):
// shortest round-tripping decimal digits, "0" for +/-0, fixed-point
// notation while the decimal-point position n satisfies -6 < n <= 21, and
// exponential notation (mantissa "e" sign exponent, unpadded — JCS wants
// "1e+5", not Go's "1e+05") outside that range. Go's `%g` verb switches to
// exponential far earlier than ES6 (e.g. 0.000001 -> "1e-06" instead of
// "0.000001", and 1e20 -> "1e+20" instead of the 21-digit fixed form), so
// this cannot be strconv.FormatFloat(f, 'g', ...) reformatted — it derives
// the shortest round-tripping digit string via the 'e' verb and then
// applies the ECMA-262 placement rule directly. NaN/Inf cannot occur from
// a decoded args map (canonicalizeArgs refuses them before this is called).
func formatNumberJCS(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0"
	}

	neg := f < 0
	if neg {
		f = -f
	}

	// strconv's 'e' verb with precision -1 gives the shortest decimal that
	// round-trips to f, as "d.ddd...e±XX" (or "de±XX" for a single digit).
	mantissa, expPart, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, err := strconv.Atoi(expPart)
	if err != nil {
		// Unreachable: strconv always emits a well-formed exponent for a
		// finite, non-zero float in 'e' format.
		panic(fmt.Sprintf("audit: malformed exponent from strconv: %q", expPart))
	}
	k := len(digits)
	n := exp + 1 // ECMA-262: digits * 10^(n-k) == f, k <= n derived from exp.

	var out string
	switch {
	case k <= n && n <= 21:
		// Integer-valued magnitude: digits followed by (n-k) trailing zeros.
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		// Decimal point falls within the digit string.
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		// Leading "0." plus -n zeros before the digits.
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		// Exponential notation, unpadded exponent (JCS: "1e+5" not "1e+05").
		m := digits
		if k > 1 {
			m = digits[:1] + "." + digits[1:]
		}
		e := n - 1
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		out = m + "e" + sign + strconv.Itoa(e)
	}
	if neg {
		out = "-" + out
	}
	return out
}
