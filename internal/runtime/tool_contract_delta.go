package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 120: the structural change gate that runs in front of the scan-mode
// approval of a changed tool contract. The offline scanner reads a schema as
// text and never sees the annotations, so the facts below are decided here,
// deterministically, from the approved and the live contract alone: no word
// list, no edit distance, no threshold. A signal holds the change whatever the
// scan verdict (FR-002); an empty delta lets a clean verdict approve it.
//
// The description is NOT compared: text is the scanner's.

// Signal is one structural-gate hold signal: a rule id (one of
// contracts.HoldSignal*), a reason and a path. Rendered as
// `rule[:reason[:path]]`, e.g. `GATE-SCHEMA-SHAPE:new-required:bcc`.
type Signal struct {
	Rule   string
	Reason string
	Path   string
}

// String renders the signal as it is recorded on the approval record.
func (s Signal) String() string {
	out := s.Rule
	if s.Reason != "" {
		out += ":" + s.Reason
	}
	if s.Path != "" {
		out += ":" + s.Path
	}
	return out
}

// SignalHintSaferFlip is kept for the spec-119 callers and tests: the
// safer-direction hint flip is one of the gate's signals (FR-003).
const SignalHintSaferFlip = contracts.HoldSignalHintSaferFlip

// contractSnapshot is the structural view of one tool contract the gate
// compares: input schema, output schema and the safety hints. A nil annotation
// snapshot is read as "no hints" (the MCP defaults); an empty input schema is
// read as unknown (FR-004 approved-unknown), never as a wildcard.
type contractSnapshot struct {
	InputSchema  string
	OutputSchema string
	Annotations  *config.ToolAnnotations
}

// approvedSnapshotOf returns the approved contract recorded on an approval
// record: the Current* fields while the record is approved (Current* ==
// Approved*), the Previous* fields once it is changed (Previous* is the
// approved contract's before-evidence; a cleared Previous* on a record whose
// stored snapshot was not proven approved reads as unknown and holds).
func approvedSnapshotOf(existing *storage.ToolApprovalRecord) contractSnapshot {
	if existing == nil {
		return contractSnapshot{}
	}
	if existing.Status == storage.ToolApprovalStatusChanged {
		return contractSnapshot{InputSchema: existing.PreviousSchema, OutputSchema: existing.PreviousOutputSchema, Annotations: existing.PreviousAnnotations}
	}
	return contractSnapshot{InputSchema: existing.CurrentSchema, OutputSchema: existing.CurrentOutputSchema, Annotations: existing.CurrentAnnotations}
}

// liveSnapshotOf is the live tool's structural view.
func liveSnapshotOf(tool *config.ToolMetadata, schemaJSON, outputSchemaJSON string) contractSnapshot {
	var ann *config.ToolAnnotations
	if tool != nil {
		ann = tool.Annotations
	}
	return contractSnapshot{InputSchema: schemaJSON, OutputSchema: outputSchemaJSON, Annotations: ann}
}

// contractDelta returns the structural hold signals between the approved and
// the live contract, in a fixed order (hint signals, then input-schema shape,
// then output schema), deduplicated. An empty result means the change is
// structurally neutral or narrowing and the scan verdict may decide.
func contractDelta(approved, live contractSnapshot) []Signal {
	var out []Signal
	out = append(out, hintDelta(approved.Annotations, live.Annotations)...)
	out = append(out, inputSchemaDelta(approved.InputSchema, live.InputSchema)...)
	out = append(out, outputSchemaDelta(approved.OutputSchema, live.OutputSchema)...)
	seen := make(map[string]bool, len(out))
	deduped := out[:0]
	for _, s := range out {
		key := s.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, s)
	}
	return deduped
}

// signalStrings renders signals for the approval record and the audit event.
func signalStrings(signals []Signal) []string {
	if len(signals) == 0 {
		return nil
	}
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		out = append(out, s.String())
	}
	return out
}

// maxGateSignals is how many gate signals a hold records verbatim; the rest
// are summarised per rule (compactGateSignals) so that storage's
// MaxToolHeldSignals cap never silently drops a rule family (review r1 P2).
const maxGateSignals = storage.MaxToolHeldSignals - 4

