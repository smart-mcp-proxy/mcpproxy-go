package bench

import (
	"encoding/json"
	"fmt"
)

// Spec 103 US4 — payload decomposition.
//
// Spec 102 projected ~70% savings from deferring input schemas and measured
// 29.7% on the frozen 45-tool corpus, concluding that names, descriptions and
// annotations carry most of the payload rather than schemas. That conclusion
// was drawn from two frozen corpora and never checked against another shape,
// and it is the single claim every reader of the benchmark will want tested.
//
// This file tests it the only way that can be honest: attribute the payload of
// a real corpus to its components and recompute the achievable ceiling FOR THAT
// CORPUS.
//
// Two measurement hazards shape the implementation, and both would produce a
// plausible-looking wrong answer:
//
//  1. TOKENS ARE NOT ADDITIVE ACROSS CONCATENATION. BPE merges across component
//     boundaries, so Count(name) + Count(description) + Count(schema) does not
//     equal Count(name + "\n" + description + "\n" + schema). Counting each
//     component separately yields shares that never sum to the payload and that
//     misattribute every boundary. So the payload is tokenized ONCE and each
//     token is attributed to a component by byte offset, reusing the span
//     partition AttributeTokens already implements for response cost.
//
//  2. THE CORPUS CARRIES NO ANNOTATIONS. bench.Tool is {ToolID, Server, Name,
//     Description, Schema}. Spec 102 measured the production WIRE payload,
//     which includes mcp-go's annotation defaults; the frozen corpora do not.
//     These are different payloads, and a decomposition that silently reported
//     a three-way split as though it settled a claim about four components
//     would read as a confirmation while measuring something else. The
//     Limitation field says so, in the report, next to the verdict.

// PayloadComponent labels the parts of a rendered tool definition.
const (
	PayloadComponentName        = "name"
	PayloadComponentDescription = "description"
	PayloadComponentSchema      = "schema"
	// PayloadComponentStructural is the rendering's own overhead — the
	// separators between components. Small, but it belongs to some component
	// or the shares do not partition the payload, and folding it into a
	// content component would overstate that component.
	PayloadComponentStructural = "structural"
)

// PayloadDecomposition attributes one corpus's rendered payload to its
// components, and states what that corpus shape can and cannot settle.
type PayloadDecomposition struct {
	FleetID     string `json:"fleet_id"`
	ToolCount   int    `json:"tool_count"`
	TotalTokens int    `json:"total_tokens"`
	// ToolsWithSchema is how many of ToolCount actually carry an input schema.
	// Load-bearing: a corpus with none cannot measure a schema share at all,
	// and a 0% result there means "nothing to defer was present", not "schemas
	// are cheap". Reporting the second from the first is the failure this
	// field exists to make impossible.
	ToolsWithSchema int `json:"tools_with_schema"`

	NameTokens        int `json:"name_tokens"`
	DescriptionTokens int `json:"description_tokens"`
	SchemaTokens      int `json:"schema_tokens"`
	StructuralTokens  int `json:"structural_tokens"`

	NamePct        float64 `json:"name_pct"`
	DescriptionPct float64 `json:"description_pct"`
	SchemaPct      float64 `json:"schema_pct"`
	StructuralPct  float64 `json:"structural_pct"`

	// SchemaCeilingPct is the most any schema-deferring design could ever save
	// on THIS corpus: the schema share, since deleting the schema entirely is
	// the upper bound of deferring it. Recomputed per corpus — carrying a
	// ceiling forward is the error spec 102 made.
	SchemaCeilingPct float64 `json:"schema_ceiling_pct"`

	// Spec102Verdict is an explicit confirmed/corrected statement rather than
	// a number the reader has to interpret.
	Spec102Verdict string `json:"spec_102_verdict"`

	// Limitation names what this corpus shape cannot settle. Always populated:
	// a decomposition with no stated limits is claiming there are none.
	Limitation string `json:"limitation"`

	Provenance string `json:"provenance"`
}

