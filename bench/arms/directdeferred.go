package arms

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolsig"
)

// DirectDeferredName is the registry key of the schema-deferred direct-surface
// arm (Spec 102).
const DirectDeferredName = "direct_deferred"

// directDeferredInputSchema is the minimal permissive schema every deferred
// entry advertises. It mirrors internal/server's deferredDirectInputSchema
// byte for byte (Spec 102 FR-004): the placeholder is deliberately NOT
// {"type":"object","properties":{},"required":[]}, because clients that prune
// arguments absent from the declared schema would then drop every argument —
// and those two extra members are also real tokens this arm must charge for
// exactly as the wire does, no more and no less.
const directDeferredInputSchema = `{"type":"object"}`

// directDeferredListingPrefix and directDeferredListingSuffix wrap the entry
// array into the tools/list result envelope. Both are paid once per listing,
// not per tool (contract rule 6).
const (
	directDeferredListingPrefix = `{"tools":[`
	directDeferredListingSuffix = `]}`
)

// DirectDeferredArm renders the DIRECT enumeration surface under Spec 102's
// deferred serialization: one `tools/list` payload, not a retrieve_tools
// result set.
//
// It is the first arm to leave the retrieve_tools shape, and that is the point:
// every other arm encodes what search RETURNS, so the profiler had no way to
// price what enumeration COSTS — the payload #971 is about, and the payload the
// direct surface serves on every connect. Measured against baseline_json (the
// full definition each entry would otherwise carry) it answers SC-001 directly.
//
// Each tool renders as the wire entry the direct surface registers under
// deferral (internal/server renderDeferredDirectTool): the "<server>__<tool>"
// display name, the "[server] description" the direct surface always prepends
// plus the tool's Spec 085 compact signature on its own line, the permissive
// placeholder input schema, and no outputSchema — stripped for the same reason
// the input schema is (FR-006).
//
// Two production fields are deliberately absent, because the frozen corpus
// carries no data for them and inventing it would price a fiction:
// `annotations` (bench.Tool has no annotation hints) and the upstream
// `outputSchema` deferral would strip anyway.
//
// The annotations omission is NOT neutral, and an earlier version of this
// comment wrongly claimed it "cancels in the comparison". It cancels from the
// byte DIFFERENCE, since the field is identical in both arms — but savings is a
// ratio, not a difference:
//
//	savings = removed / full
//
// so adding an unchanged block to both columns grows the denominator and LOWERS
// the percentage. Omitting it therefore biases this arm's savings UPWARD
// relative to the real wire. Measured: this arm reports 29.5% where the
// in-process gate over the real marshalled mcp.Tool — annotations, `_meta` and
// all — reports 29.7% on the same corpus, so the two modelling gaps happen to
// nearly offset. Treat this arm as a comparative instrument for ranking
// encodings, not as an absolute wire model.
type DirectDeferredArm struct{}

// NewDirectDeferred returns the direct_deferred arm.
func NewDirectDeferred() *DirectDeferredArm { return &DirectDeferredArm{} }

// Name implements Arm.
func (*DirectDeferredArm) Name() string { return DirectDeferredName }

// IndexAltering implements Arm: deferral changes SERIALIZATION only (FR-008).
// The retrieval index still ingests the full input schema — indeed it must,
// since the signature cache this arm renders from is warmed at index time from
// exactly that text. Nothing the index sees changes, so retrieval quality
// cannot move and scoring it would only cost CI time.
func (*DirectDeferredArm) IndexAltering() bool { return false }

// LowerBound implements Arm: descriptions are preserved verbatim — the
// signature is appended to the full description, never substituted for it, so
// the measured listing size is exact rather than a floor. The schema is
// deferred, not dropped: its cost reappears as describe_tool round trips for
// the lossy share of the corpus, which the break-even model prices separately
// instead of hiding it in this arm's label.
func (*DirectDeferredArm) LowerBound() bool { return false }

// directDeferredDescription builds the description field of one deferred
// entry: the direct surface's "[server] " prefix, the upstream description,
// then a newline, the BARE tool name, and the compact signature.
//
// The tool name has to be prepended here because toolsig.Signature.Sig is the
// parenthesized parameter list alone and carries no name — appending Sig by
// itself would emit a dangling "(owner*:str, …)" with nothing to attach it to.
// internal/server's directSignatureSuffix prepends it for the same reason.
func directDeferredDescription(t bench.Tool, sig toolsig.Signature) string {
	return "[" + t.Server + "] " + t.Description + "\n" + t.Name + sig.Sig
}

// directDeferredSignature compiles one tool's compact signature.
//
// toolsig.Render is fail-soft — on an unparseable schema it returns the "(~)"
// placeholder alongside its error, which is right for the serving path, where
// a listing must never fail. It is wrong HERE: "(~)" is three tokens, so an
// arm that swallowed the error would charge almost nothing for a tool whose
// full definition baseline_json still charges in full, quietly inflating the
// measured savings. The error is propagated instead, and the harness records
// the tool as an explicit skip (contract rule 2).
func directDeferredSignature(t bench.Tool) (toolsig.Signature, error) {
	sig, err := toolsig.Render(string(t.Schema), t.Description)
	if err != nil {
		return toolsig.Signature{}, fmt.Errorf("tool %s: render compact signature: %w", t.ToolID, err)
	}
	return sig, nil
}

// EncodeTool implements Arm: one deferred `tools/list` entry as canonical JSON
// (sorted keys, compact, no HTML escaping — the same canonicalization every
// other schema-bearing arm renders through, so no arm's totals include another
// arm's whitespace).
func (*DirectDeferredArm) EncodeTool(t bench.Tool) (string, error) {
	sig, err := directDeferredSignature(t)
	if err != nil {
		return "", err
	}

	entry := map[string]interface{}{
		// server.FormatDirectToolName rather than a local "__" join: the
		// display-name format is production's to define, and binding to it
		// means a change there cannot silently desync the measurement.
		"name":        server.FormatDirectToolName(t.Server, t.Name),
		"description": directDeferredDescription(t, sig),
		"inputSchema": json.RawMessage(directDeferredInputSchema),
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("tool %s: marshal deferred entry: %w", t.ToolID, err)
	}
	return bench.CanonicalJSON(raw)
}

// EncodeListing implements Arm: the entries joined into the tools/list result
// envelope. The envelope and the separating commas are concatenated around the
// per-tool encodings verbatim rather than re-marshaled, so a listing total
// decomposes exactly into per-tool costs plus a fixed, once-paid wrapper.
func (a *DirectDeferredArm) EncodeListing(ts []bench.Tool) (string, error) {
	entries := make([]string, 0, len(ts))
	for _, t := range ts {
		enc, err := a.EncodeTool(t)
		if err != nil {
			return "", err
		}
		entries = append(entries, enc)
	}
	return directDeferredListingPrefix + strings.Join(entries, ",") + directDeferredListingSuffix, nil
}

// EncodeIndexMetadata implements Arm: the production index fields unchanged.
// Deferral never reaches the index — see IndexAltering.
func (*DirectDeferredArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	return config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: t.Description,
		ParamsJSON:  string(t.Schema),
	}, nil
}

func init() {
	MustRegister(NewDirectDeferred())
}
