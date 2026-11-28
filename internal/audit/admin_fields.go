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
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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

// opaqueRoots are blocks that preserve arbitrary keys (the personal build
// keeps server_edition as raw JSON): nothing below them is descended into,
// a change is reported at the fixed parent (contract §Changes).
var opaqueRoots = map[string]bool{"server_edition": true}

// nestedRoots are the only roots below which a path may continue (contract
// §Changes, FR-015b): map keys under env/headers, server and profile names,
// and the fixed oauth sub-schema. Anywhere else a deeper path is collapsed
// to its fixed root, so a value smuggled in as a path segment never
// serializes (a positional argument under /args, say).
var nestedRoots = map[string]bool{"env": true, "headers": true, "oauth": true, "mcpServers": true, "profiles": true, "rate_limit": true}

// maxDepth bounds how far a path may go below a root whose children are map
// keys or fixed fields: one key under env/headers, one fixed field under
// oauth. A deeper path collapses to that depth.
var maxDepth = map[string]int{"env": 2, "headers": 2, "oauth": 2, "rate_limit": 2}

// knownFieldRoots are fixed config field names a change may name besides the
// literal list, the secret-class prefixes and the nested and opaque roots.
var knownFieldRoots = map[string]bool{
	// agent-token record fields (token.create)
	"allowed_servers": true, "permissions": true, "profile_pin": true, "purpose": true, "rate_limit": true, "guard_bound": true,
	// upstream server fields (PR-1b and PR-3 extend this list)
	"url": true, "command": true, "args": true, "working_dir": true, "protocol": true, "quarantined": true,
}

func knownRoot(seg string) bool {
	// A literal key spanning segments ("rate_limit/per_minute") is a path,
	// never one segment: a segment holding '/' is not a fixed root.
	_, literal := literalFields[seg]
	literal = literal && !strings.Contains(seg, "/")
	return literal || secretPrefixes[seg] || nestedRoots[seg] || opaqueRoots[seg] || knownFieldRoots[seg]
}

// profileFields are the fixed fields of a profile entry.
var profileFields = map[string]bool{
	"servers": true, "mode": true, "tier": true, "management_tools": true, "description": true,
	"switchable_to": true, "tools": true, "labels": true, "name": true,
}

// fixedChildren are the only child names a fixed sub-schema root may have;
// any other child collapses the path to the root (FR-015b: paths come from a
// fixed schema, never from a value). Mirrors config.OAuthConfig.
var fixedChildren = map[string]map[string]bool{
	"rate_limit": {"per_minute": true, "burst": true},
	"oauth": {
		"client_id": true, "client_secret": true, "redirect_uri": true, "callback_base_url": true,
		"scopes": true, "pkce_enabled": true, "refresh_replay_enabled": true, "extra_params": true,
		"authorization_endpoint": true, "token_endpoint": true, "registration_endpoint": true,
		"auth_server_metadata_url": true,
	},
}