// compactGateSignals keeps the recorded gate signals within maxGateSignals
// while keeping every rule family visible: the first signal of each rule is
// always kept, the remaining slots are filled in order, and each rule that lost
// signals gets one `<rule>:more:+<n>` marker. Short lists pass through.
func compactGateSignals(all []string) []string {
	if len(all) <= maxGateSignals {
		return all
	}
	ruleOf := func(s string) string {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			return s[:i]
		}
		return s
	}
	keep := make(map[int]bool, maxGateSignals)
	seenRule := map[string]bool{}
	for i, s := range all {
		if r := ruleOf(s); !seenRule[r] {
			seenRule[r] = true
			keep[i] = true
		}
	}
	budget := maxGateSignals - len(seenRule) // slots left after one per rule; the markers come on top
	for i := range all {
		if budget <= 0 {
			break
		}
		if !keep[i] {
			keep[i] = true
			budget--
		}
	}
	dropped := map[string]int{}
	var rules []string
	out := make([]string, 0, maxGateSignals+len(seenRule))
	for i, s := range all {
		if keep[i] {
			out = append(out, s)
			continue
		}
		r := ruleOf(s)
		if dropped[r] == 0 {
			rules = append(rules, r)
		}
		dropped[r]++
	}
	for _, r := range rules {
		out = append(out, fmt.Sprintf("%s:more:+%d", r, dropped[r]))
	}
	return out
}

// withGateSignals prepends the gate's signals to the scan's evidence (FR-002:
// recorded whatever the verdict). A clean verdict becomes a scan_findings hold
// with those signals; a non-clean one keeps its verdict and its own signals
// after them. The gate signals go FIRST, compacted per rule, so
// storage.MaxToolHeldSignals never drops a rule family. The result is never
// nil and shares no slice with the input.
func withGateSignals(evidence *scanHoldEvidence, signals []Signal) *scanHoldEvidence {
	gate := compactGateSignals(signalStrings(signals))
	if evidence == nil {
		return &scanHoldEvidence{Reason: storage.ToolHeldReasonScanFindings, Verdict: "clean", Signals: gate}
	}
	out := *evidence
	if len(gate) == 0 {
		out.Signals = append([]string(nil), evidence.Signals...)
		return &out
	}
	out.Reason = storage.ToolHeldReasonScanFindings
	out.Signals = append(gate, evidence.Signals...)
	return &out
}

// ---- hints ---------------------------------------------------------------

// effectiveHints applies the MCP defaults (readOnly false, destructive true,
// idempotent false, openWorld true) to an annotation snapshot; nil is "no
// hints".
func effectiveHints(a *config.ToolAnnotations) (readOnly, destructive, idempotent, openWorld bool) {
	readOnly, destructive, idempotent, openWorld = false, true, false, true
	if a == nil {
		return
	}
	if a.ReadOnlyHint != nil {
		readOnly = *a.ReadOnlyHint
	}
	if a.DestructiveHint != nil {
		destructive = *a.DestructiveHint
	}
	if a.IdempotentHint != nil {
		idempotent = *a.IdempotentHint
	}
	if a.OpenWorldHint != nil {
		openWorld = *a.OpenWorldHint
	}
	return
}

// hintDelta is FR-003 plus the FR-009 dispatch rule: every effective hint
// change in either direction, the two safer flips also as GATE-HINT-SAFER-FLIP
// (first), and a dispatch-variant change (contracts.DeriveCallWith differs,
// which can happen with equal effective hints: explicit defaults vs none).
func hintDelta(approved, live *config.ToolAnnotations) []Signal {
	aRO, aD, aI, aOW := effectiveHints(approved)
	lRO, lD, lI, lOW := effectiveHints(live)
	var out []Signal
	if (!aRO && lRO) || (aD && !lD) {
		out = append(out, Signal{Rule: contracts.HoldSignalHintSaferFlip})
	}
	flip := func(name string, before, after bool) {
		if before != after {
			out = append(out, Signal{Rule: contracts.HoldSignalHintChange, Reason: name, Path: fmt.Sprintf("%t>%t", before, after)})
		}
	}
	flip("readOnlyHint", aRO, lRO)
	flip("destructiveHint", aD, lD)
	flip("idempotentHint", aI, lI)
	flip("openWorldHint", aOW, lOW)
	if before, after := dispatchVariantOf(approved), dispatchVariantOf(live); before != after {
		out = append(out, Signal{Rule: contracts.HoldSignalHintChange, Reason: "dispatch", Path: before + ">" + after})
	}
	return out
}

