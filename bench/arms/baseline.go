package arms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// BaselineName is the registry key of the full-JSON baseline arm.
const BaselineName = "baseline_json"

// listingSeparator joins per-tool renderings into a listing, so listing totals
// decompose exactly into per-tool costs plus separators.
const listingSeparator = "\n\n"

// CanonicalJSON re-encodes raw JSON bytes into the canonical form used
// everywhere in the bench (research D7b): object keys sorted lexicographically
// at every depth, array order preserved, number literals preserved verbatim
// (via json.Number — no float round-trip), compact (no insignificant
// whitespace), and no HTML escaping. Identical JSON content in any key order
// canonicalizes to identical bytes (FR-010).
func CanonicalJSON(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	// Reject trailing garbage after the first value — a silently half-parsed
	// schema would be a silently truncated encoding (contract rule 2).
	if dec.More() {
		return "", fmt.Errorf("canonicalize JSON: trailing data after value")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	// Encoder appends a newline; the canonical form is the bare value.
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// CanonicalToolText is THE canonical full-definition renderer (research D7b):
// name + "\n" + description, plus "\n" + canonical-JSON schema when the tool
// has one — byte-identical to the text shape the existing
// Tokenizer.CountToolWithSchema counts for a canonical-schema tool. This single
// renderer feeds the baseline_json arm, the naive full-menu count, the
// proxy-menu count, savings-% denominators, and break-even inputs, so every
// savings percentage shares one denominator.
func CanonicalToolText(t bench.Tool) (string, error) {
	s := t.Name + "\n" + t.Description
	if len(t.Schema) > 0 {
		canon, err := CanonicalJSON(t.Schema)
		if err != nil {
			return "", fmt.Errorf("tool %s: %w", t.ToolID, err)
		}
		s += "\n" + canon
	}
	return s, nil
}

// BaselineArm is the mandatory baseline_json arm: the canonical
// full-definition rendering every other arm's savings are measured against.
type BaselineArm struct{}

// NewBaseline returns the baseline_json arm.
func NewBaseline() *BaselineArm { return &BaselineArm{} }

// Name implements Arm.
func (*BaselineArm) Name() string { return BaselineName }

// IndexAltering implements Arm: the baseline IS the production rendering, so
// it never alters what the index ingests.
func (*BaselineArm) IndexAltering() bool { return false }

// LowerBound implements Arm: descriptions are preserved verbatim.
func (*BaselineArm) LowerBound() bool { return false }

// EncodeTool implements Arm via the canonical full-definition renderer.
func (*BaselineArm) EncodeTool(t bench.Tool) (string, error) {
	return CanonicalToolText(t)
}

// EncodeListing implements Arm: per-tool renderings joined by a fixed
// separator (the baseline has no shared preamble to amortize).
func (a *BaselineArm) EncodeListing(ts []bench.Tool) (string, error) {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		enc, err := a.EncodeTool(t)
		if err != nil {
			return "", err
		}
		parts = append(parts, enc)
	}
	return strings.Join(parts, listingSeparator), nil
}

// EncodeIndexMetadata implements Arm: a rendering-only arm returns the tool's
// production index fields unchanged — the same Name/ServerName/Description/
// ParamsJSON mapping internal/upstream/core builds from a live tools/list
// (ParamsJSON is the tool's input-schema JSON, empty when the tool has none).
func (*BaselineArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	return config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: t.Description,
		ParamsJSON:  string(t.Schema),
	}, nil
}

func init() {
	MustRegister(NewBaseline())
}
