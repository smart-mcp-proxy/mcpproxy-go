package bench

import (
	"encoding/json"
	"strings"
	"testing"
)

// decompCorpus is a small schema-bearing corpus. The schemas are deliberately
// larger than the descriptions on one tool and smaller on another, so a test
// that only ever sees one shape dominating cannot pass by accident.
func decompCorpus() *Corpus {
	return &Corpus{
		Version: "decomp_test@1",
		Tools: []Tool{
			{
				ToolID:      "srv:big_schema",
				Server:      "srv",
				Name:        "big_schema",
				Description: "Short.",
				Schema: json.RawMessage(`{"type":"object","properties":{` +
					`"alpha":{"type":"string","description":"the first parameter of several"},` +
					`"beta":{"type":"integer","description":"the second parameter of several"},` +
					`"gamma":{"type":"boolean","description":"the third parameter of several"}},` +
					`"required":["alpha","beta"]}`),
			},
			{
				ToolID: "srv:big_description",
				Server: "srv",
				Name:   "big_description",
				Description: "A deliberately long description that runs well past the length of " +
					"this tool's input schema, so the decomposition has at least one tool where " +
					"prose rather than structure dominates the payload.",
				Schema: json.RawMessage(`{"type":"object"}`),
			},
			{
				ToolID:      "srv:no_schema",
				Server:      "srv",
				Name:        "no_schema",
				Description: "Carries no schema at all.",
			},
		},
	}
}

// T050 — the shares must PARTITION the payload, not merely approximate it.
//
// This is the whole reason the decomposition attributes token spans over a
// single tokenization instead of counting each component separately. BPE merges
// across boundaries, so Count(name) + Count(description) + Count(schema) does
// NOT equal Count(name + description + schema). Independent counts would
// produce shares that look plausible, never sum, and quietly misattribute the
// bytes at every component boundary.
func TestPayloadDecomposition_SharesPartitionThePayload(t *testing.T) {
	tk := runnerTokenizer(t)

	d, err := DecomposePayload(tk, decompCorpus(), "decomp_test@1")
	if err != nil {
		t.Fatalf("DecomposePayload: %v", err)
	}

	sum := d.NameTokens + d.DescriptionTokens + d.SchemaTokens + d.StructuralTokens
	if sum != d.TotalTokens {
		t.Errorf("shares must sum to the payload exactly: %d + %d + %d + %d = %d, want %d",
			d.NameTokens, d.DescriptionTokens, d.SchemaTokens, d.StructuralTokens, sum, d.TotalTokens)
	}
	if d.TotalTokens == 0 {
		t.Fatal("a non-empty corpus must have a non-zero payload")
	}
	for _, c := range []struct {
		label string
		n     int
	}{
		{"name", d.NameTokens},
		{"description", d.DescriptionTokens},
		{"schema", d.SchemaTokens},
	} {
		if c.n <= 0 {
			t.Errorf("%s share is %d; this corpus has all three, so a zero means the "+
				"attribution collapsed rather than measured", c.label, c.n)
		}
	}
}

// T051 — the ceiling is a property of the corpus and must be recomputed for
// each one.
//
// Carrying a ceiling forward is precisely the error spec 102 made: it projected
// ~70% from an assumption about one corpus shape and measured 29.7%. A
// decomposition that reused a previous corpus's ceiling would repeat it while
// looking like a measurement.
func TestPayloadDecomposition_CeilingIsPerCorpus(t *testing.T) {
	tk := runnerTokenizer(t)

	schemaHeavy := decompCorpus()
	proseHeavy := &Corpus{
		Version: "prose@1",
		Tools: []Tool{{
			ToolID: "srv:prose", Server: "srv", Name: "prose",
			Description: strings.Repeat("a great deal of description text and nothing else. ", 40),
			Schema:      json.RawMessage(`{"type":"object"}`),
		}},
	}

	a, err := DecomposePayload(tk, schemaHeavy, "a@1")
	if err != nil {
		t.Fatalf("DecomposePayload(schemaHeavy): %v", err)
	}
	b, err := DecomposePayload(tk, proseHeavy, "b@1")
	if err != nil {
		t.Fatalf("DecomposePayload(proseHeavy): %v", err)
	}

	if a.SchemaCeilingPct == b.SchemaCeilingPct {
		t.Errorf("two corpora with opposite shapes produced an identical ceiling (%.2f%%); "+
			"the ceiling must be recomputed per corpus, never carried forward", a.SchemaCeilingPct)
	}
	if b.SchemaCeilingPct >= a.SchemaCeilingPct {
		t.Errorf("the prose-heavy corpus must have a LOWER schema ceiling than the "+
			"schema-heavy one: prose=%.2f%% schema=%.2f%%", b.SchemaCeilingPct, a.SchemaCeilingPct)
	}
	if b.SchemaCeilingPct < 0 || a.SchemaCeilingPct > 100 {
		t.Errorf("ceiling out of range: a=%.2f b=%.2f", a.SchemaCeilingPct, b.SchemaCeilingPct)
	}
}

// The block must state what this corpus shape CANNOT settle.
//
// Spec 102 concluded that names, descriptions AND ANNOTATIONS dominate the
// payload. bench.Tool carries no annotations field, so a corpus-based
// decomposition cannot confirm or refute the annotations third of that claim.
// Reporting a verdict without saying so would look like a confirmation and
// would not be one.
func TestPayloadDecomposition_DeclaresTheAnnotationsLimit(t *testing.T) {
	tk := runnerTokenizer(t)

	d, err := DecomposePayload(tk, decompCorpus(), "decomp_test@1")
	if err != nil {
		t.Fatalf("DecomposePayload: %v", err)
	}
	if !strings.Contains(strings.ToLower(d.Limitation), "annotation") {
		t.Errorf("the decomposition must state that annotations are absent from the corpus "+
			"shape and therefore out of its reach; got %q", d.Limitation)
	}
	if d.Spec102Verdict == "" {
		t.Error("the block must carry an explicit spec-102 verdict, not leave it implied")
	}
}