// dispatchVariantOf is the short name of the call_tool variant the tool is
// routed to (read | write | destructive).
func dispatchVariantOf(a *config.ToolAnnotations) string {
	return strings.TrimPrefix(contracts.DeriveCallWith(a), "call_tool_")
}

// dispatchVariantChanged reports whether the live hints route the tool to a
// different call_tool variant than the stored snapshot (FR-009). Only
// meaningful on a record whose snapshot was captured (hintSnapshotCaptured).
func dispatchVariantChanged(stored, live *config.ToolAnnotations) bool {
	return dispatchVariantOf(stored) != dispatchVariantOf(live)
}

// hintSnapshotCaptured reports whether a record's annotation snapshot is the
// approved contract's (written once the hints were part of the hash); a nil
// snapshot on an older record is unknown, not "no hints".
func hintSnapshotCaptured(existing *storage.ToolApprovalRecord) bool {
	return existing != nil && existing.HashSchemaVersion >= storage.AnnotationHashSchemaVersion
}

// ---- input schema ----------------------------------------------------------

const schemaRootPath = "/"

func childPath(parent, segment string) string {
	if parent == schemaRootPath {
		return segment
	}
	return parent + "/" + segment
}

func shape(reason, path string) Signal {
	return Signal{Rule: contracts.HoldSignalSchemaShape, Reason: reason, Path: path}
}

// inputSchemaDelta is FR-004: the recursive keyword-path diff. An empty or
// unparsable approved schema against a non-empty live one is approved-unknown
// (unknown is not a wildcard); an unparsable live schema that differs from the
// approved text is unparsable.
func inputSchemaDelta(approvedJSON, liveJSON string) []Signal {
	approvedJSON, liveJSON = strings.TrimSpace(approvedJSON), strings.TrimSpace(liveJSON)
	if approvedJSON == "" && liveJSON == "" {
		return nil
	}
	before, approvedOK := decodeSchema(approvedJSON)
	after, liveOK := decodeSchema(liveJSON)
	switch {
	case !approvedOK && liveJSON != "":
		return []Signal{shape("approved-unknown", schemaRootPath)}
	case !approvedOK:
		// Approved unparsable, live empty: the schema was dropped.
		return []Signal{shape("type", schemaRootPath)}
	case !liveOK:
		if normalizeJSON(approvedJSON) == normalizeJSON(liveJSON) {
			return nil
		}
		return []Signal{shape("unparsable", schemaRootPath)}
	}
	var out []Signal
	walkSchemaNode(schemaRootPath, before, after, &out)
	return out
}

// decodeSchema parses a schema with numbers kept as json.Number, so a bound or
// an enum member beyond float64 precision compares exactly (review r1 P1).
func decodeSchema(raw string) (any, bool) {
	if raw == "" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		// Trailing content after the first value: not one JSON document
		// (review r2 P2).
		return nil, false
	}
	return v, true
}

// nameMapKeywords are the keywords whose object value maps NAMES to schemas:
// the keys are property names or definition names, never schema keywords.
var nameMapKeywords = map[string]bool{"properties": true, "patternProperties": true, "$defs": true, "definitions": true, "dependentSchemas": true, "dependencies": true}

// subschemaKeywords are the keywords whose value is a schema or a list of
// schemas (walked by the steering and text-stripping passes).
var subschemaKeywords = map[string]bool{"items": true, "prefixItems": true, "additionalProperties": true, "not": true, "anyOf": true, "oneOf": true, "allOf": true, "if": true, "then": true, "else": true, "contains": true, "propertyNames": true, "additionalItems": true, "unevaluatedProperties": true, "unevaluatedItems": true}

