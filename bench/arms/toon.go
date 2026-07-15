package arms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"

	toon "github.com/toon-format/toon-go"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ToonListingName is the registry key of the TOON tool-listing arm.
const ToonListingName = "toon_listing"

// ToonListingArm renders tool listings as TOON (Token-Oriented Object
// Notation, https://github.com/toon-format) via the official toon-go encoder
// (research D1: measured honestly even though the TOON spec itself concedes
// compact JSON often wins on deeply-nested structures like JSON Schema).
//
// Each tool becomes an ordered TOON object {name, description, inputSchema}
// where inputSchema is the tool's JSON input schema re-expressed as TOON text.
// Determinism (FR-010): field order is fixed by explicit toon.Object fields,
// schema object keys are sorted lexicographically by toon-go's normalizer, and
// schema numbers are decoded as json.Number (no float round-trip surprises in
// the literal source).
type ToonListingArm struct{}

// NewToonListing returns the toon_listing arm.
func NewToonListing() *ToonListingArm { return &ToonListingArm{} }

// Name implements Arm.
func (*ToonListingArm) Name() string { return ToonListingName }

// IndexAltering implements Arm: the TOON schema text replaces ParamsJSON in
// the index mapping (see EncodeIndexMetadata), so the arm changes text the
// retrieval index ingests and obligates retrieval-quality scoring (FR-008).
func (*ToonListingArm) IndexAltering() bool { return true }

// LowerBound implements Arm: descriptions are preserved verbatim (TOON quotes
// and escapes multi-line strings; nothing is dropped or truncated).
func (*ToonListingArm) LowerBound() bool { return false }

// decodeSchemaValue parses a tool's raw JSON schema into the Go value shape
// toon-go encodes deterministically: maps (keys sorted by the encoder),
// slices, strings, bools, and json.Number literals. Invalid JSON and trailing
// garbage are explicit errors, never a silently half-parsed schema (contract
// rule 2, mirroring CanonicalJSON's strictness).
func decodeSchemaValue(t bench.Tool) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(t.Schema))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("tool %s: parse schema for TOON: %w", t.ToolID, err)
	}
	if err := bench.RequireEOF(dec); err != nil {
		return nil, fmt.Errorf("tool %s: parse schema for TOON: %w", t.ToolID, err)
	}
	return v, nil
}

// toonToolObject builds the ordered {name, description, inputSchema} object
// for one tool; a schema-less tool omits the inputSchema field entirely.
func toonToolObject(t bench.Tool) (toon.Object, error) {
	fields := []toon.Field{
		{Key: "name", Value: t.Name},
		{Key: "description", Value: t.Description},
	}
	if len(t.Schema) > 0 {
		schema, err := decodeSchemaValue(t)
		if err != nil {
			return toon.Object{}, err
		}
		fields = append(fields, toon.Field{Key: "inputSchema", Value: schema})
	}
	return toon.NewObject(fields...), nil
}

// EncodeTool implements Arm: one tool as a bare TOON object document. The
// listing array header is amortized in EncodeListing, not here (contract
// rule 6).
func (*ToonListingArm) EncodeTool(t bench.Tool) (string, error) {
	obj, err := toonToolObject(t)
	if err != nil {
		return "", err
	}
	s, err := toon.MarshalString(obj)
	if err != nil {
		return "", fmt.Errorf("tool %s: TOON encode: %w", t.ToolID, err)
	}
	return s, nil
}

// EncodeListing implements Arm: the whole listing is a single TOON array
// document, so the shared array header ("[N]:") is paid once for the listing
// rather than per tool.
func (*ToonListingArm) EncodeListing(ts []bench.Tool) (string, error) {
	items := make([]any, 0, len(ts))
	for _, t := range ts {
		obj, err := toonToolObject(t)
		if err != nil {
			return "", err
		}
		items = append(items, obj)
	}
	s, err := toon.MarshalString(items)
	if err != nil {
		return "", fmt.Errorf("TOON encode listing: %w", err)
	}
	return s, nil
}

// EncodeIndexMetadata implements Arm: Name/ServerName/Description are the
// production values unchanged; ParamsJSON is replaced by the TOON-encoded
// schema text — the exact parameter text the retrieval index would ingest
// under this arm (empty when the tool has no schema, matching baseline).
func (*ToonListingArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	params := ""
	if len(t.Schema) > 0 {
		schema, err := decodeSchemaValue(t)
		if err != nil {
			return config.ToolMetadata{}, err
		}
		s, err := toon.MarshalString(schema)
		if err != nil {
			return config.ToolMetadata{}, fmt.Errorf("tool %s: TOON encode schema: %w", t.ToolID, err)
		}
		params = s
	}
	return config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: t.Description,
		ParamsJSON:  params,
	}, nil
}

func init() {
	MustRegister(NewToonListing())
}

