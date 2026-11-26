package audit

// admin_fields.go — Spec 133 FR-015/FR-015b: the change-entry classifier and
// the sealed literal value types.
//
// A change entry's value can reach a line only through Literal, whose value
// parameters are the sealed LiteralValue interface below: every
// implementation is a package-owned type with a validating render, and the
// field ↔ type pairing is checked against one table keyed by field path.
// There is no constructor that takes a string or an `any` value and no
// hashing path, so a free-text config value or a secret has no way onto a
// line (contracts/admin-action-line.md §Changes). An unknown path is
// withheld as a value; a path under a secret-class prefix is withheld as a
// secret.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// FieldPath is a structured JSON Pointer path (RFC 6901), kept as segments so
// classification, diffing and sorting use the original path and escaping
// happens only at render time.
type FieldPath struct {
	segs []string
}

// Path builds a field path from its segments.
func Path(segs ...string) FieldPath { return FieldPath{segs: append([]string(nil), segs...)} }

// ParsePointer parses a rendered JSON Pointer ("/env/a~1b") into a path.
func ParsePointer(p string) FieldPath {
	if p == "" || p == "/" {
		return FieldPath{}
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return FieldPath{segs: parts}
}

// sortKey orders entries by the original structured path.
func (f FieldPath) sortKey() string { return strings.Join(f.segs, "\x00") }

// Fixed field paths the P1 lines use.
var (
	FieldTokenPrefix        = Path("token_prefix")
	FieldPendingTokenPrefix = Path("pending_token_prefix")
	FieldExpiresAt          = Path("expires_at")
	FieldLogins             = Path("logins")
	FieldEnabled            = Path("enabled")
)

// maxPointerSegment is the per-segment cap applied before escaping, and
// maxPointerLength the cap on the rendered pointer (contract §Size).
const (
	maxPointerSegment = 64
	maxPointerLength  = 256
)

// render returns the escaped pointer and whether a segment was capped or the
// entry fell back to its fixed parent.
func (f FieldPath) render() (string, bool) {
	masked := false
	var b strings.Builder
	for _, s := range f.segs {
		r := []rune(s)
		if len(r) > maxPointerSegment {
			r = r[:maxPointerSegment]
			masked = true
		}
		seg := string(r)
		if m, hit := applyMasking(seg); hit {
			noteFieldMask()
			seg = m
			masked = true
		}
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(seg, "~", "~0"), "/", "~1"))
	}
	out := b.String()
	if len(out) > maxPointerLength && len(f.segs) > 0 {
		return "/" + escapeSeg(f.segs[0]), true
	}
	return out, masked
}