func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// sameJSON compares two decoded JSON values by VALUE: numbers by their exact
// rational value at any depth (1 and 1.0 are equal, review r4 P2), object keys
// sorted, every other scalar by type and content.
func sameJSON(a, b any) bool {
	ab, errA := json.Marshal(canonicalValue(a))
	bb, errB := json.Marshal(canonicalValue(b))
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

// canonicalValue rewrites a decoded JSON value into an injective tagged form:
// every value becomes a two-element list [tag, payload] so a number can never
// collide with a string that spells the same (review r4 P1): numbers are
// ["n", RatString], strings ["s", v], booleans ["b", v], null ["z"], lists
// ["a", items...], objects ["o", {key: canonical}] (json.Marshal sorts keys).
func canonicalValue(v any) any {
	switch t := v.(type) {
	case nil:
		return []any{"z"}
	case bool:
		return []any{"b", t}
	case string:
		return []any{"s", t}
	case []any:
		out := make([]any, 0, len(t)+1)
		out = append(out, "a")
		for _, child := range t {
			out = append(out, canonicalValue(child))
		}
		return out
	case map[string]any:
		obj := make(map[string]any, len(t))
		for k, child := range t {
			obj[k] = canonicalValue(child)
		}
		return []any{"o", obj}
	default:
		if n, ok := numberOf(v); ok {
			return []any{"n", n.RatString()}
		}
		return []any{"x", fmt.Sprint(v)}
	}
}

func schemaKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// walkSchemaNode compares one schema node pair. Both are expected to be
// objects; a boolean or other scalar schema is compared whole as a type.
func walkSchemaNode(path string, before, after any, out *[]Signal) {
	b, bOK := asObject(before)
	a, aOK := asObject(after)
	if !bOK || !aOK {
		if !sameJSON(before, after) {
			*out = append(*out, shape("type", path))
		}
		return
	}
	// type / $ref
	if !sameJSON(typeSet(b["type"]), typeSet(a["type"])) {
		*out = append(*out, shape("type", path))
	}
	if _, hasB := b["$ref"]; hasB || hasKey(a, "$ref") {
		if !sameJSON(b["$ref"], a["$ref"]) {
			*out = append(*out, shape("type", path))
		}
	}
	// enum
	if bEnum, hasB := b["enum"]; hasB {
		if aEnum, hasA := a["enum"]; !hasA {
			*out = append(*out, shape("enum-dropped", path))
		} else if enumGainedMember(bEnum, aEnum) {
			*out = append(*out, shape("enum-added", path))
		}
	}
	// pattern / format: never narrowing
	for _, kw := range []string{"pattern", "format"} {
		if hasKey(b, kw) != hasKey(a, kw) || !sameJSON(b[kw], a[kw]) {
			*out = append(*out, shape(kw, path))
		}
	}
	// bounds
	for _, kw := range []string{"minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties"} {
		if boundLoosened(b, a, kw, true) {
			*out = append(*out, shape("bound", path+":"+kw))
		}
	}
	for _, kw := range []string{"maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties"} {
		if boundLoosened(b, a, kw, false) {
			*out = append(*out, shape("bound", path+":"+kw))
		}
	}
	if hasKey(b, "multipleOf") && (!hasKey(a, "multipleOf") || !sameJSON(b["multipleOf"], a["multipleOf"])) {
		*out = append(*out, shape("bound", path+":multipleOf"))
	}
	// additionalProperties
	bAP, bHasAP := b["additionalProperties"]
	aAP, aHasAP := a["additionalProperties"]
	if bHasAP && bAP == false {
		if !aHasAP || aAP != false {
			*out = append(*out, shape("additional-properties", path))
		}
	} else if bAPObj, ok := asObject(bAP); ok {
		if aAPObj, ok := asObject(aAP); ok {
			// Two schemas recurse, an unconstrained `{}` included: a default,
			// const or pattern added under it is a widening (review r2 P1).
			walkSchemaNode(childPath(path, "additionalProperties"), bAPObj, aAPObj, out)
		} else if !schemaIsUnconstrained(bAPObj) && (!aHasAP || aAP != false) {
			// A constraining schema disappeared (or became `true`); `{}`
			// constrained nothing, so its disappearance is neutral.
			*out = append(*out, shape("additional-properties", path))
		}
	} else if aAPObj, ok := asObject(aAP); ok && carriesSteering(aAPObj) {
		// A schema introduced where additional properties were unconstrained
		// (absent or true) narrows them, but a default/const/enum under it
		// steers what the model sends (review r3 P1).
		*out = append(*out, shape("new-property", childPath(path, "additionalProperties")))
	}
	// default / const
	if _, hasA := a["default"]; hasA && (!hasKey(b, "default") || !sameJSON(b["default"], a["default"])) {
		*out = append(*out, shape("default", path))
	}
	if hasKey(b, "const") != hasKey(a, "const") || !sameJSON(b["const"], a["const"]) {
		*out = append(*out, shape("const", path))
	}
	// required
	bReq := requiredSet(b["required"])
	for _, name := range sortedStrings(a["required"]) {
		if !bReq[name] {
			*out = append(*out, shape("new-required", childPath(path, name)))
		}
	}
	// properties / patternProperties / $defs / definitions
	walkKeyedSchemas(path, b, a, "properties", "", out)
	walkKeyedSchemas(path, b, a, "patternProperties", "patternProperties", out)
	walkKeyedSchemas(path, b, a, "$defs", "$defs", out)
	walkKeyedSchemas(path, b, a, "definitions", "definitions", out)
	// items
	walkItems(path, b["items"], hasKey(b, "items"), a["items"], hasKey(a, "items"), out)
	// combinators
	walkCombinator(path, b, a, "anyOf", true, out)
	walkCombinator(path, b, a, "oneOf", true, out)
	walkCombinator(path, b, a, "allOf", false, out)
	if hasKey(b, "not") != hasKey(a, "not") || !sameJSON(stripText(b["not"]), stripText(a["not"])) {
		*out = append(*out, shape("combinator", path+":not"))
	}
}

// walkKeyedSchemas walks a map keyword (properties, patternProperties, $defs,
// definitions). A key present only in the live schema is a new entry: held
// when its subtree carries a default, const or enum anywhere (a new required
// property is reported by the required rule), otherwise the benign optional
// addition. A removed key passes.
func walkKeyedSchemas(path string, b, a map[string]any, keyword, segment string, out *[]Signal) {
	bMap, _ := asObject(b[keyword])
	aMap, _ := asObject(a[keyword])
	base := path
	if segment != "" {
		base = childPath(path, segment)
	}
	for _, key := range schemaKeys(aMap) {
		p := childPath(base, key)
		if bChild, ok := bMap[key]; ok {
			walkSchemaNode(p, bChild, aMap[key], out)
			continue
		}
		if carriesSteering(aMap[key]) {
			*out = append(*out, shape("new-property", p))
		}
	}
}

// walkItems compares the items keyword: object form recurses, tuple form
// recurses pairwise (a length change is a type change), a form change is a
// type change, a dropped items schema is a type change and a new one holds
// only when it carries steering keywords.
func walkItems(path string, bItems any, bHas bool, aItems any, aHas bool, out *[]Signal) {
	p := childPath(path, "items")
	switch {
	case !bHas && !aHas:
		return
	case bHas && !aHas:
		*out = append(*out, shape("type", p))
		return
	case !bHas:
		if carriesSteering(aItems) {
			*out = append(*out, shape("new-property", p))
		}
		return
	}
	bList, bIsList := bItems.([]any)
	aList, aIsList := aItems.([]any)
	switch {
	case bIsList && aIsList:
		if len(bList) != len(aList) {
			*out = append(*out, shape("type", p))
			return
		}
		for i := range bList {
			walkSchemaNode(childPath(p, fmt.Sprint(i)), bList[i], aList[i], out)
		}
	case bIsList != aIsList:
		*out = append(*out, shape("type", p))
	default:
		walkSchemaNode(p, bItems, aItems, out)
	}
}

// walkCombinator compares anyOf/oneOf (growing widens) and allOf (shrinking
// widens). Equal-length lists recurse pairwise. A combinator appearing where
// there was none, or disappearing, is a shape change for anyOf/oneOf; an allOf
// that appears passes (narrowing) and one that disappears holds.
func walkCombinator(path string, b, a map[string]any, keyword string, growWidens bool, out *[]Signal) {
	bList, bHas := b[keyword].([]any)
	aList, aHas := a[keyword].([]any)
	if !hasKey(b, keyword) && !hasKey(a, keyword) {
		return
	}
	sig := shape("combinator", path+":"+keyword)
	switch {
	case bHas && !aHas:
		*out = append(*out, sig)
	case !bHas && aHas:
		if growWidens {
			*out = append(*out, sig)
		}
	default:
		// The retained branches are compared pairwise whatever the length
		// change, so a default or type moved inside branch 0 is not hidden by
		// an appended branch (review r6 P1); the length rule applies on top.
		for i := 0; i < len(bList) && i < len(aList); i++ {
			walkSchemaNode(childPath(path, keyword+"/"+fmt.Sprint(i)), bList[i], aList[i], out)
		}
		if len(aList) != len(bList) && (len(aList) > len(bList)) == growWidens {
			*out = append(*out, sig)
		}
	}
}

// carriesSteering reports whether a schema subtree carries a default, const
// or enum at any schema node: the keywords that steer what a model sends
// without the operator reading a description. The walk is schema-aware
// (review r1 P2): the keys of a properties/$defs map are names, not keywords,
// and text keywords (examples, description) are data, so a property NAMED
// "default" or an example containing one does not count.
func carriesSteering(node any) bool {
	if list, isList := node.([]any); isList {
		// A schema list (a new tuple-form items, review r2 P1).
		for _, item := range list {
			if carriesSteering(item) {
				return true
			}
		}
		return false
	}
	schema, ok := asObject(node)
	if !ok {
		return false
	}
	for _, kw := range []string{"default", "const", "enum"} {
		if _, ok := schema[kw]; ok {
			return true
		}
	}
	for _, k := range schemaKeys(schema) {
		switch {
		case nameMapKeywords[k]:
			names, _ := asObject(schema[k])
			for _, name := range schemaKeys(names) {
				if carriesSteering(names[name]) {
					return true
				}
			}
		case subschemaKeywords[k]:
			switch child := schema[k].(type) {
			case []any:
				for _, item := range child {
					if carriesSteering(item) {
						return true
					}
				}
			default:
				if carriesSteering(child) {
					return true
				}
			}
		}
	}
	return false
}

// validationKeywords are the JSON Schema keywords that can reject a value.
// Anything else (text, default, examples, $id, vendor x-* annotations) is
// not a constraint (review r6 P2).
var validationKeywords = map[string]bool{
	"type": true, "$ref": true, "enum": true, "const": true, "pattern": true, "format": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "minItems": true, "maxItems": true, "minProperties": true, "maxProperties": true,
	"uniqueItems": true, "required": true, "properties": true, "patternProperties": true, "additionalProperties": true,
	"items": true, "prefixItems": true, "additionalItems": true, "contains": true, "minContains": true, "maxContains": true,
	"not": true, "anyOf": true, "oneOf": true, "allOf": true, "if": true, "then": true, "else": true,
	"dependencies": true, "dependentRequired": true, "dependentSchemas": true, "propertyNames": true,
	"unevaluatedProperties": true, "unevaluatedItems": true,
}

// schemaIsUnconstrained reports whether a schema object constrains nothing
// (`{}` accepts every value, like `true`): no validation keyword with a
// non-empty value. Text, default, examples and unknown annotation keywords do
// not count; an empty required list or an empty properties map does not either
// (review r5).
func schemaIsUnconstrained(schema map[string]any) bool {
	for k, v := range schema {
		if !validationKeywords[k] {
			continue
		}
		switch t := v.(type) {
		case []any:
			if len(t) == 0 {
				continue
			}
		case map[string]any:
			if len(t) == 0 && nameMapKeywords[k] {
				continue
			}
		}
		return false
	}
	return true
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// typeSet normalizes `type` (a string or a list) to a sorted list.
func typeSet(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, fmt.Sprint(item))
		}
		sort.Strings(out)
		return out
	default:
		return fmt.Sprint(t)
	}
}