// render returns the escaped pointer and whether it was shortened, masked or
// collapsed to a parent (field_masked). Order per segment (contract §Size):
// private-key check on the original, mask, then the 64-character cap, then
// escaping. A private-key segment, or any segment below an opaque root,
// collapses the path to its fixed parent.
func (f FieldPath) render() (string, bool) {
	segs := f.segs
	masked := false
	if len(segs) == 0 {
		return "", false
	}
	// FR-015b: the root segment comes from the fixed schema. An unknown root
	// could be arbitrary text, so it is never spelled: the entry is reported
	// at the document root (the RFC 6901 root pointer "") with field_masked.
	if !knownRoot(segs[0]) {
		return "", true
	}
	if len(segs) > 1 && (opaqueRoots[segs[0]] || !nestedRoots[segs[0]]) {
		segs = segs[:1]
		masked = true
	}
	if d, ok := maxDepth[segs[0]]; ok && len(segs) > d {
		segs = segs[:d]
		masked = true
	}
	if allowed, ok := fixedChildren[segs[0]]; ok && len(segs) > 1 && !allowed[segs[1]] {
		segs = segs[:1]
		masked = true
	}
	switch segs[0] {
	case "mcpServers":
		// Server-name segments are derived labels until PR-1b records their
		// origin (contract §Target, derived labels): never spelled here.
		if len(segs) > 1 {
			segs = segs[:1]
			masked = true
		}
	case "profiles":
		// /profiles/<name>/<fixed field>, nothing deeper or unknown.
		if len(segs) > 2 && !profileFields[segs[2]] {
			segs = segs[:2]
			masked = true
		} else if len(segs) > 3 {
			segs = segs[:3]
			masked = true
		}
	}
	var b strings.Builder
	for _, s := range segs {
		if containsPrivateKey(s) {
			masked = true
			break
		}
		seg := s
		if m, hit := maskAllSpans(seg); hit {
			noteFieldMask()
			seg = m
			masked = true
		}
		if rep := replaceHexEscaped(seg); rep != seg {
			seg = rep
			masked = true
		}
		if r := []rune(seg); len(r) > maxPointerSegment {
			seg = string(r[:maxPointerSegment])
			masked = true
		}
		b.WriteByte('/')
		b.WriteString(escapeSeg(seg))
	}
	out := b.String()
	if out == "" {
		// The first segment itself was a private-key label: no fixed parent
		// to report; the root pointer "" names "a field changed".
		return "", true
	}
	// Segments are masked one by one; a credential shape can still form
	// across a '/' (e.g. "Bearer " + "/description"). The sanitizer would
	// rewrite that pointer silently, so collapse it to its root instead.
	if _, hit := maskAllSpans(out); hit && len(segs) > 1 {
		noteFieldMask()
		return "/" + escapeSeg(segs[0]), true
	}
	if utf8.RuneCountInString(out) > maxPointerLength && len(segs) > 0 {
		return "/" + escapeSeg(segs[0]), true
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
	kindRateLimit
)

// literalFields is the literal list (contract §Changes): a path named here is
// rendered verbatim only when the value has the paired type. Later slices
// extend it (PR-2/PR-3: enums, bounded integers, name lists).
var literalFields = map[string]literalKind{
	"token_prefix":         kindTokenPrefix,
	"pending_token_prefix": kindTokenPrefix,
	"expires_at":           kindTimestamp,
	"logins":               kindCount,
	"guard_bound":          kindBool,
	"revoked":              kindBool,
	// Bounded integers, keyed by their full path (segments joined by '/').
	"rate_limit/per_minute": kindRateLimit,
	"rate_limit/burst":      kindRateLimit,
	"enabled":               kindBool,
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
	if _, ok := literalKindOf(f); ok {
		return classLiteral
	}
	return classWithheldValue
}

func literalKindOf(f FieldPath) (literalKind, bool) {
	if len(f.segs) == 0 || secretPrefixes[f.segs[0]] {
		return 0, false
	}
	// The table is keyed by segments joined with '/': a segment that itself
	// contains '/' would forge another path's key, so it is never literal.
	for _, seg := range f.segs {
		if strings.Contains(seg, "/") {
			return 0, false
		}
	}
	k, ok := literalFields[strings.Join(f.segs, "/")]
	return k, ok
}

// MaxRateLimit mirrors auth.MaxRateLimitPerMinute, the bound of the
// call-budget integers (a literal outside it is withheld).
const MaxRateLimit = 100000

// RateLimitValue is a call-budget integer (rate_limit.per_minute, burst),
// literal only within [0, MaxRateLimit].
type RateLimitValue int

func (RateLimitValue) literalKind() literalKind { return kindRateLimit }
func (v RateLimitValue) render() (interface{}, bool) {
	if v < 0 || v > MaxRateLimit {
		return nil, false
	}
	return int(v), true
}

// AllServersGranted is the name-list change of a grant to every server: the
// documented `*` sentinel added (contract §Changes, name lists). Lists that
// name servers are reported once PR-1b's server-name provenance exists;
// until then they are withheld.
func AllServersGranted(field FieldPath) Change { return AllServersGrantedAmong(field, 1) }

// AllServersGrantedAmong is AllServersGranted for a list of total entries
// that contains the sentinel: only "*" is rendered, added_count reports the
// full list and truncated marks the entries left out (server names, until
// PR-1b's provenance).
func AllServersGrantedAmong(field FieldPath, total int) Change {
	if field.sortKey() != Path("allowed_servers").sortKey() {
		// Only a server allow-list can carry the sentinel: anything else is
		// classified like any other path (never rendered).
		return Withheld(field, WithheldKind(ClassifyField(field)))
	}
	if total < 1 {
		total = 1
	}
	return Change{path: field, set: true, list: true, added: []interface{}{"*"}, addedCount: total, truncated: total > 1}
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
	tt := time.Time(t).UTC()
	if tt.IsZero() || tt.Year() < 0 || tt.Year() > 9999 {
		// The wire layout has a four-digit year.
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

// nilIfTypedNil turns a typed nil pointer (e.g. a nil *Timestamp for an unset
// expiry) into an absent side instead of a value whose methods would panic.
func nilIfTypedNil(v LiteralValue) LiteralValue {
	if v == nil {
		return nil
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil
	}
	return v
}

// Withheld kinds.
type WithheldKind string

const (
	WithheldSecret WithheldKind = "secret"
	WithheldValue  WithheldKind = "value"
)

// Change is one change entry. It can be built only by Literal or Withheld.
type Change struct {
	path FieldPath
	// before/after hold the values rendered at construction (a caller
	// mutating a value after Literal returned cannot change the line).
	before interface{}
	after  interface{}
	// list, added, addedCount, removedCount: a name-list diff entry.
	list         bool
	truncated    bool
	added        []interface{}
	addedCount   int
	removedCount int
	withheld     WithheldKind
	set          bool
}

// Literal records field going from before to after; a nil side does not
// exist. When the classifier does not name field as literal, a value type
// does not match the field, or a value is outside its closed set, the entry
// is withheld (as a secret when the path is secret-class) — never rendered.
func Literal(field FieldPath, before, after LiteralValue) Change {
	before, after = nilIfTypedNil(before), nilIfTypedNil(after)
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
	if before != nil {
		c.before, _ = before.render()
	}
	if after != nil {
		c.after, _ = after.render()
	}
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
	if len(c.path.segs) == 0 {
		return nil, fmt.Errorf("audit: change with an empty field path")
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
	if c.list {
		if len(c.added) > 0 {
			e["added"] = c.added
		}
		e["added_count"] = c.addedCount
		e["removed_count"] = c.removedCount
		if c.truncated {
			e["truncated"] = true
		}
		return e, nil
	}
	if c.before == nil && c.after == nil {
		return nil, fmt.Errorf("audit: literal change %s has neither side", field)
	}
	if c.before != nil {
		e["before"] = c.before
	}
	if c.after != nil {
		e["after"] = c.after
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
