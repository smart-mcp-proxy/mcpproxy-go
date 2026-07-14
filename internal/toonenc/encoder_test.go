package toonenc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	toon "github.com/toon-format/toon-go"
)

const defaultThreshold = 15

// bigTable returns a uniform tabular JSON fixture large enough that TOON wins
// comfortably even after paying the marker's byte cost.
func bigTable(rows int) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < rows; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%d,"name":"user-%d","email":"user-%d@example.com","active":%t,"score":%d}`,
			i, i, i, i%2 == 0, i*7)
	}
	sb.WriteString("]")
	return sb.String()
}

// --- Determinism (FR-011) ---

func TestEncodeBlockDeterministic(t *testing.T) {
	fixtures := []string{
		bigTable(50),
		`{"rows":` + bigTable(20) + `}`,
		`{"nested":{"deep":{"deeper":[1,2,3]}},"z":"last","a":"first"}`,
		`not json at all`,
	}
	for _, mode := range []Mode{ModeAdaptive, ModeAlways} {
		for i, text := range fixtures {
			out1, d1 := EncodeBlock(text, mode, defaultThreshold, 0)
			out2, d2 := EncodeBlock(text, mode, defaultThreshold, 0)
			if out1 != out2 {
				t.Fatalf("mode %s fixture %d: output not deterministic", mode, i)
			}
			if !reflect.DeepEqual(d1, d2) {
				t.Fatalf("mode %s fixture %d: decision not deterministic: %+v vs %+v", mode, i, d1, d2)
			}
		}
	}
}

// marshalShuffled serializes v as JSON with object keys in an order drawn from
// r — used to prove the encoder's output does not depend on source key order.
func marshalShuffled(r *rand.Rand, v interface{}) string {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			kb, _ := json.Marshal(k)
			parts = append(parts, string(kb)+":"+marshalShuffled(r, val[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []interface{}:
		parts := make([]string, 0, len(val))
		for _, e := range val {
			parts = append(parts, marshalShuffled(r, e))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		b, err := json.Marshal(val)
		if err != nil {
			panic(err)
		}
		return string(b)
	}
}

// TestEncodeBlockRandomizedKeyOrderDeterministic (finding 5): N JSON
// serializations of the same value with shuffled object-key order must all
// produce byte-identical encoded output — canonicalToon, not map or source
// order, fixes the bytes.
func TestEncodeBlockRandomizedKeyOrderDeterministic(t *testing.T) {
	tabular := make([]interface{}, 0, 30)
	for i := 0; i < 30; i++ {
		tabular = append(tabular, map[string]interface{}{
			"id": i, "name": fmt.Sprintf("user-%d", i), "email": fmt.Sprintf("u%d@example.com", i),
			"active": i%2 == 0, "score": i * 3,
		})
	}
	nested := map[string]interface{}{
		"meta":  map[string]interface{}{"z": 1, "a": 2, "m": map[string]interface{}{"y": true, "b": nil}},
		"items": []interface{}{map[string]interface{}{"k2": "v", "k1": 1}},
		"tag":   "x",
	}
	cases := []struct {
		name  string
		mode  Mode
		value interface{}
	}{
		{"adaptive tabular", ModeAdaptive, tabular},
		{"always nested", ModeAlways, nested},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic test shuffling
			var first string
			var firstDecision Decision
			for n := 0; n < 20; n++ {
				text := marshalShuffled(r, tc.value)
				out, d := EncodeBlock(text, tc.mode, defaultThreshold, 0)
				if d.Outcome != OutcomeEncoded {
					t.Fatalf("iteration %d: expected encoded, got %+v", n, d)
				}
				if n == 0 {
					first, firstDecision = out, d
					continue
				}
				if out != first {
					t.Fatalf("iteration %d: shuffled-key input changed encoded bytes:\n%q\nvs\n%q", n, out, first)
				}
				// Sizes of the encoded emission must match too (the passthrough
				// size may differ because the source text differs, so compare
				// only the encoded side).
				if d.EncodedEmissionBytes != firstDecision.EncodedEmissionBytes {
					t.Fatalf("iteration %d: encoded size changed: %d vs %d", n,
						d.EncodedEmissionBytes, firstDecision.EncodedEmissionBytes)
				}
			}
		})
	}
}

// --- Never-larger property (FR-004 / SC-003) ---

func TestEncodeBlockNeverLargerAdaptive(t *testing.T) {
	corpus := []string{
		bigTable(4),
		bigTable(5),
		bigTable(10),
		bigTable(100),
		bigTable(250),
		`{"rows":` + bigTable(40) + `}`,
		`[]`,
		`[{"a":1},{"a":2},{"a":3}]`,
		`[1,2,3,4,5]`,
		`{"deep":{"nested":{"x":[{"a":1},{"a":2}]}}}`,
		`42`,
		`"a plain string"`,
		`true`,
		`null`,
		`plain text, not JSON`,
		`[{"a":1,"b":2},{"a":3},{"b":4},{"a":5,"b":6}]`, // ragged
		`[{"a":"x"},{"a":"y"},{"a":"z"},{"a":"w"}]`,     // tiny table: marker overhead loses
		``,
	}
	for i, text := range corpus {
		out, d := EncodeBlock(text, ModeAdaptive, defaultThreshold, 0)
		if len(out) > len(text) {
			t.Fatalf("fixture %d: adaptive output larger than passthrough (%d > %d), decision %+v",
				i, len(out), len(text), d)
		}
		if d.Outcome != OutcomeEncoded && out != text {
			t.Fatalf("fixture %d: passthrough outcome %q must return the input byte-identically", i, d.Outcome)
		}
	}
}

// --- Threshold boundary (FR-003c) ---

func TestEncodeBlockThresholdBoundary(t *testing.T) {
	text := bigTable(60)
	// Establish the actual sizes with a minimal threshold.
	out, d := EncodeBlock(text, ModeAdaptive, 1, 0)
	if d.Outcome != OutcomeEncoded {
		t.Fatalf("fixture must encode at threshold 1, got %+v", d)
	}
	encBytes := len(out)
	passBytes := len(text)
	if d.EncodedEmissionBytes != encBytes || d.PassthroughEmissionBytes != passBytes {
		t.Fatalf("decision sizes must match emissions: %+v (enc %d, pass %d)", d, encBytes, passBytes)
	}

	// Largest threshold t (<=90) still satisfying encBytes <= passBytes*(100-t)/100.
	maxT := 0
	for tPct := 1; tPct <= 90; tPct++ {
		if encBytes <= passBytes*(100-tPct)/100 {
			maxT = tPct
		}
	}
	if maxT < 2 || maxT >= 90 {
		t.Fatalf("fixture unsuitable for boundary test: maxT=%d (enc %d, pass %d)", maxT, encBytes, passBytes)
	}

	// At the boundary: encoded.
	outAt, dAt := EncodeBlock(text, ModeAdaptive, maxT, 0)
	if dAt.Outcome != OutcomeEncoded || outAt != out {
		t.Fatalf("threshold %d (boundary) must encode, got %+v", maxT, dAt)
	}
	// One past the boundary: passthrough-below-threshold, byte-identical input.
	outPast, dPast := EncodeBlock(text, ModeAdaptive, maxT+1, 0)
	if dPast.Outcome != OutcomePassthroughBelowThreshold {
		t.Fatalf("threshold %d must be below-threshold passthrough, got %+v", maxT+1, dPast)
	}
	if outPast != text {
		t.Fatalf("below-threshold passthrough must be byte-identical to input")
	}
	if dPast.EncodedEmissionBytes != 0 {
		t.Fatalf("EncodedEmissionBytes must be 0 on passthrough, got %d", dPast.EncodedEmissionBytes)
	}
	if dPast.ThresholdPct != maxT+1 {
		t.Fatalf("ThresholdPct must record the threshold in effect, got %+v", dPast)
	}
}

// --- Adaptive rejections (FR-003) ---

func TestEncodeBlockAdaptiveNotTabular(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		reason NotTabularReason
	}{
		{"nested object", `{"a":{"b":1},"c":2}`, ReasonNotArray},
		{"scalar", `42`, ReasonNotArray},
		{"short array", `[{"a":1},{"a":2},{"a":3}]`, ReasonTooFewRows},
		{"non-object array", `[1,2,3,4]`, ReasonNonObjectElements},
		{"nested values", `[{"a":[1]},{"a":[2]},{"a":[3]},{"a":[4]}]`, ReasonNestedValues},
		{"non-JSON", `hello world`, ReasonNotJSON},
		{"empty", ``, ReasonNotJSON},
		{"trailing garbage", bigTable(10) + ` tail`, ReasonNotJSON},
		{"two JSON values", `{"a":1} {"b":2}`, ReasonNotJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, d := EncodeBlock(tt.text, ModeAdaptive, defaultThreshold, 0)
			if out != tt.text {
				t.Fatalf("passthrough must be byte-identical")
			}
			if d.Outcome != OutcomePassthroughNotTabular {
				t.Fatalf("expected passthrough-not-tabular, got %+v", d)
			}
			if d.Classification.Reason != tt.reason {
				t.Fatalf("expected reason %q, got %+v", tt.reason, d)
			}
			if strings.Contains(out, Marker) {
				t.Fatalf("passthrough must carry no marker")
			}
		})
	}
}

// --- Marker + round-trip (FR-005) ---

func normalizeNums(v interface{}) interface{} {
	switch val := v.(type) {
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			panic(err)
		}
		return f
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, e := range val {
			out[k] = normalizeNums(e)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, e := range val {
			out[i] = normalizeNums(e)
		}
		return out
	default:
		return v
	}
}

func TestEncodeBlockMarkerAndRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		mode Mode
		text string
	}{
		{"adaptive tabular", ModeAdaptive, bigTable(30)},
		{"adaptive envelope", ModeAdaptive, `{"items":` + bigTable(30) + `}`},
		{"always nested", ModeAlways, `{"z":{"y":1},"a":[1,2,{"k":"v"}],"s":"text"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, d := EncodeBlock(tc.text, tc.mode, defaultThreshold, 0)
			if d.Outcome != OutcomeEncoded {
				t.Fatalf("expected encoded, got %+v", d)
			}
			if !strings.HasPrefix(out, Marker+"\n") {
				t.Fatalf("encoded emission must start with Marker+\\n, got %q", out[:min(len(out), 160)])
			}
			body := strings.TrimPrefix(out, Marker+"\n")
			decoded, err := toon.DecodeString(body)
			if err != nil {
				t.Fatalf("TOON body must decode: %v\n%s", err, body)
			}
			var orig interface{}
			dec := json.NewDecoder(strings.NewReader(tc.text))
			dec.UseNumber()
			if err := dec.Decode(&orig); err != nil {
				t.Fatalf("fixture parse: %v", err)
			}
			if !reflect.DeepEqual(normalizeNums(orig), normalizeNums(decoded)) {
				t.Fatalf("decoded TOON does not match original value\norig: %#v\ndec:  %#v",
					normalizeNums(orig), normalizeNums(decoded))
			}
		})
	}
}