func requiredSet(v any) map[string]bool {
	out := map[string]bool{}
	for _, s := range sortedStrings(v) {
		out[s] = true
	}
	return out
}

func sortedStrings(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, fmt.Sprint(item))
	}
	sort.Strings(out)
	return out
}

// enumGainedMember reports whether the live enum carries a member the
// approved one did not (members compared by JSON value).
func enumGainedMember(before, after any) bool {
	bList, _ := before.([]any)
	aList, _ := after.([]any)
	seen := make(map[string]bool, len(bList))
	for _, item := range bList {
		seen[enumMemberKey(item)] = true
	}
	for _, item := range aList {
		if !seen[enumMemberKey(item)] {
			return true
		}
	}
	return false
}

// enumMemberKey identifies an enum member by VALUE (canonicalValue): every
// number in it, at any depth, by its exact rational value, and a number never
// collides with a string (review r2 P2 / r3 P2 / r4 P1).
func enumMemberKey(item any) string {
	raw, _ := json.Marshal(canonicalValue(item))
	return string(raw)
}

// boundLoosened reports whether a numeric bound keyword got looser or
// disappeared. isMin: a smaller value loosens; otherwise a larger one does. A
// non-numeric value on either side (draft-4 boolean exclusive bounds) is
// compared as text and any difference holds. An added bound passes.
func boundLoosened(b, a map[string]any, kw string, isMin bool) bool {
	bv, bHas := b[kw]
	if !bHas {
		return false
	}
	av, aHas := a[kw]
	if !aHas {
		return true
	}
	bf, bNum := numberOf(bv)
	af, aNum := numberOf(av)
	if !bNum || !aNum {
		return !sameJSON(bv, av)
	}
	cmp := af.Cmp(bf)
	if isMin {
		return cmp < 0
	}
	return cmp > 0
}

