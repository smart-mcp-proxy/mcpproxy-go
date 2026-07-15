package toonenc

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	toon "github.com/toon-format/toon-go"
)

// MinToonRowBytes is a small fixed estimate of one TOON data row, used by the
// too-small-budget guard (FR-008/FR-009): when the truncator would retain
// fewer bytes than marker + newline + one data row, encoding is pointless —
// the block passes through and truncation behaves exactly as today. Fixed
// constant → determinism preserved (FR-011).
const MinToonRowBytes = 16

// marshalToon is the TOON marshal seam. A package variable (not a direct
// call) so tests can inject a genuine encoder failure to exercise the
// passthrough-error path (FR-006) — toon-go accepts every value reachable
// from parsed JSON, so the failure is otherwise unreachable.
var marshalToon = func(v interface{}) (string, error) {
	return toon.MarshalString(v)
}

// EncodeBlock is the single deterministic entry point called by
// internal/server (production seam) and designed to be imported by the
// spec-083 bench adaptive-results arm (FR-012; lands after PR #851 merges
// and this branch rebases). It decides, for one tool-result text block,
// whether to emit TOON
// or pass the block through unchanged, and returns a full Decision record.
//
// Inputs (contracts/encoder-decision.md):
//   - text: the exact text-block content the agent would receive with the
//     feature off (the passthrough emission, FR-003c), already sanitised;
//   - mode: ModeAdaptive or ModeAlways — the caller never invokes EncodeBlock
//     when the resolved mode is ModeOff;
//   - minSavingsPct: toon_min_savings_pct in effect (validated 1-90);
//     ignored in ModeAlways;
//   - retainedBudget: the truncator's actual retained-prefix budget in bytes
//     (Truncator.SimpleTruncateBudget()); 0 means unlimited.
//
// EncodeBlock is a pure function: no logging, no metrics, no I/O. FR-006
// observability for OutcomePassthroughError is the caller's responsibility.
//
// Guarantees:
//   - G1 byte-identity: every non-encoded outcome returns out == text.
//   - G2 never-larger (adaptive): encoded ⇒ len(out) <= len(text).
//   - G3 determinism: identical input ⇒ identical out and Decision.
//   - G4 no data loss: any failure ⇒ passthrough, never an error return.
func EncodeBlock(text string, mode Mode, minSavingsPct int, retainedBudget int) (string, Decision) {
	d := Decision{
		Mode:                     mode,
		PassthroughEmissionBytes: len(text),
		ThresholdPct:             minSavingsPct,
	}

	// 1. PARSE as a single JSON value (json.Number for determinism — no
	// float round-trip). Trailing non-whitespace disqualifies. Non-JSON
	// never qualifies, in any mode (edge case: plain text/base64/binary).
	v, ok := parseSingleJSON(text)
	if !ok {
		d.Outcome = OutcomePassthroughNotTabular
		d.Classification = Classification{Reason: ReasonNotJSON}
		return text, d
	}

	// 1b. NUMBER FIDELITY GATE (FR-004/FR-006 — every mode): toon-go
	// normalizes json.Number through float64, so any literal that does not
	// survive that round-trip byte-identically (integers beyond 2^53, large
	// uint64, exotic notations) would be silently corrupted. No data loss:
	// pass through. Not an encoder fault — passthrough-not-tabular family,
	// never logged.
	if !numbersRoundtrip(v) {
		d.Outcome = OutcomePassthroughNotTabular
		d.Classification = Classification{Reason: ReasonNonRoundtrippableNumber}
		return text, d
	}

	// 2. CLASSIFY. Always computed (informational in always mode, FR-009 —
	// no tabular gate there); gates encoding in adaptive mode.
	d.Classification = Classify(v)
	if mode == ModeAdaptive && !d.Classification.Tabular {
		d.Outcome = OutcomePassthroughNotTabular
		return text, d
	}

	// 3. ORDER + ENCODE (FR-011): canonicalToon fixes the byte order before
	// toon-go ever sees a Go map. A marshal error is the one genuine encoder
	// failure — passthrough, and the caller logs + counts it (FR-006).
	toonBody, err := marshalToon(canonicalToon(v))
	if err != nil {
		d.Outcome = OutcomePassthroughError
		return text, d
	}

	// 4. ASSEMBLE + MEASURE the complete emissions (FR-003c: marker + hint +
	// body vs the exact passthrough emission).
	emission := AssembleEmission(toonBody)
	encBytes := len(emission)
	passBytes := len(text)

	// 5. TOO-SMALL-BUDGET GUARD (FR-008/FR-009 — precedence in every mode):
	// if the truncator would keep fewer bytes than marker + newline + one
	// data row, encoding could not survive truncation usefully.
	if retainedBudget > 0 && retainedBudget < len(Marker)+1+MinToonRowBytes {
		d.Outcome = OutcomePassthroughBelowThreshold
		return text, d
	}

	// 6. MODE GATE.
	if mode == ModeAlways {
		d.Outcome = OutcomeEncoded
		d.EncodedEmissionBytes = encBytes
		return emission, d
	}
	// Adaptive: emit only when the complete encoded emission beats the exact
	// passthrough emission by at least the threshold (integer math floors
	// conservatively — FR-003c, FR-004 never-larger by construction).
	if encBytes <= passBytes*(100-minSavingsPct)/100 {
		d.Outcome = OutcomeEncoded
		d.EncodedEmissionBytes = encBytes
		return emission, d
	}
	d.Outcome = OutcomePassthroughBelowThreshold
	return text, d
}

// numbersRoundtrip reports whether every json.Number in v survives the exact
// float64 round-trip toon-go applies when marshaling (normalizeNumberString:
// strconv.ParseFloat then strconv.FormatFloat(f, 'f', -1, 64)). A literal
// that parses with error or re-formats to different bytes would be emitted
// with a different value or shape, so the whole block must not be encoded.
func numbersRoundtrip(v interface{}) bool {
	switch val := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(val), 64)
		if err != nil {
			return false
		}
		return strconv.FormatFloat(f, 'f', -1, 64) == string(val)
	case map[string]interface{}:
		for _, e := range val {
			if !numbersRoundtrip(e) {
				return false
			}
		}
		return true
	case []interface{}:
		for _, e := range val {
			if !numbersRoundtrip(e) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// parseSingleJSON decodes text as exactly one JSON value (json.Number
// numbers) with nothing but whitespace after it.
func parseSingleJSON(text string) (interface{}, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false // trailing garbage (or a second value)
	}
	return v, true
}