// An empty corpus is an error, not a zero-filled decomposition.
//
// Zeros would render as "measured, and everything costs nothing", which is the
// same silent-zero failure the exclusion accounting exists to prevent.
func TestPayloadDecomposition_EmptyCorpusIsAnError(t *testing.T) {
	tk := runnerTokenizer(t)

	if _, err := DecomposePayload(tk, &Corpus{Version: "empty@1"}, "empty@1"); err == nil {
		t.Error("an empty corpus must be an error, never a zero-filled decomposition")
	}
}

// A corpus with no schemas cannot measure a schema share, and must say so
// rather than reporting a confident 0%.
//
// Found by running the first version against the committed 527-tool snapshot:
// all 527 tools, zero schemas. The verdict read "deferring schemas cannot
// exceed 0.0% however it is implemented" — which sounds like a decisive result
// and is really a corpus that does not contain the thing under test.
func TestPayloadDecomposition_SchemalessCorpusIsInapplicable(t *testing.T) {
	tk := runnerTokenizer(t)

	schemaless := &Corpus{
		Version: "schemaless@1",
		Tools: []Tool{
			{ToolID: "s:a", Server: "s", Name: "a", Description: "no schema here"},
			{ToolID: "s:b", Server: "s", Name: "b", Description: "nor here"},
		},
	}

	d, err := DecomposePayload(tk, schemaless, "schemaless@1")
	if err != nil {
		t.Fatalf("DecomposePayload: %v", err)
	}
	if d.ToolsWithSchema != 0 {
		t.Fatalf("fixture should carry no schemas, got %d", d.ToolsWithSchema)
	}
	if !strings.Contains(d.Spec102Verdict, "INAPPLICABLE") {
		t.Errorf("a schema-less corpus must yield an INAPPLICABLE verdict, not a confident "+
			"0%% result; got %q", d.Spec102Verdict)
	}
}

// The verdict must never claim to confirm or correct spec 102.
//
// Spec 102 measured the production wire payload — annotations and framing
// included. This decomposition covers the corpus rendering. Adjudicating that
// claim across the gap would be answering with evidence about something else,
// which is precisely what the Limitation field warns of; the verdict has to
// respect its own caveat.
func TestPayloadDecomposition_VerdictDeclinesToSettleSpec102(t *testing.T) {
	tk := runnerTokenizer(t)

	d, err := DecomposePayload(tk, decompCorpus(), "decomp_test@1")
	if err != nil {
		t.Fatalf("DecomposePayload: %v", err)
	}
	v := d.Spec102Verdict
	for _, forbidden := range []string{"CONFIRMED", "CORRECTED"} {
		if strings.Contains(v, forbidden) {
			t.Errorf("the verdict must not claim to %s spec 102 from corpus evidence — it is a "+
				"different payload; got %q", forbidden, v)
		}
	}
	if !strings.Contains(v, "WIRE payload") {
		t.Errorf("the verdict must name the payload gap it is declining to cross; got %q", v)
	}
}

// T053 — the block must carry a populated accounting source, and must report
// the annotation share as NULL rather than zero.
//
// Zero would claim a measurement that was never taken. The corpora carry no
// annotations field at all, so "not measured" is the only truthful value, and
// the difference between null and 0 is the difference between an honest gap and
// a fabricated finding.
func TestPayloadDecompositionBlock_AnnotationShareIsNullNotZero(t *testing.T) {
	tk := runnerTokenizer(t)

	a, err := DecomposePayload(tk, decompCorpus(), "a@1")
	if err != nil {
		t.Fatalf("DecomposePayload(a): %v", err)
	}
	b, err := DecomposePayload(tk, decompCorpus(), "b@1")
	if err != nil {
		t.Fatalf("DecomposePayload(b): %v", err)
	}

	block, err := PayloadDecompositionBlockFor([]*PayloadDecomposition{a, b})
	if err != nil {
		t.Fatalf("PayloadDecompositionBlockFor: %v", err)
	}
	if block.AccountingSource.IsZero() {
		t.Error("the block must name a populated accounting source")
	}
	for i, row := range block.Shapes {
		if row.ShareAnnotationsPct != nil {
			t.Errorf("shape %d: the annotation share must be null (not measurable from a "+
				"corpus that carries no annotations), got %v", i, *row.ShareAnnotationsPct)
		}
		if row.Provenance != ProvenanceMeasured {
			t.Errorf("shape %d: provenance %q", i, row.Provenance)
		}
	}
}

// FR-024 wants at least two shapes, so a single-corpus block is an error.
//
// One shape is exactly the evidence base spec 102 projected from, and the point
// of the requirement is to make that shape of claim impossible to emit.
func TestPayloadDecompositionBlock_RequiresTwoShapes(t *testing.T) {
	tk := runnerTokenizer(t)

	d, err := DecomposePayload(tk, decompCorpus(), "only@1")
	if err != nil {
		t.Fatalf("DecomposePayload: %v", err)
	}
	if _, err := PayloadDecompositionBlockFor([]*PayloadDecomposition{d}); err == nil {
		t.Error("a single-shape decomposition block must be an error — one corpus is what " +
			"spec 102 projected from")
	}
}