func escapeSeg(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// Field classes (ClassifyField results).
const (
	classLiteral        = "literal"
	classWithheldSecret = "secret"
	classWithheldValue  = "value"
)

// literalKind names the one LiteralValue type a literal path accepts.
type literalKind int

const (
	kindBool literalKind = iota + 1
	kindTokenPrefix
	kindTimestamp
	kindCount
)

// literalFields is the literal list (contract §Changes): a path named here is
// rendered verbatim only when the value has the paired type. Later slices
// extend it (PR-2/PR-3: enums, bounded integers, name lists).
var literalFields = map[string]literalKind{
	"token_prefix":         kindTokenPrefix,
	"pending_token_prefix": kindTokenPrefix,
	"expires_at":           kindTimestamp,
	"logins":               kindCount,
	"enabled":              kindBool,
}

// secretPrefixes are the secret-class roots: anything at or below them is
// withheld as a secret.
var secretPrefixes = map[string]bool{
	"env":                        true,
	"headers":                    true,
	"api_key":                    true,
	"oauth":                      true,
	"oauth_token_encryption_key": true,
	"secrets":                    true,
	"secret":                     true,
	"keyring":                    true,
}

// ClassifyField returns "literal", "secret" or "value" for a path. Exported
// for the classifier table test.
func ClassifyField(f FieldPath) string {
	if len(f.segs) == 0 {
		return classWithheldValue
	}
	if secretPrefixes[f.segs[0]] {
		return classWithheldSecret
	}
	if len(f.segs) == 1 {
		if _, ok := literalFields[f.segs[0]]; ok {
			return classLiteral
		}
	}
	return classWithheldValue
}

func literalKindOf(f FieldPath) (literalKind, bool) {
	if len(f.segs) != 1 || secretPrefixes[f.segs[0]] {
		return 0, false
	}
	k, ok := literalFields[f.segs[0]]
	return k, ok
}

// LiteralValue is the sealed set of value types a literal change may carry.
// Implementations live in this package only.
type LiteralValue interface {
	literalKind() literalKind
	// render returns the wire value, or ok=false when the value is outside
	// its closed set (the entry is then withheld).
	render() (interface{}, bool)
}

// Bool is a boolean literal.
type Bool bool

func (Bool) literalKind() literalKind      { return kindBool }
func (b Bool) render() (interface{}, bool) { return bool(b), true }

// tokenPrefixPattern is the display-prefix shape (8–16 characters, the
// schema's caller.token_prefix bounds) of every proxy token kind.
var tokenPrefixPattern = regexp.MustCompile(`^mcp_(agt|cli|oat|ort)_[A-Za-z0-9_-]{0,8}$`)

// TokenPrefix is a token display prefix, already shown in every token list.
type TokenPrefix string

func (TokenPrefix) literalKind() literalKind { return kindTokenPrefix }
func (p TokenPrefix) render() (interface{}, bool) {
	s := string(p)
	if len(s) < 8 || len(s) > 16 || !tokenPrefixPattern.MatchString(s) {
		return nil, false
	}
	return s, true
}

// Timestamp is a commit-relevant time (expires_at), rendered in the line's
// fixed layout. The zero time is outside the set (withheld).
type Timestamp time.Time

func (Timestamp) literalKind() literalKind { return kindTimestamp }
func (t Timestamp) render() (interface{}, bool) {
	tt := time.Time(t)
	if tt.IsZero() {
		return nil, false
	}
	return tt.UTC().Format(tsLayout), true
}

// Count is a non-negative count (logins removed by client.forget).
type Count int

func (Count) literalKind() literalKind { return kindCount }
func (c Count) render() (interface{}, bool) {
	if c < 0 {
		return nil, false
	}
	return int(c), true
}

// Withheld kinds.
type WithheldKind string

const (
	WithheldSecret WithheldKind = "secret"
	WithheldValue  WithheldKind = "value"
)

// Change is one change entry. It can be built only by Literal or Withheld.
type Change struct {
	path     FieldPath
	before   LiteralValue
	after    LiteralValue
	withheld WithheldKind
	set      bool
}

// Literal records field going from before to after; a nil side does not
// exist. When the classifier does not name field as literal, a value type
// does not match the field, or a value is outside its closed set, the entry
// is withheld (as a secret when the path is secret-class) — never rendered.
func Literal(field FieldPath, before, after LiteralValue) Change {
	c := Change{path: field, set: true}
	kind, ok := literalKindOf(field)
	if !ok {
		c.withheld = WithheldKind(ClassifyField(field))
		return c
	}
	for _, v := range []LiteralValue{before, after} {
		if v == nil {
			continue
		}
		if v.literalKind() != kind {
			c.withheld = WithheldValue
			return c
		}
		if _, ok := v.render(); !ok {
			c.withheld = WithheldValue
			return c
		}
	}
	c.before, c.after = before, after
	return c
}

// Withheld records that field changed without its value. A secret-class path
// is always withheld as a secret, whatever kind is passed.
func Withheld(field FieldPath, kind WithheldKind) Change {
	if ClassifyField(field) == classWithheldSecret {
		kind = WithheldSecret
	}
	if kind != WithheldSecret {
		kind = WithheldValue
	}
	return Change{path: field, withheld: kind, set: true}
}

func (c Change) entry() (map[string]interface{}, error) {
	if !c.set {
		return nil, fmt.Errorf("audit: zero Change")
	}
	field, masked := c.path.render()
	e := map[string]interface{}{"field": field}
	if masked {
		e["field_masked"] = true
	}
	if c.withheld != "" {
		e["withheld"] = string(c.withheld)
		return e, nil
	}
	if c.before == nil && c.after == nil {
		return nil, fmt.Errorf("audit: literal change %s has neither side", field)
	}
	if c.before != nil {
		v, _ := c.before.render()
		e["before"] = v
	}
	if c.after != nil {
		v, _ := c.after.render()
		e["after"] = v
	}
	return e, nil
}

// sortChanges orders entries by their original structured path; entries
// whose paths are equal keep their input order (stable).
func sortChanges(cs []Change) []Change {
	out := append([]Change(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].path.sortKey() < out[j].path.sortKey() })
	return out
}