// ---------------------------------------------------------------------------
// toon_results — the fixture-driven TOON tool-RESULTS arm (T038, FR-007,
// research D10). Unlike the registry arms above it does not encode tool
// definitions from a corpus: it measures TOON against a compact-JSON baseline
// on deterministic tool-call OUTPUT payloads (result_fixtures_v1.json), split
// tabular vs non-tabular — tabular results are TOON's favorable regime, and
// the split keeps the verdict honest in both directions. It is deliberately
// NOT registered: the tool-corpus contract tests (EncodeTool/EncodeListing/
// EncodeIndexMetadata over corpus_v2) do not apply to result payloads.
// ---------------------------------------------------------------------------

// ToonResultsName is the report key of the fixture-driven TOON results arm.
const ToonResultsName = "toon_results"

// PayloadClassTabular / PayloadClassNonTabular are the fixture classification
// hints (datasets/README.md rule: tabular = a uniform JSON array of flat
// objects; everything else is non_tabular).
const (
	PayloadClassTabular    = "tabular"
	PayloadClassNonTabular = "non_tabular"
)

// ResultFixture is one captured tool-call output payload.
type ResultFixture struct {
	ToolID           string          `json:"tool_id"`
	PayloadClassHint string          `json:"payload_class_hint"`
	Payload          json.RawMessage `json:"payload"`
}

// ResultFixtureSet mirrors datasets/result_fixtures_v1.json: a frozen,
// versioned snapshot of deterministic tool-call outputs (T037).
type ResultFixtureSet struct {
	Captured  string          `json:"captured"`
	FixtureID string          `json:"fixture_id"`
	Results   []ResultFixture `json:"results"`
}

// LoadResultFixtures reads and validates a result-fixture snapshot.
// Validation is strict and per-record (the fixture set is frozen and
// self-describing): missing identity fields, empty/duplicate tool IDs,
// unknown classification hints, invalid payload JSON, and a tabular hint on a
// non-array payload are all explicit errors naming the offending record.
func LoadResultFixtures(path string) (*ResultFixtureSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read result fixtures %q: %w", path, err)
	}
	var fx ResultFixtureSet
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, fmt.Errorf("parse result fixtures %q: %w", path, err)
	}
	if fx.FixtureID == "" {
		return nil, fmt.Errorf("result fixtures %q: missing fixture_id", path)
	}
	if fx.Captured == "" {
		return nil, fmt.Errorf("result fixtures %q: missing captured date", path)
	}
	if len(fx.Results) == 0 {
		return nil, fmt.Errorf("result fixtures %q: contains no results", path)
	}
	seen := make(map[string]bool, len(fx.Results))
	for i, r := range fx.Results {
		if r.ToolID == "" {
			return nil, fmt.Errorf("result fixtures %q: results[%d]: missing tool_id", path, i)
		}
		if seen[r.ToolID] {
			return nil, fmt.Errorf("result fixtures %q: duplicate tool_id %q", path, r.ToolID)
		}
		seen[r.ToolID] = true
		if r.PayloadClassHint != PayloadClassTabular && r.PayloadClassHint != PayloadClassNonTabular {
			return nil, fmt.Errorf("result fixtures %q: tool %s: unknown payload_class_hint %q (want %s|%s)",
				path, r.ToolID, r.PayloadClassHint, PayloadClassTabular, PayloadClassNonTabular)
		}
		if len(r.Payload) == 0 || !json.Valid(r.Payload) {
			return nil, fmt.Errorf("result fixtures %q: tool %s: payload missing or invalid JSON", path, r.ToolID)
		}
		if r.PayloadClassHint == PayloadClassTabular {
			trimmed := bytes.TrimLeft(r.Payload, " \t\r\n")
			if len(trimmed) == 0 || trimmed[0] != '[' {
				return nil, fmt.Errorf("result fixtures %q: tool %s: hinted tabular but payload is not a JSON array", path, r.ToolID)
			}
		}
	}
	return &fx, nil
}

// ToonResultsRun is one toon_results measurement: the two report rows
// (compact-JSON baseline of the payloads, then TOON of the same payloads) and
// the tabular/non-tabular token split behind them. The baseline row makes the
// savings recomputable from report rows alone (FR-004 spirit).
type ToonResultsRun struct {
	// Rows: [0] = compact-JSON baseline row (arm baseline_json, savings 0),
	// [1] = the toon_results row (savings vs [0]).
	Rows []bench.ArmResult `json:"rows"`
	// Per-class token totals: the tabular/non-tabular split of each side's
	// TotalTokens (FR-007 classification split).
	TabularToonTokens        int `json:"tabular_toon_tokens"`
	TabularBaselineTokens    int `json:"tabular_baseline_tokens"`
	NonTabularToonTokens     int `json:"non_tabular_toon_tokens"`
	NonTabularBaselineTokens int `json:"non_tabular_baseline_tokens"`
}

// TabularSavingsPct is the TOON savings vs compact JSON on tabular payloads
// only (0 when no tabular payload was measured).
func (r *ToonResultsRun) TabularSavingsPct() float64 {
	return savingsPct(r.TabularToonTokens, r.TabularBaselineTokens)
}

// NonTabularSavingsPct is the TOON savings vs compact JSON on non-tabular
// payloads only (0 when none was measured).
func (r *ToonResultsRun) NonTabularSavingsPct() float64 {
	return savingsPct(r.NonTabularToonTokens, r.NonTabularBaselineTokens)
}

