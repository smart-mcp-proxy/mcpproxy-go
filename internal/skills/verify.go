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

// canonicalNumber returns one spelling per numeric value. ok is false for a
// value that is not a finite number.
//
// A whole number that fits 64 bits is exact, so two integers that differ only
// beyond float64 precision stay different. Everything else is compared as a
// float64, on both sides: YAML hands decimals over as float64 already, and
// reading the listing's decimals the same way makes 0.1 equal 0.1, 1 equal
// 1.0 and 1e2 equal 100. A number too large for a float64 has no canonical
// form and so equals nothing.
func canonicalNumber(v any) (string, bool) {
	switch n := v.(type) {
	case json.Number:
		lit := string(n)
		if !strings.ContainsAny(lit, ".eE") {
			if i, err := strconv.ParseInt(lit, 10, 64); err == nil {
				return strconv.FormatInt(i, 10), true
			}
			if u, err := strconv.ParseUint(lit, 10, 64); err == nil {
				return strconv.FormatUint(u, 10), true
			}
		}
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return "", false
		}
		return canonicalFloat(f)
	case int:
		return strconv.FormatInt(int64(n), 10), true
	case int64:
		return strconv.FormatInt(n, 10), true
	case uint64:
		return strconv.FormatUint(n, 10), true
	case float64:
		return canonicalFloat(n)
	}
	return "", false
}

// canonicalFloat writes a whole float the way the same integer is written, so
// 100.0 and 100 share a form, and any other float in its shortest form that
// reads back as the same float64.
func canonicalFloat(f float64) (string, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", false
	}
	if f == math.Trunc(f) {
		switch {
		case f >= -(1<<63) && f < 1<<63:
			return strconv.FormatInt(int64(f), 10), true // also turns -0 into 0
		case f >= 1<<63 && f < 1<<64:
			return strconv.FormatUint(uint64(f), 10), true
		}
	}
	return strconv.FormatFloat(f, 'g', -1, 64), true
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