// numberOf reads a JSON number (json.Number from decodeSchema, or a float64)
// exactly.
func numberOf(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(n.String())
		return r, ok
	case float64:
		return new(big.Rat).SetFloat64(n), true
	}
	return nil, false
}

// ---- output schema ---------------------------------------------------------

// outputSchemaDelta is FR-005: added, removed, or structurally changed (text
// keywords stripped at every level, keys sorted).
func outputSchemaDelta(approvedJSON, liveJSON string) []Signal {
	approvedJSON, liveJSON = strings.TrimSpace(approvedJSON), strings.TrimSpace(liveJSON)
	sig := func(reason string) []Signal {
		return []Signal{{Rule: contracts.HoldSignalOutputSchema, Reason: reason}}
	}
	switch {
	case approvedJSON == "" && liveJSON == "":
		return nil
	case approvedJSON == "":
		return sig("added")
	case liveJSON == "":
		return sig("removed")
	}
	if structuralForm(approvedJSON) != structuralForm(liveJSON) {
		return sig("changed")
	}
	return nil
}

// textKeywords are the schema keywords the scanner reads and the gate ignores.
var textKeywords = map[string]bool{"description": true, "title": true, "examples": true, "$comment": true}

// structuralForm is a schema with its text keywords removed at every level,
// serialized with sorted keys; unparsable input is returned as normalized text.
func structuralForm(raw string) string {
	v, ok := decodeSchema(raw)
	if !ok {
		return normalizeJSON(raw)
	}
	out, err := json.Marshal(stripText(v))
	if err != nil {
		return normalizeJSON(raw)
	}
	return string(out)
}

// stripText removes the text keywords from a SCHEMA node and from every
// subschema under it. It is schema-aware (review r1 P1): the keys of a
// properties/$defs map are names and are kept even when a property is called
// "description"; only schema-keyword positions are stripped.
func stripText(v any) any {
	schema, ok := asObject(v)
	if !ok {
		return v
	}
	out := make(map[string]any, len(schema))
	for k, child := range schema {
		switch {
		case textKeywords[k]:
			continue
		case nameMapKeywords[k]:
			names, isMap := asObject(child)
			if !isMap {
				out[k] = child
				continue
			}
			kept := make(map[string]any, len(names))
			for name, sub := range names {
				kept[name] = stripText(sub)
			}
			out[k] = kept
		case subschemaKeywords[k]:
			if list, isList := child.([]any); isList {
				stripped := make([]any, len(list))
				for i, item := range list {
					stripped[i] = stripText(item)
				}
				out[k] = stripped
			} else {
				out[k] = stripText(child)
			}
		default:
			out[k] = child
		}
	}
	return out
}
