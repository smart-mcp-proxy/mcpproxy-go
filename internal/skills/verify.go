package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// VerifyFile checks the bytes against the listing: size first, then sha256.
// Only sha256:<64 lower-case hex> digests are understood.
func VerifyFile(ref FileRef, content []byte) *UnusableError {
	if int64(len(content)) != ref.Size {
		return unusable(ReasonSizeMismatch, "%s: listed %d bytes, read %d", ref.URI, ref.Size, len(content))
	}
	if !sha256DigestRe.MatchString(ref.Digest) {
		return unusable(ReasonUnsupportedDigest, "%s: digest must be sha256:<64 lower-case hex>", ref.URI)
	}
	sum := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(sum[:]) != ref.Digest {
		return unusable(ReasonDigestMismatch, "%s", ref.URI)
	}
	return nil
}

// ParseFrontmatter reads the leading "---" YAML block of a SKILL.md. The block
// is bounded at MaxFrontmatterBytes and must be a mapping.
func ParseFrontmatter(skillMD []byte) (map[string]any, error) {
	b := bytes.TrimPrefix(skillMD, []byte("\xef\xbb\xbf"))
	first, rest, ok := bytes.Cut(b, []byte("\n"))
	if !ok || strings.TrimRight(string(first), " \t\r") != "---" {
		return nil, errors.New("SKILL.md does not start with a --- frontmatter block")
	}

	var block []byte
	closed := false
	for len(rest) > 0 {
		line, tail, _ := bytes.Cut(rest, []byte("\n"))
		if strings.TrimRight(string(line), " \t\r") == "---" {
			closed = true
			break
		}
		block = append(block, line...)
		block = append(block, '\n')
		if len(block) > MaxFrontmatterBytes {
			return nil, fmt.Errorf("frontmatter longer than %d bytes", MaxFrontmatterBytes)
		}
		rest = tail
	}
	if !closed {
		return nil, errors.New("frontmatter block is not closed")
	}

	var v any
	if err := yaml.Unmarshal(block, &v); err != nil {
		return nil, fmt.Errorf("frontmatter is not valid YAML: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("frontmatter must be a mapping with string keys")
	}
	if _, err := json.Marshal(m); err != nil {
		return nil, fmt.Errorf("frontmatter is not representable as JSON: %w", err)
	}
	return m, nil
}

// maxNumberExponent bounds the exponent of a number that is compared by value.
// A larger one is compared by its spelling instead: expanding 1e999999 into an
// exact value would cost memory for no benefit.
const maxNumberExponent = 400

// canonicalNumber returns one spelling per numeric value, so 1, 1.0 and 1e0
// are the same and two integers that differ only beyond float64 precision are
// not. ok is false for a value that is not a finite number.
func canonicalNumber(v any) (string, bool) {
	r := new(big.Rat)
	switch n := v.(type) {
	case json.Number:
		lit := string(n)
		if i := strings.IndexAny(lit, "eE"); i >= 0 {
			exp, err := strconv.Atoi(lit[i+1:])
			if err != nil || exp > maxNumberExponent || exp < -maxNumberExponent {
				return "lit:" + strings.ToLower(lit), true
			}
		}
		if _, ok := r.SetString(lit); !ok {
			return "", false
		}
	case int:
		r.SetInt64(int64(n))
	case int64:
		r.SetInt64(n)
	case uint64:
		r.SetInt(new(big.Int).SetUint64(n))
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", false
		}
		r.SetFloat64(n)
	default:
		return "", false
	}
	return r.RatString(), true
}

// appendCanonical writes an unambiguous encoding of a frontmatter value: every
// value starts with a type tag, strings and numbers carry their length, maps
// are written in key order. It is only ever hashed or compared, never parsed,
// so it does not need to be JSON; what it needs is that two values share an
// encoding exactly when they are the same value.
func appendCanonical(b []byte, v any) ([]byte, error) {
	str := func(b []byte, tag byte, s string) []byte {
		b = append(b, tag)
		b = binary.BigEndian.AppendUint64(b, uint64(len(s)))
		return append(b, s...)
	}
	switch t := v.(type) {
	case nil:
		return append(b, 'z'), nil
	case bool:
		if t {
			return append(b, 't'), nil
		}
		return append(b, 'f'), nil
	case string:
		return str(b, 's', t), nil
	case []any:
		b = append(b, 'a')
		b = binary.BigEndian.AppendUint64(b, uint64(len(t)))
		for _, el := range t {
			var err error
			if b, err = appendCanonical(b, el); err != nil {
				return nil, err
			}
		}
		return b, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b = append(b, 'm')
		b = binary.BigEndian.AppendUint64(b, uint64(len(keys)))
		for _, k := range keys {
			b = str(b, 'k', k)
			var err error
			if b, err = appendCanonical(b, t[k]); err != nil {
				return nil, err
			}
		}
		return b, nil
	}
	if n, ok := canonicalNumber(v); ok {
		return str(b, 'n', n), nil
	}
	return nil, fmt.Errorf("frontmatter value of type %T has no canonical form", v)
}

// canonicalFrontmatter is the canonical encoding of a frontmatter mapping; a
// nil mapping is the empty one.
func canonicalFrontmatter(m map[string]any) ([]byte, error) {
	if m == nil {
		m = map[string]any{}
	}
	return appendCanonical(nil, m)
}

// FrontmatterEqual compares the listed frontmatter with the one parsed from
// SKILL.md by value: YAML and JSON numbers compare by their exact numeric
// value, nested maps of either origin compare key by key. A value with no
// canonical form never compares equal.
func FrontmatterEqual(listed, parsed map[string]any) bool {
	a, err := canonicalFrontmatter(listed)
	if err != nil {
		return false
	}
	b, err := canonicalFrontmatter(parsed)
	if err != nil {
		return false
	}
	return bytes.Equal(a, b)
}
