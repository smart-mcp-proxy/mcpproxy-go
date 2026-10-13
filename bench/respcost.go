// respcost.go — span-based component attribution for retrieve_tools responses
// (Spec 083 US1, FR-001/FR-002, research D7b).
//
// The FR-002 invariant — component token counts sum EXACTLY to the response
// total — is mathematically unsatisfiable if each field is tokenized on its
// own, because BPE is not additive across concatenation boundaries. Instead
// the response text is partitioned into contiguous labeled byte spans, the
// WHOLE text is tokenized once, and each token is attributed to the span that
// contains its starting byte. The sum then equals the total by construction.
package bench

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Component labels of a retrieve_tools response (data-model
// DiscoveryResponseMeasurement.Components). Every token of the response text
// lands in exactly one of these buckets.
const (
	// ComponentInputSchemas is each result's "inputSchema" pair — the raw JSON
	// input schema, including nested property descriptions (schema cost, not
	// tool-description cost).
	ComponentInputSchemas = "input_schemas"
	// ComponentDescriptions is each result's tool-level "description" pair.
	ComponentDescriptions = "descriptions"
	// ComponentUsageInstructions is the top-level "usage_instructions" pair.
	ComponentUsageInstructions = "usage_instructions"
	// ComponentMetadata is every other pair inside a result object: name,
	// score, server, call_with, annotations, usage stats.
	ComponentMetadata = "metadata"
	// ComponentOther is the response envelope: query/total/session_risk/notice
	// and the structural braces, brackets, and separators between spans.
	ComponentOther = "other"
)

// responseComponents lists the canonical buckets in a fixed order, so
// component maps always carry all five keys (zero-valued when empty).
var responseComponents = []string{
	ComponentInputSchemas,
	ComponentDescriptions,
	ComponentUsageInstructions,
	ComponentMetadata,
	ComponentOther,
}

// Span is one contiguous labeled byte range [Start, End) of a response text.
// A valid span list is a partition: sorted, non-empty, gap-free, covering
// exactly [0, len(text)).
type Span struct {
	Label string
	Start int
	End   int
}

// PartitionRetrieveToolsResponse partitions the raw retrieve_tools MCP text
// content (the exact JSON string produced by internal/server/mcp.go
// handleRetrieveToolsWithMode) into labeled byte spans, and returns the number
// of tools in the response. The walk is offset-exact over the ORIGINAL bytes
// (json.Decoder.InputOffset) — the text is never re-marshaled, so the token
// total measured over these spans is the cost of the wire payload itself.
//
// A key/value pair's span runs from the end of the previous value through the
// end of its own value, so it includes its preceding separator and its key
// bytes: the "inputSchema" bucket is the full cost of carrying schemas,
// key included. Structural bytes not owned by any pair land in "other".
func PartitionRetrieveToolsResponse(raw string) ([]Span, int, error) {
	dec := json.NewDecoder(strings.NewReader(raw))

	var spans []Span
	mark := 0
	emit := func(label string, end int) {
		if end > mark {
			spans = append(spans, Span{Label: label, Start: mark, End: end})
			mark = end
		}
	}
	off := func() int { return int(dec.InputOffset()) }

	if err := expectDelim(dec, '{', "top-level value"); err != nil {
		return nil, 0, err
	}
	emit(ComponentOther, off())

	resultCount := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, 0, fmt.Errorf("read top-level key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, 0, fmt.Errorf("top-level key is %T, want string", keyTok)
		}

		if key == "tools" {
			n, err := partitionToolsArray(dec, emit, off)
			if err != nil {
				return nil, 0, err
			}
			resultCount = n
			continue
		}

		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, 0, fmt.Errorf("read value of top-level key %q: %w", key, err)
		}
		label := ComponentOther
		if key == "usage_instructions" {
			label = ComponentUsageInstructions
		}
		emit(label, off())
	}

	if err := expectDelim(dec, '}', "top-level object end"); err != nil {
		return nil, 0, err
	}
	if rest := strings.TrimSpace(raw[off():]); rest != "" {
		return nil, 0, fmt.Errorf("trailing data after top-level object: %q", rest)
	}
	emit(ComponentOther, len(raw))

	return spans, resultCount, nil
}

// partitionToolsArray walks the "tools" array, emitting one span per pair of
// each tool object: inputSchema → input_schemas, description → descriptions,
// everything else → metadata. Array/object delimiters go to "other".
func partitionToolsArray(dec *json.Decoder, emit func(string, int), off func() int) (int, error) {
	if err := expectDelim(dec, '[', `"tools" value`); err != nil {
		return 0, err
	}
	emit(ComponentOther, off()) // ..."tools":[

	count := 0
	for dec.More() {
		if err := expectDelim(dec, '{', "tools array element"); err != nil {
			return 0, err
		}
		emit(ComponentOther, off())

		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return 0, fmt.Errorf("read tool key: %w", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return 0, fmt.Errorf("tool key is %T, want string", keyTok)
			}
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				return 0, fmt.Errorf("read value of tool key %q: %w", key, err)
			}
			label := ComponentMetadata
			switch key {
			case "inputSchema":
				label = ComponentInputSchemas
			case "description":
				label = ComponentDescriptions
			}
			emit(label, off())
		}

		if err := expectDelim(dec, '}', "tool object end"); err != nil {
			return 0, err
		}
		emit(ComponentOther, off())
		count++
	}

	if err := expectDelim(dec, ']', `"tools" array end`); err != nil {
		return 0, err
	}
	emit(ComponentOther, off())
	return count, nil
}