func TestEncodeBlockEnvelopeClassification(t *testing.T) {
	_, d := EncodeBlock(`{"items":`+bigTable(30)+`}`, ModeAdaptive, defaultThreshold, 0)
	if d.Outcome != OutcomeEncoded || !d.Classification.Envelope || !d.Classification.Tabular {
		t.Fatalf("envelope fixture must encode with Envelope classification, got %+v", d)
	}
}

// --- Always mode (FR-009): any JSON value, size gate bypassed ---

func TestEncodeBlockAlwaysMode(t *testing.T) {
	encodedCases := []struct {
		name string
		text string
	}{
		{"nested object", `{"a":{"b":{"c":1}},"list":[{"x":1},{"y":2}]}`},
		{"scalar number", `42`},
		{"scalar bool", `true`},
		{"scalar string", `"hello"`},
		{"tabular below adaptive threshold", `[{"a":"x"},{"a":"y"},{"a":"z"},{"a":"w"}]`},
	}
	for _, tt := range encodedCases {
		t.Run(tt.name, func(t *testing.T) {
			out, d := EncodeBlock(tt.text, ModeAlways, defaultThreshold, 0)
			if d.Outcome != OutcomeEncoded {
				t.Fatalf("always mode must encode any JSON value, got %+v", d)
			}
			if !strings.HasPrefix(out, Marker+"\n") {
				t.Fatalf("encoded emission must carry the marker")
			}
			if d.EncodedEmissionBytes != len(out) {
				t.Fatalf("EncodedEmissionBytes must equal len(out): %d vs %d", d.EncodedEmissionBytes, len(out))
			}
		})
	}

	t.Run("non-JSON passthrough no marker", func(t *testing.T) {
		text := "plain text output"
		out, d := EncodeBlock(text, ModeAlways, defaultThreshold, 0)
		if out != text || d.Outcome != OutcomePassthroughNotTabular || d.Classification.Reason != ReasonNotJSON {
			t.Fatalf("always mode must pass non-JSON through unmarked, got %+v out=%q", d, out)
		}
	})
}