func savingsPct(tokens, baseline int) float64 {
	if baseline <= 0 {
		return 0
	}
	return (1 - float64(tokens)/float64(baseline)) * 100
}

// RunToonResults measures the fixture payloads under both encodings and
// assembles the two results-class report rows. Determinism (FR-010): payloads
// are processed in fixture-file order (the file is sorted by tool_id at
// capture), the baseline is CanonicalJSON (sorted keys, compact), and TOON
// object keys are sorted by toon-go's normalizer.
func RunToonResults(tk *bench.Tokenizer, fx *ResultFixtureSet) (*ToonResultsRun, error) {
	run := &ToonResultsRun{}
	tabular, nonTabular := 0, 0
	basePerTool := make([]bench.ToolTokenEntry, 0, len(fx.Results))
	toonPerTool := make([]bench.ToolTokenEntry, 0, len(fx.Results))

	for _, r := range fx.Results {
		baseText, err := CanonicalJSON(r.Payload)
		if err != nil {
			return nil, fmt.Errorf("tool %s: compact-JSON baseline: %w", r.ToolID, err)
		}
		value, err := decodePayloadValue(r)
		if err != nil {
			return nil, err
		}
		toonText, err := toon.MarshalString(value)
		if err != nil {
			return nil, fmt.Errorf("tool %s: TOON encode result payload: %w", r.ToolID, err)
		}

		baseTokens := tk.Count(baseText)
		toonTokens := tk.Count(toonText)
		basePerTool = append(basePerTool, bench.ToolTokenEntry{ToolID: r.ToolID, Tokens: baseTokens})
		toonPerTool = append(toonPerTool, bench.ToolTokenEntry{ToolID: r.ToolID, Tokens: toonTokens})

		if r.PayloadClassHint == PayloadClassTabular {
			tabular++
			run.TabularBaselineTokens += baseTokens
			run.TabularToonTokens += toonTokens
		} else {
			nonTabular++
			run.NonTabularBaselineTokens += baseTokens
			run.NonTabularToonTokens += toonTokens
		}
	}

	baseTotal := run.TabularBaselineTokens + run.NonTabularBaselineTokens
	toonTotal := run.TabularToonTokens + run.NonTabularToonTokens

	baseRow := resultsRow(BaselineName, fx, tabular, nonTabular, baseTotal, basePerTool)
	toonRow := resultsRow(ToonResultsName, fx, tabular, nonTabular, toonTotal, toonPerTool)
	toonRow.SavingsVsBaselinePct = savingsPct(toonTotal, baseTotal)
	run.Rows = []bench.ArmResult{baseRow, toonRow}
	return run, nil
}

// decodePayloadValue parses a fixture payload into the Go value shape toon-go
// encodes deterministically (json.Number literals, no float round-trips) —
// the same strictness as decodeSchemaValue.
func decodePayloadValue(r ResultFixture) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(r.Payload))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("tool %s: parse result payload: %w", r.ToolID, err)
	}
	if err := bench.RequireEOF(dec); err != nil {
		return nil, fmt.Errorf("tool %s: parse result payload: %w", r.ToolID, err)
	}
	return v, nil
}

// resultsRow assembles one results-class ArmResult over the fixture set.
func resultsRow(armName string, fx *ResultFixtureSet, tabular, nonTabular, total int, perTool []bench.ToolTokenEntry) bench.ArmResult {
	tab, nontab := tabular, nonTabular
	row := bench.ArmResult{
		Arm:             armName,
		CorpusID:        fx.FixtureID,
		PayloadClass:    "results",
		FixtureID:       fx.FixtureID + "@" + fx.Captured,
		TabularCount:    &tab,
		NonTabularCount: &nontab,
		TotalTokens:     total,
	}
	if len(perTool) == 0 {
		return row
	}
	sum := 0
	tokens := make([]int, len(perTool))
	for i, e := range perTool {
		sum += e.Tokens
		tokens[i] = e.Tokens
	}
	row.MeanTokens = float64(sum) / float64(len(perTool))
	sort.Ints(tokens)
	row.P95Tokens = tokens[nearestRankIndex(len(tokens), 95)]
	row.HeaviestTools = heaviestResultTools(perTool)
	return row
}

// nearestRankIndex returns the 0-based nearest-rank percentile index for a
// sorted slice of length n — the same rank rule as bench's percentiles.
func nearestRankIndex(n int, p float64) int {
	rank := int(math.Ceil(p / 100.0 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return rank - 1
}

// heaviestResultTools sorts per-payload entries by token count descending,
// ties broken by tool_id ascending (deterministic, FR-010). The fixture set
// is small, so all entries are kept.
func heaviestResultTools(perTool []bench.ToolTokenEntry) []bench.ToolTokenEntry {
	sorted := make([]bench.ToolTokenEntry, len(perTool))
	copy(sorted, perTool)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Tokens != sorted[j].Tokens {
			return sorted[i].Tokens > sorted[j].Tokens
		}
		return sorted[i].ToolID < sorted[j].ToolID
	})
	return sorted
}
