package toolsig

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Signature is the deterministic compact rendering of one tool (Spec 085,
// data-model §1). Sig is the parenthesized parameter list; Desc is the
// verbatim first-sentence prefix of the description; Lossy is true iff Sig
// contains at least one "~" (strict biconditional — grammar contract §1).
type Signature struct {
	Sig   string // e.g. "(origin*:str, ttl:int=3600, account~:obj)"; "()" for no params; "(~)" for an unparseable schema
	Desc  string // first-sentence verbatim prefix (may be "")
	Lossy bool   // true iff any param (or the whole schema) collapsed under "~" (FR-004)
}

// maxDescPrefix is the hard cap (in runes) for the no-terminator
// first-sentence fallback (grammar contract §6).
const maxDescPrefix = 200

// maxInlineEnum is the largest enum inlined into a signature; longer enums
// collapse to the base type with the lossy marker (grammar contract §3).
const maxInlineEnum = 5

// maxDefaultLen is the longest default literal (in runes, before quoting)
// inlined into a signature (grammar contract §4).
const maxDefaultLen = 20

// Render compiles a tool's stored input schema (ParamsJSON) and description
// into its compact Signature per the normative grammar
// (specs/085-compact-router/contracts/signature-grammar.md).
//
// Rendering is fail-soft: it NEVER returns an unusable Signature. When
// paramsJSON cannot be parsed as an object schema, the returned Signature is
// the "(~)" fallback (Lossy=true, E11) and the error describes why — callers
// may log the error but must still use the Signature; failing the whole
// response on it would violate the contract.
func Render(paramsJSON, description string) (Signature, error) {
	sig := Signature{Desc: FirstSentence(description)}

	schema, err := parseObjectSchema(paramsJSON)
	if err != nil {
		sig.Sig = "(~)"
		sig.Lossy = true
		return sig, err
	}

	properties := objectField(schema, "properties")
	requiredNames := requiredList(schema)

	requiredSet := make(map[string]bool, len(requiredNames))
	for _, name := range requiredNames {
		requiredSet[name] = true
	}

	var optionalNames []string
	for name := range properties {
		if !requiredSet[name] {
			optionalNames = append(optionalNames, name)
		}
	}
	sort.Strings(optionalNames)

	parts := make([]string, 0, len(requiredNames)+len(optionalNames))
	lossy := false
	// Required first, in required-array order (never elided — FR-003).
	for _, name := range requiredNames {
		text, paramLossy := renderParam(name, properties[name], true)
		parts = append(parts, text)
		lossy = lossy || paramLossy
	}
	// Optionals after, sorted by name (code-point order).
	for _, name := range optionalNames {
		text, paramLossy := renderParam(name, properties[name], false)
		parts = append(parts, text)
		lossy = lossy || paramLossy
	}

	sig.Sig = "(" + strings.Join(parts, ", ") + ")"
	sig.Lossy = lossy
	return sig, nil
}

// parseObjectSchema parses paramsJSON as a JSON-Schema object schema. An
// empty string means "no declared params" (mirrors the tolerant inputSchema
// fallback in the full-mode response path) and yields an empty schema.
func parseObjectSchema(paramsJSON string) (map[string]any, error) {
	if strings.TrimSpace(paramsJSON) == "" {
		return map[string]any{}, nil
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &schema); err != nil {
		return nil, fmt.Errorf("toolsig: unparseable ParamsJSON: %w", err)
	}
	// A declared top-level type other than "object" is not an object schema.
	if typ, ok := schema["type"].(string); ok && typ != "object" {
		return nil, errors.New("toolsig: ParamsJSON is not an object schema (type=" + typ + ")")
	}
	return schema, nil
}

// objectField returns schema[key] as a map, or an empty map.
func objectField(schema map[string]any, key string) map[string]any {
	if m, ok := schema[key].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// requiredList extracts the schema "required" array (string members only) in
// declared order, de-duplicated.
func requiredList(schema map[string]any) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	seen := make(map[string]bool, len(raw))
	var names []string
	for _, v := range raw {
		name, ok := v.(string)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// renderParam renders one parameter: name + markers (*~) + typespec
// [+ =default]. A required name with no resolvable property schema still
// renders (name*~:any) — dropping it is a hard invariant violation (FR-003,
// grammar E8).
func renderParam(name string, propSchema any, required bool) (text string, lossy bool) {
	typespec, lossy, defaultLiteral := resolveTypespec(propSchema, required)

	var b strings.Builder
	b.WriteString(quoteAtom(name))
	if required {
		b.WriteString("*")
	}
	if lossy {
		b.WriteString("~")
	}
	b.WriteString(":")
	b.WriteString(typespec)
	if defaultLiteral != "" {
		b.WriteString("=")
		b.WriteString(defaultLiteral)
	}
	return b.String(), lossy
}

// resolveTypespec maps a property schema to its typespec (grammar §3), the
// lossy verdict, and the rendered default literal ("" when none applies —
// defaults render for optional short scalars only, §4).
func resolveTypespec(propSchema any, required bool) (typespec string, lossy bool, defaultLiteral string) {
	prop, ok := propSchema.(map[string]any)
	if !ok {
		// Absent (required name not in properties — E8), boolean schema, or
		// otherwise unresolvable: any + lossy. The name is never dropped.
		return "any", true, ""
	}

	typespec, lossy = resolvePropType(prop)
	if !required {
		// renderDefault itself gates on scalar typespecs: object/array/
		// collapsed defaults are dropped (lossiness already covers them) and
		// required params never carry a default (their * is the contract).
		defaultLiteral = renderDefault(prop, typespec)
	}
	return typespec, lossy, defaultLiteral
}

// resolvePropType implements the §3 type-mapping table for one property.
func resolvePropType(prop map[string]any) (typespec string, lossy bool) {
	// 1. Enum: inline when <=5 scalar values; otherwise collapse to the base
	//    type with the lossy marker (values dropped).
	if rawEnum, ok := prop["enum"].([]any); ok {
		if literals, scalar := enumLiterals(rawEnum); scalar && len(literals) > 0 && len(literals) <= maxInlineEnum {
			return "enum[" + strings.Join(literals, "|") + "]", false
		}
		base, _ := resolveDeclaredType(prop)
		if base == "" {
			base = "any"
		}
		return base, true
	}

	// 2. Declared type (string or non-null union).
	if base, ok := resolveDeclaredType(prop); ok {
		switch base {
		case "str", "int", "num", "bool":
			return base, false
		case "obj", "any":
			return base, true
		default:
			if strings.HasPrefix(base, "[") { // array
				return base, base == "[obj]" || base == "[any]"
			}
			if strings.Contains(base, "|") { // union
				return base, unionLossy(base)
			}
			return base, true
		}
	}

	// 3. anyOf/oneOf of typed subschemas (incl. the nullable pattern §3).
	for _, key := range []string{"anyOf", "oneOf"} {
		if subs, ok := prop[key].([]any); ok {
			return resolveSubschemaUnion(subs)
		}
	}

	// 4. $ref / recursion / no resolvable type.
	if _, ok := prop["$ref"]; ok {
		return "obj", true
	}
	if _, ok := prop["properties"]; ok {
		// Implied object.
		return "obj", true
	}
	return "any", true
}

// resolveDeclaredType maps the "type" keyword (string or array-of-strings) to
// a typespec. ok=false when "type" is absent or carries no usable member.
func resolveDeclaredType(prop map[string]any) (typespec string, ok bool) {
	switch typ := prop["type"].(type) {
	case string:
		return mapSingleType(typ, prop)
	case []any:
		var members []string
		seen := map[string]bool{}
		for _, m := range typ {
			name, isStr := m.(string)
			if !isStr || name == "null" {
				continue // null dropped: nullable == omittable
			}
			mapped, _ := mapUnionMember(name)
			if seen[mapped] {
				continue
			}
			seen[mapped] = true
			members = append(members, mapped)
		}
		if len(members) == 0 {
			return "any", true
		}
		// A degenerate one-member union (e.g. ["string","null"]) renders as
		// the plain type; resolvePropType derives lossiness from the result.
		return strings.Join(members, "|"), true
	}
	return "", false
}

// mapSingleType maps one JSON-Schema type name to a typespec.
func mapSingleType(typ string, prop map[string]any) (typespec string, ok bool) {
	switch typ {
	case "string":
		return "str", true
	case "integer":
		return "int", true
	case "number":
		return "num", true
	case "boolean":
		return "bool", true
	case "object":
		return "obj", true
	case "array":
		return mapArrayType(prop), true
	case "null":
		return "any", true
	default:
		return "any", true
	}
}

// mapArrayType renders the array typespec from "items" (grammar §3):
// scalar element -> [scalar] (not lossy); object element -> [obj] (lossy);
// unknown/absent element -> [any] (lossy).
func mapArrayType(prop map[string]any) string {
	items, ok := prop["items"].(map[string]any)
	if !ok {
		return "[any]"
	}
	switch typ, _ := items["type"].(string); typ {
	case "string":
		return "[str]"
	case "integer":
		return "[int]"
	case "number":
		return "[num]"
	case "boolean":
		return "[bool]"
	case "object":
		return "[obj]"
	default:
		return "[any]"
	}
}

// mapUnionMember maps one non-null union member to a basetype (§1 union
// production: scalar | obj | any) and whether it makes the union lossy.
func mapUnionMember(typ string) (basetype string, lossy bool) {
	switch typ {
	case "string":
		return "str", false
	case "integer":
		return "int", false
	case "number":
		return "num", false
	case "boolean":
		return "bool", false
	case "object":
		return "obj", true
	default: // array or unknown — not representable as a union member
		return "any", true
	}
}

// unionLossy reports whether a rendered union contains a collapsed member.
func unionLossy(union string) bool {
	for _, m := range strings.Split(union, "|") {
		if m == "obj" || m == "any" {
			return true
		}
	}
	return false
}

// resolveSubschemaUnion resolves anyOf/oneOf members that each declare a
// simple type. Null members are dropped (nullable == omittable). A member
// without a resolvable simple type collapses the whole param (oneOf of
// objects, $ref members, ...).
func resolveSubschemaUnion(subs []any) (typespec string, lossy bool) {
	var members []string
	seen := map[string]bool{}
	anyLossy := false
	for _, s := range subs {
		sub, ok := s.(map[string]any)
		if !ok {
			return "obj", true
		}
		typ, ok := sub["type"].(string)
		if !ok {
			return "obj", true
		}
		if typ == "null" {
			continue
		}
		mapped, memberLossy := mapUnionMember(typ)
		if seen[mapped] {
			continue
		}
		seen[mapped] = true
		members = append(members, mapped)
		anyLossy = anyLossy || memberLossy
	}
	switch len(members) {
	case 0:
		return "any", true
	case 1:
		return members[0], members[0] == "obj" || members[0] == "any"
	default:
		return strings.Join(members, "|"), anyLossy
	}
}

// isScalarTypespec reports whether a typespec is a plain scalar (eligible to
// carry a default even when the param is lossy for other reasons — kept
// conservative: only bare scalars qualify).
func isScalarTypespec(t string) bool {
	switch t {
	case "str", "int", "num", "bool":
		return true
	}
	return false
}

// renderDefault renders the "=default" literal for an optional scalar param
// (grammar §4): scalar default value, <=20 chars, atom-quoted when needed.
// Returns "" when no default applies.
func renderDefault(prop map[string]any, typespec string) string {
	if !isScalarTypespec(typespec) {
		return ""
	}
	raw, present := prop["default"]
	if !present {
		return ""
	}
	literal, scalar := scalarLiteral(raw)
	if !scalar {
		return "" // object/array defaults dropped
	}
	if utf8.RuneCountInString(literal) > maxDefaultLen {
		return ""
	}
	return quoteAtom(literal)
}

// enumLiterals renders enum values as atoms in declared order. scalar=false
// when any value is non-scalar (whole enum collapses instead).
func enumLiterals(values []any) (literals []string, scalar bool) {
	literals = make([]string, 0, len(values))
	for _, v := range values {
		lit, ok := scalarLiteral(v)
		if !ok {
			return nil, false
		}
		literals = append(literals, quoteAtom(lit))
	}
	return literals, true
}

// scalarLiteral formats a JSON scalar deterministically. ok=false for
// composite values.
func scalarLiteral(v any) (literal string, ok bool) {
	switch val := v.(type) {
	case nil:
		return "null", true
	case bool:
		return strconv.FormatBool(val), true
	case string:
		return val, true
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64), true
	case json.Number:
		return val.String(), true
	default:
		return "", false
	}
}

// signature metacharacters (grammar §3.5): any atom containing one of these
// (or the empty atom) renders quoted.
const metachars = ` ,:|=()*~[]"`

// quoteAtom renders an atom (name / enum value / default literal): bare when
// non-empty and metachar-free; otherwise double-quoted with embedded `"` and
// `\` backslash-escaped (unambiguous and reversible, §3.5).
func quoteAtom(atom string) string {
	if atom != "" && !strings.ContainsAny(atom, metachars) {
		return atom
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range atom {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// parseQuotedAtom is the reference parser for a quoted atom — it recovers the
// original string from quoteAtom output. Used by tests to prove the escaping
// round-trips; exported grammar consumers should treat signatures as opaque.
func parseQuotedAtom(quoted string) (string, error) {
	if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		return "", errors.New("toolsig: not a quoted atom")
	}
	var b strings.Builder
	escaped := false
	body := quoted[1 : len(quoted)-1]
	for _, r := range body {
		if escaped {
			if r != '"' && r != '\\' {
				return "", fmt.Errorf("toolsig: invalid escape \\%c", r)
			}
			b.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '"':
			return "", errors.New("toolsig: unescaped quote inside atom")
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		return "", errors.New("toolsig: dangling escape")
	}
	return b.String(), nil
}

// cjkTerminators end a sentence unconditionally (unspaced scripts, §6).
// asciiTerminators end a sentence only at a boundary: immediately followed by
// whitespace, EOF, or a closing quote/bracket.
var (
	cjkTerminators   = map[rune]bool{'。': true, '！': true, '？': true}
	asciiTerminators = map[rune]bool{'.': true, '!': true, '?': true}
	closingAfter     = map[rune]bool{'"': true, ')': true, ']': true, '}': true}
)

// FirstSentence extracts the deterministic verbatim first-sentence prefix of
// a description (grammar contract §6): the earliest matching terminator of
// either class wins; with no terminator in the first maxDescPrefix runes the
// verbatim capped prefix is returned (rune-boundary safe), with a trailing
// "…" only when truncation actually occurred. Empty/whitespace-only input
// yields "".
func FirstSentence(description string) string {
	if strings.TrimSpace(description) == "" {
		return ""
	}

	runes := []rune(description)
	for i, r := range runes {
		if i >= maxDescPrefix {
			break
		}
		if cjkTerminators[r] {
			return string(runes[:i+1])
		}
		if asciiTerminators[r] {
			atEOF := i == len(runes)-1
			if atEOF {
				return string(runes[:i+1])
			}
			next := runes[i+1]
			if isSpaceRune(next) || closingAfter[next] {
				return string(runes[:i+1])
			}
		}
	}

	if len(runes) <= maxDescPrefix {
		return description
	}
	return string(runes[:maxDescPrefix]) + "…"
}

// isSpaceRune matches Unicode whitespace for the ASCII-terminator boundary.
func isSpaceRune(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', 0x85, 0xA0:
		return true
	}
	return false
}