// --- Too-small-budget guard (FR-008/FR-009, finding 2) ---

func TestEncodeBlockTooSmallBudgetGuard(t *testing.T) {
	text := bigTable(60)
	guard := len(Marker) + 1 + MinToonRowBytes
	for _, mode := range []Mode{ModeAdaptive, ModeAlways} {
		t.Run(string(mode), func(t *testing.T) {
			// Just below the guard: passthrough-below-threshold.
			out, d := EncodeBlock(text, mode, defaultThreshold, guard-1)
			if d.Outcome != OutcomePassthroughBelowThreshold || out != text {
				t.Fatalf("budget %d must pass through (below-threshold), got %+v", guard-1, d)
			}
			// Exactly at the guard: encode proceeds.
			_, d = EncodeBlock(text, mode, defaultThreshold, guard)
			if d.Outcome != OutcomeEncoded {
				t.Fatalf("budget %d must allow encoding, got %+v", guard, d)
			}
			// Zero: unlimited, guard never fires.
			_, d = EncodeBlock(text, mode, defaultThreshold, 0)
			if d.Outcome != OutcomeEncoded {
				t.Fatalf("budget 0 (unlimited) must allow encoding, got %+v", d)
			}
		})
	}
}

// --- Encoder failure → passthrough-error (FR-006, G4) ---