// DecomposePayload attributes the canonical rendering of every tool in the
// corpus to name, description, schema and structural overhead.
//
// The rendering matches the baseline arm's canonical form —
// name + "\n" + description + "\n" + canonical(schema) — because that is what
// the savings figures are measured against. Decomposing a different rendering
// would produce shares that do not explain those figures.
func DecomposePayload(tk *Tokenizer, corpus *Corpus, fleetID string) (*PayloadDecomposition, error) {
	if tk == nil {
		return nil, fmt.Errorf("payload decomposition: nil tokenizer")
	}
	if corpus == nil || len(corpus.Tools) == 0 {
		// Never a zero-filled result: zeros render as "measured, and every
		// component costs nothing", which is worse than an error.
		return nil, fmt.Errorf("payload decomposition: corpus %q has no tools", fleetID)
	}

	d := &PayloadDecomposition{
		FleetID:    fleetID,
		ToolCount:  len(corpus.Tools),
		Provenance: ProvenanceMeasured,
	}

	for _, tl := range corpus.Tools {
		if len(tl.Schema) > 0 {
			d.ToolsWithSchema++
		}
		text, spans, err := partitionToolDefinition(tl)
		if err != nil {
			return nil, fmt.Errorf("payload decomposition: tool %s: %w", tl.ToolID, err)
		}
		total, components, err := AttributeTokens(tk, text, spans)
		if err != nil {
			return nil, fmt.Errorf("payload decomposition: tool %s: %w", tl.ToolID, err)
		}
		d.TotalTokens += total
		d.NameTokens += components[PayloadComponentName]
		d.DescriptionTokens += components[PayloadComponentDescription]
		d.SchemaTokens += components[PayloadComponentSchema]
		d.StructuralTokens += components[PayloadComponentStructural]
	}

	if d.TotalTokens > 0 {
		pct := func(n int) float64 { return float64(n) / float64(d.TotalTokens) * 100 }
		d.NamePct = pct(d.NameTokens)
		d.DescriptionPct = pct(d.DescriptionTokens)
		d.SchemaPct = pct(d.SchemaTokens)
		d.StructuralPct = pct(d.StructuralTokens)
		d.SchemaCeilingPct = d.SchemaPct
	}

	d.Spec102Verdict = spec102Verdict(d)
	d.Limitation = "This decomposition covers the corpus rendering only: name, description and " +
		"input schema. bench.Tool carries no annotations field, while spec 102 measured the " +
		"production wire payload including mcp-go's annotation defaults. The annotations third " +
		"of its conclusion is therefore OUT OF REACH of any corpus-based decomposition and is " +
		"neither confirmed nor refuted here."

	return d, nil
}