// expectDelim reads the next token and fails unless it is the given delimiter.
func expectDelim(dec *json.Decoder, want json.Delim, what string) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read %s: %w", what, err)
	}
	d, ok := tok.(json.Delim)
	if !ok || d != want {
		return fmt.Errorf("%s is %v, want %q", what, tok, want)
	}
	return nil
}

// AttributeTokens tokenizes the WHOLE text once and attributes each token to
// the span containing its starting byte (research D7b). It returns the total
// token count and the per-label component counts; all five canonical
// components are always present in the map. Invariant, by construction:
// sum(components) == total.
//
// Token byte offsets are recovered by decoding each token id individually —
// cl100k is a byte-level BPE, so per-token decoding is lossless and the
// concatenated pieces reproduce the input bytes exactly (verified at runtime).
func AttributeTokens(tk *Tokenizer, text string, spans []Span) (int, map[string]int, error) {
	if err := validateSpanPartition(spans, len(text)); err != nil {
		return 0, nil, err
	}

	components := make(map[string]int, len(responseComponents))
	for _, label := range responseComponents {
		components[label] = 0
	}

	ids := tk.enc.Encode(text, nil, nil)
	offset := 0
	si := 0
	for _, id := range ids {
		piece := tk.enc.Decode([]int{id})
		if len(piece) == 0 {
			return 0, nil, fmt.Errorf("token %d decoded to zero bytes at offset %d", id, offset)
		}
		for si < len(spans) && spans[si].End <= offset {
			si++
		}
		if si >= len(spans) {
			return 0, nil, fmt.Errorf("token starting at byte %d falls outside the span partition", offset)
		}
		components[spans[si].Label]++
		offset += len(piece)
	}
	if offset != len(text) {
		return 0, nil, fmt.Errorf("decode round-trip covered %d bytes, want %d", offset, len(text))
	}

	return len(ids), components, nil
}

// validateSpanPartition enforces the partition contract: spans are sorted,
// non-empty, contiguous, and cover exactly [0, textLen).
func validateSpanPartition(spans []Span, textLen int) error {
	if len(spans) == 0 {
		if textLen == 0 {
			return nil
		}
		return fmt.Errorf("no spans for %d bytes of text", textLen)
	}
	if spans[0].Start != 0 {
		return fmt.Errorf("first span starts at %d, want 0", spans[0].Start)
	}
	for i, s := range spans {
		if s.Start >= s.End {
			return fmt.Errorf("span %d (%q) is empty or inverted: [%d,%d)", i, s.Label, s.Start, s.End)
		}
		if i > 0 && s.Start != spans[i-1].End {
			return fmt.Errorf("span %d (%q) starts at %d, want %d (gap or overlap)", i, s.Label, s.Start, spans[i-1].End)
		}
	}
	if last := spans[len(spans)-1].End; last != textLen {
		return fmt.Errorf("spans cover [0,%d), text is %d bytes", last, textLen)
	}
	return nil
}

// MeasureRetrieveToolsResponse is the one-call US1 measurement: partition the
// raw response text, attribute tokens, and assemble the report row. queryID
// and latencyMs are caller-observed (the harness measures latency around the
// MCP call, FR-023).
func MeasureRetrieveToolsResponse(tk *Tokenizer, queryID, raw string, latencyMs float64) (*DiscoveryResponseMeasurement, error) {
	spans, resultCount, err := PartitionRetrieveToolsResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("partition retrieve_tools response for query %q: %w", queryID, err)
	}
	total, components, err := AttributeTokens(tk, raw, spans)
	if err != nil {
		return nil, fmt.Errorf("attribute tokens for query %q: %w", queryID, err)
	}
	return &DiscoveryResponseMeasurement{
		QueryID:     queryID,
		TotalTokens: total,
		ResultCount: resultCount,
		LatencyMs:   latencyMs,
		Components:  components,
	}, nil
}

// SummarizeResponseCost aggregates per-query measurements into the FR-001
// summary with nearest-rank percentiles over TotalTokens (same rank rule as
// the latency percentiles in live_report.go). PerQuery keeps the caller's
// order (golden-set order), so reports stay deterministic (FR-010).
func SummarizeResponseCost(perQuery []DiscoveryResponseMeasurement) *ResponseCostSummary {
	s := &ResponseCostSummary{PerQuery: perQuery}
	if len(perQuery) == 0 {
		return s
	}
	totals := make([]int, len(perQuery))
	sum := 0
	for i, m := range perQuery {
		totals[i] = m.TotalTokens
		sum += m.TotalTokens
	}
	sort.Ints(totals)
	s.P50 = percentileInt(totals, 50)
	s.P95 = percentileInt(totals, 95)
	s.Max = totals[len(totals)-1]
	s.Mean = float64(sum) / float64(len(totals))
	return s
}

// percentileInt returns the nearest-rank percentile p (0-100) of a sorted int
// slice — the int twin of live_report.go's duration percentile.
func percentileInt(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100.0 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
