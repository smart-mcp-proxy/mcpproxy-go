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
	"regexp"
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

	var doc yaml.Node
	if err := yaml.Unmarshal(block, &doc); err != nil {
		return nil, fmt.Errorf("frontmatter is not valid YAML: %w", err)
	}
	budget := maxFrontmatterNodes
	v, err := yamlValue(&doc, &budget)
	if err != nil {
		return nil, fmt.Errorf("frontmatter is not usable: %w", err)
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

// maxFrontmatterNodes bounds how many YAML nodes one frontmatter may amount to
// once aliases are followed, so a block of nested aliases cannot expand without
// end. Real frontmatter has a few dozen.
const maxFrontmatterNodes = 10000

// plainDecimal matches a number written in plain decimal notation, the only
// notation JSON has. YAML's other forms (hex, octal, underscores, .inf) are
// left to the YAML library.
var plainDecimal = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)

// yamlValue turns a YAML node into the value a JSON listing would hold for it.
// The block is read node by node, and not through the library's own decoding
// into Go values, for one reason: a decimal number keeps its digits. Decoded
// to a float64 it would lose them, and a number written the same way in the
// listing and in SKILL.md could then compare different, or two different ones
// equal.
func yamlValue(n *yaml.Node, budget *int) (any, error) {
	if *budget--; *budget < 0 {
		return nil, errors.New("too many values (aliases that repeat?)")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return yamlValue(n.Content[0], budget)
	case yaml.AliasNode:
		return yamlValue(n.Alias, budget)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind == yaml.AliasNode {
				k = k.Alias
			}
			if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
				return nil, errors.New("merge keys (<<) are not supported")
			}
			if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
				return nil, errors.New("a mapping key is not a string")
			}
			if _, dup := m[k.Value]; dup {
				return nil, fmt.Errorf("key %q appears twice", k.Value)
			}
			v, err := yamlValue(n.Content[i+1], budget)
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := yamlValue(c, budget)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int", "!!float":
			if plainDecimal.MatchString(n.Value) {
				if c, ok := canonicalDecimal(n.Value); ok {
					return json.Number(c), nil
				}
			}
			var v any
			if err := n.Decode(&v); err != nil {
				return nil, err
			}
			return v, nil
		}
		return n.Value, nil // strings, and anything else by its text
	}
	return nil, errors.New("unsupported YAML node")
}

// canonicalDecimal gives a plain decimal literal the one spelling its value
// has: an optional minus, the significant digits with no zero at either end,
// and a power of ten when it is not zero ("-125e-2", "1e2", "0"). It is exact
// for any number of digits and never expands an exponent. The result is also a
// valid JSON number. ok is false for text that is not a plain decimal, or whose
// exponent does not fit an int64.
func canonicalDecimal(lit string) (string, bool) {
	neg := false
	switch {
	case strings.HasPrefix(lit, "-"):
		neg, lit = true, lit[1:]
	case strings.HasPrefix(lit, "+"):
		lit = lit[1:]
	}
	var exp int64
	if i := strings.IndexAny(lit, "eE"); i >= 0 {
		e, err := strconv.ParseInt(lit[i+1:], 10, 64)
		if err != nil {
			return "", false
		}
		exp, lit = e, lit[:i]
	}
	intPart, frac, _ := strings.Cut(lit, ".")
	if intPart == "" && frac == "" {
		return "", false
	}
	digits := intPart + frac
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	// A literal is at most a few KiB (the frontmatter bound), so these length
	// adjustments cannot overflow unless the exponent itself is at the edge.
	if exp < math.MinInt64+int64(len(frac)) {
		return "", false
	}
	exp -= int64(len(frac))
	digits = strings.TrimLeft(digits, "0")
	trimmed := strings.TrimRight(digits, "0")
	if trimmed == "" {
		return "0", true
	}
	if exp > math.MaxInt64-int64(len(digits)-len(trimmed)) {
		return "", false
	}
	exp += int64(len(digits) - len(trimmed))
	out := trimmed
	if neg {
		out = "-" + out
	}
	if exp != 0 {
		out += "e" + strconv.FormatInt(exp, 10)
	}
	return out, true
}

// canonicalNumber returns one spelling per numeric value, exactly: 1, 1.0 and
// 1e0 are the same, and two numbers that differ in any digit are not. ok is
// false for a value that is not a finite number.
func canonicalNumber(v any) (string, bool) {
	switch n := v.(type) {
	case json.Number:
		return canonicalDecimal(string(n))
	case int:
		return canonicalDecimal(strconv.FormatInt(int64(n), 10))
	case int64:
		return canonicalDecimal(strconv.FormatInt(n, 10))
	case uint64:
		return canonicalDecimal(strconv.FormatUint(n, 10))
	case float64:
		// Reached only for YAML's non-decimal forms; the shortest text that
		// reads back as this float is its value as far as anyone wrote it.
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", false
		}
		return canonicalDecimal(strconv.FormatFloat(n, 'e', -1, 64))
	}
	return "", false
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