func TestEncodeBlockEncoderErrorPassthrough(t *testing.T) {
	orig := marshalToon
	marshalToon = func(interface{}) (string, error) {
		return "", errors.New("injected toon failure")
	}
	defer func() { marshalToon = orig }()

	text := bigTable(20)
	for _, mode := range []Mode{ModeAdaptive, ModeAlways} {
		out, d := EncodeBlock(text, mode, defaultThreshold, 0)
		if out != text {
			t.Fatalf("mode %s: encoder failure must return the input byte-identically", mode)
		}
		if d.Outcome != OutcomePassthroughError {
			t.Fatalf("mode %s: expected passthrough-error, got %+v", mode, d)
		}
	}
}

// --- Decision bookkeeping ---

func TestEncodeBlockDecisionFields(t *testing.T) {
	text := bigTable(50)
	out, d := EncodeBlock(text, ModeAdaptive, defaultThreshold, 0)
	if d.Mode != ModeAdaptive || d.ThresholdPct != defaultThreshold {
		t.Fatalf("decision must record mode and threshold: %+v", d)
	}
	if d.PassthroughEmissionBytes != len(text) {
		t.Fatalf("PassthroughEmissionBytes must be len(text): %+v", d)
	}
	if d.Outcome != OutcomeEncoded || d.EncodedEmissionBytes != len(out) {
		t.Fatalf("EncodedEmissionBytes must be the complete emission size: %+v (out %d)", d, len(out))
	}
	if !d.Classification.Tabular || d.Classification.Rows != 50 || d.Classification.Cols != 5 {
		t.Fatalf("decision must carry the classification: %+v", d)
	}
	// Threshold semantics account for the COMPLETE emission incl. marker.
	if d.EncodedEmissionBytes > d.PassthroughEmissionBytes*(100-d.ThresholdPct)/100 {
		t.Fatalf("encoded emission violates the threshold inequality: %+v", d)
	}
}