// spec102Verdict states what THIS CORPUS RENDERING shows, and is careful not to
// claim more than that.
//
// Two ways a naive verdict goes wrong here, both found by running the first
// version against the real corpora:
//
//  1. A CORPUS WITH NO SCHEMAS yields a 0% schema share, and a verdict phrased
//     as "deferring schemas cannot exceed 0%" reads as a resounding
//     confirmation of spec 102. It is nothing of the kind — it measures a
//     corpus that does not contain the thing under test. The committed
//     527-tool snapshot is exactly this case: all 527 tools, zero schemas.
//
//  2. IT IS A DIFFERENT PAYLOAD FROM THE ONE SPEC 102 MEASURED. Spec 102
//     measured the production wire payload — mcp.Tool marshalling, the
//     "[server] " prefix, mcp-go's annotation defaults, the raw-schema
//     placeholder. This decomposition covers the corpus rendering, which has no
//     annotations and no wire framing. A confident CONFIRMED/CORRECTED across
//     that gap would be adjudicating a claim on evidence about something else.
//
// So the verdict describes the corpus and explicitly declines to settle spec
// 102's figure. The decomposition is still the useful thing — it says where the
// payload actually goes on a given fleet — it just is not a verdict on a
// measurement taken elsewhere.
func spec102Verdict(d *PayloadDecomposition) string {
	prose := d.NamePct + d.DescriptionPct
	const scope = " This describes the CORPUS RENDERING (name, description, schema). Spec 102's " +
		"29.7% figure was measured on the production WIRE payload, which additionally carries " +
		"annotations and wire framing, so this neither confirms nor corrects it."

	if d.ToolsWithSchema == 0 {
		return fmt.Sprintf("INAPPLICABLE: none of the %d tools in this corpus carries an input "+
			"schema, so there is no schema share to measure and no ceiling to compute. A 0%% "+
			"result here means the corpus lacks the thing under test, NOT that schemas are "+
			"cheap.%s", d.ToolCount, scope)
	}

	coverage := float64(d.ToolsWithSchema) / float64(d.ToolCount) * 100
	partial := ""
	if d.ToolsWithSchema < d.ToolCount {
		partial = fmt.Sprintf(" Note only %d of %d tools (%.0f%%) carry a schema, so the share is "+
			"diluted by the schema-less remainder.", d.ToolsWithSchema, d.ToolCount, coverage)
	}

	switch {
	case d.SchemaPct >= 50:
		return fmt.Sprintf("SCHEMA-DOMINANT on this corpus: schemas are %.1f%% of the payload "+
			"against %.1f%% for names and descriptions, so deferring them could save up to "+
			"%.1f%% here.%s%s", d.SchemaPct, prose, d.SchemaCeilingPct, partial, scope)
	case prose > d.SchemaPct:
		return fmt.Sprintf("PROSE-DOMINANT on this corpus: names and descriptions are %.1f%% "+
			"against %.1f%% for schemas, so deferring schemas cannot exceed %.1f%% here however "+
			"it is implemented.%s%s", prose, d.SchemaPct, d.SchemaCeilingPct, partial, scope)
	default:
		return fmt.Sprintf("EVENLY SPLIT on this corpus: schemas %.1f%%, names plus descriptions "+
			"%.1f%%. Ceiling %.1f%%.%s%s", d.SchemaPct, prose, d.SchemaCeilingPct, partial, scope)
	}
}

// partitionToolDefinition builds the canonical rendering of one tool together
// with a span partition covering it exactly.
//
// The spans must be contiguous and cover [0, len(text)) — AttributeTokens
// enforces that — which is why the separators get their own structural spans
// rather than being folded into a neighbour.
func partitionToolDefinition(tl Tool) (string, []Span, error) {
	var (
		text  string
		spans []Span
	)

	add := func(label, s string) {
		if s == "" {
			return
		}
		start := len(text)
		text += s
		spans = append(spans, Span{Label: label, Start: start, End: len(text)})
	}

	add(PayloadComponentName, tl.Name)
	add(PayloadComponentStructural, "\n")
	add(PayloadComponentDescription, tl.Description)

	if len(tl.Schema) > 0 {
		canon, err := canonicalSchemaJSON(tl.Schema)
		if err != nil {
			return "", nil, err
		}
		add(PayloadComponentStructural, "\n")
		add(PayloadComponentSchema, canon)
	}

	if text == "" {
		return "", nil, fmt.Errorf("tool renders to an empty definition")
	}
	return text, spans, nil
}

// canonicalSchemaJSON matches the canonicalisation the baseline arm applies, so
// the decomposition explains the same payload the savings figures are measured
// against rather than a re-marshalled approximation of it.
//
// bench cannot import bench/arms (arms import bench), so the canonical form is
// reproduced here; the drift risk is bounded because both are "unmarshal into
// interface{}, re-marshal with sorted keys", which encoding/json does by
// construction for maps.
func canonicalSchemaJSON(raw json.RawMessage) (string, error) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("schema is not valid JSON: %w", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("schema could not be re-marshalled: %w", err)
	}
	return string(out), nil
}
