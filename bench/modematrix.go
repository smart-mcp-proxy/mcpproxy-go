// modematrix.go — the Spec 103 mode matrix
// (specs/103-token-bench/contracts/mode-matrix.md, FR-015/016/017).
//
// The matrix is what an agent actually sees, crossed over the three
// configuration axes that determine it: routing mode, discovery-surface
// serialization, and direct-surface serialization. The naive product is
// 3 x 2 x 2 = 12 combinations, but only 5 of them are distinct BEHAVIOURS —
// on each surface one or both serialization axes have no consumer, so the
// other 7 configurations produce a run identical to one of the 5.
//
// Those 7 are the reason this file exists as data rather than as a loop. They
// are configurable and behaviourally redundant, NOT impossible: calling them
// impossible would be wrong, and rendering them as zeros would be worse,
// because a zero reads as "measured, and it cost nothing". Each is emitted as
// a skip row naming the cell it collapses onto and the reason code for the
// collapse (FR-017).
//
// This file composes; it does not reimplement. Cell tool catalogs come from
// ProxyToolsForMode (proxytools.go, itself derived from the production tool
// builders), the arm that renders a cell's menu is named for resolution
// through the existing bench/arms registry — bench cannot import bench/arms,
// since arms imports bench — and skip rows go through the existing
// SkippedArmResult constructor rather than a second skip shape.
package bench

import (
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Cell identifiers. These are a stability contract (FR-028): a report key that
// changed between releases would silently break comparability with every
// earlier run, so these strings are frozen even if the internal naming moves.
const (
	// CellBaseline is the FR-020 denominator, not an mcpproxy mode: the same
	// agent doing the same tasks with every upstream tool loaded inline.
	CellBaseline = "baseline"
	// CellRetrieveFull is the default: retrieve_tools surface, schema-bearing
	// discovery entries.
	CellRetrieveFull = "retrieve_full"
	// CellRetrieveCompact is the Spec 085 compact discovery serialization.
	CellRetrieveCompact = "retrieve_compact"
	// CellDirectFull is the direct enumeration surface with full schemas.
	CellDirectFull = "direct_full"
	// CellDirectDeferred is the Spec 102 deferred-schema direct surface.
	CellDirectDeferred = "direct_deferred"
	// CellCodeExec is the code-execution surface, which forces full discovery.
	CellCodeExec = "code_exec"
)

// Serialization-axis values. SerializationNotApplicable is deliberately its own
// value rather than an empty string or a default: contract rule 3 requires an
// ignored axis to be recorded as not-applicable, because recording it as
// "full" would imply a measurement of a serialization that was never in play.
const (
	SerializationFull          = config.ToolResponseModeFull
	SerializationCompact       = config.ToolResponseModeCompact
	SerializationDeferred      = config.DirectToolResponseModeDeferred
	SerializationNotApplicable = "not_applicable"
)

// Skip reason codes (contract "The other 7 combinations" + "a degenerate
// configuration"). They are distinct because they describe different things:
// an axis with no consumer on that surface, a surface that actively overrides
// the axis, and a configuration in which the surface can discover tools and
// call none of them.
const (
	// SkipReasonAxisIgnored: the configured axis governs a different surface,
	// so this combination runs identically to the cell it collapses onto.
	SkipReasonAxisIgnored = "axis-ignored"
	// SkipReasonForcedFull: the code-execution surface overwrites the response
	// mode with full and blanks the detail parameter. An override, not an
	// ignored axis, hence its own code.
	SkipReasonForcedFull = "forced-full"
	// SkipReasonDegenerate: code_execution with enable_code_execution:false.
	// Not part of the product at all — nothing is callable, so there is no
	// behaviour to collapse onto.
	SkipReasonDegenerate = "degenerate"
)

// Capability identifiers (FR-016). These are binary conditions applied to the
// cells where each is available — measure a cell with the condition on and
// with it off — NOT a fourth axis of the product.
const (
	// CapabilityBatching is describe_tool's batch form (up to 5 ids in one
	// call, Spec 085). Registered on the retrieve_tools surface and, since
	// Spec 102, on the direct surface; deliberately absent from the
	// code-execution surface, where a compact response would reference a
	// second stage that does not exist there.
	CapabilityBatching = "batching"
	// CapabilityStoredScripts is Spec 097 stored-script execution, a
	// code_execution feature with no analogue on the other surfaces.
	CapabilityStoredScripts = "stored_scripts"
	// CapabilityValidateBeforeDispatch is Spec 085 FR-013 pre-dispatch
	// argument validation with its self-healing invalid_params error. It runs
	// on the call_tool_* variants and, since Spec 102 US3, on direct dispatch;
	// the code-execution surface dispatches from the sandbox and does not.
	CapabilityValidateBeforeDispatch = "validate_before_dispatch"
)

// Endpoint paths for the routing-mode axis.
//
// These are mounted permanently at startup and stay mounted regardless of
// config (internal/server/server.go, the "Routing mode dedicated endpoints"
// block): each endpoint always serves its own routing mode. That is what makes
// the routing axis free of config changes and restarts — and it is why two
// cells that differ only in serialization legitimately share one URL.
const (
	EndpointRetrieveTools = "/mcp/call"
	EndpointDirect        = "/mcp/all"
	EndpointCodeExec      = "/mcp/code"
)

// ModeCell is one point in the matrix: either one of the 5 distinct
// behaviours, the baseline denominator, or a skip row standing in for a
// configurable-but-redundant combination.
//
// A cell is (URL, serialization-config), NOT a URL alone. The routing axis is
// chosen by endpoint; the two serialization axes are config. Both of those
// hot-reload — tool_response_mode is read from the live snapshot on every call
// and direct_tool_response_mode is rebuilt on config.reloaded — so the whole
// matrix crosses on ONE long-lived proxy instance, with a config apply between
// serialization cells rather than a restart.
type ModeCell struct {
	// ID is the stable report key (FR-028).
	ID string `json:"id"`
	// RoutingMode is the production routing-mode identifier
	// (config.RoutingMode*). Empty for the baseline, which is served by no
	// mcpproxy surface.
	RoutingMode string `json:"routing_mode,omitempty"`
	// Endpoint is the mounted path this cell is measured through. Empty for
	// the baseline and for skip rows.
	Endpoint string `json:"endpoint,omitempty"`
	// DiscoverySerialization is full / compact / not-applicable.
	DiscoverySerialization string `json:"discovery_serialization"`
	// DirectSerialization is full / deferred / not-applicable.
	DirectSerialization string `json:"direct_serialization"`
	// Capabilities lists the FR-016 binary conditions available on this cell.
	Capabilities []string `json:"capabilities,omitempty"`
	// Skipped marks the configurable-but-redundant and degenerate rows.
	Skipped bool `json:"skipped"`
	// SkipReasonCodes holds one or more of the reason codes above. It is a
	// slice because one combination genuinely carries two:
	// code_execution x compact x deferred is forced-full AND axis-ignored.
	SkipReasonCodes []string `json:"skip_reason_codes,omitempty"`
	// CollapsesOnto names the valid cell a redundant combination runs
	// identically to. Empty for valid cells and for the degenerate row, which
	// collapses onto nothing.
	CollapsesOnto string `json:"collapses_onto,omitempty"`
}

// CapabilityCondition is one FR-016 binary toggle together with the cells it
// applies to. The enumeration is part of the contract: the report must say
// which rows a capability was measured on, so a reader never has to guess
// whether an absent figure means "off" or "not available here".
type CapabilityCondition struct {
	ID        string   `json:"id"`
	AppliesTo []string `json:"applies_to"`
}

// Combination is a configured (routing mode, discovery serialization, direct
// serialization) triple plus the one config flag that can make the
// code-execution surface degenerate. It is what an operator can actually set;
// ResolveCombination maps it to the behaviour that would actually run.
type Combination struct {
	RoutingMode            string
	ToolResponseMode       string
	DirectToolResponseMode string
	EnableCodeExecution    bool
}

// modeCells is the frozen table of the 5 distinct behaviours, in contract-table
// order. Declared as a package-level value and copied out by ModeCells so a
// caller cannot mutate the matrix other runs are compared against.
var modeCells = []ModeCell{
	{
		ID:                     CellRetrieveFull,
		RoutingMode:            ModeRetrieveTools,
		Endpoint:               EndpointRetrieveTools,
		DiscoverySerialization: SerializationFull,
		DirectSerialization:    SerializationNotApplicable,
		Capabilities:           []string{CapabilityBatching, CapabilityValidateBeforeDispatch},
	},
	{
		ID:                     CellRetrieveCompact,
		RoutingMode:            ModeRetrieveTools,
		Endpoint:               EndpointRetrieveTools,
		DiscoverySerialization: SerializationCompact,
		DirectSerialization:    SerializationNotApplicable,
		Capabilities:           []string{CapabilityBatching, CapabilityValidateBeforeDispatch},
	},
	{
		ID:                     CellDirectFull,
		RoutingMode:            config.RoutingModeDirect,
		Endpoint:               EndpointDirect,
		DiscoverySerialization: SerializationNotApplicable,
		DirectSerialization:    SerializationFull,
		Capabilities:           []string{CapabilityBatching, CapabilityValidateBeforeDispatch},
	},
	{
		ID:                     CellDirectDeferred,
		RoutingMode:            config.RoutingModeDirect,
		Endpoint:               EndpointDirect,
		DiscoverySerialization: SerializationNotApplicable,
		DirectSerialization:    SerializationDeferred,
		Capabilities:           []string{CapabilityBatching, CapabilityValidateBeforeDispatch},
	},
	{
		ID:          CellCodeExec,
		RoutingMode: ModeCodeExecution,
		Endpoint:    EndpointCodeExec,
		// Full here is an OVERRIDE, not a default: the surface overwrites the
		// response mode and does not expose the detail parameter at all.
		DiscoverySerialization: SerializationFull,
		DirectSerialization:    SerializationNotApplicable,
		Capabilities:           []string{CapabilityStoredScripts},
	},
}

// baselineCell is the FR-020 denominator. It is kept OUT of modeCells because
// it is not an mcpproxy mode and must never be counted as one of the five: it
// is the same agent doing the same tasks with every upstream tool inline, and
// it is what every published percentage is measured against.
var baselineCell = ModeCell{
	ID:                     CellBaseline,
	RoutingMode:            ModeBaseline,
	DiscoverySerialization: SerializationNotApplicable,
	DirectSerialization:    SerializationNotApplicable,
}

// ModeCells returns the 5 distinct mcpproxy behaviours in contract order.
func ModeCells() []ModeCell {
	out := make([]ModeCell, len(modeCells))
	for i, c := range modeCells {
		out[i] = c.clone()
	}
	return out
}

// BaselineCell returns the FR-020 denominator cell.
func BaselineCell() ModeCell { return baselineCell.clone() }

// AllCells returns the baseline followed by the 5 mcpproxy cells — the full
// row set a comparison report renders, denominator first.
func AllCells() []ModeCell {
	return append([]ModeCell{BaselineCell()}, ModeCells()...)
}

// CellByID looks up a valid cell (the baseline included) by its stable id.
// Skip rows are not addressable this way: they are outcomes of a
// configuration, not cells of the matrix.
func CellByID(id string) (ModeCell, bool) {
	if id == CellBaseline {
		return BaselineCell(), true
	}
	for _, c := range modeCells {
		if c.ID == id {
			return c.clone(), true
		}
	}
	return ModeCell{}, false
}

// clone deep-copies the mutable slice fields so callers cannot alias the
// package-level table.
func (c ModeCell) clone() ModeCell {
	out := c
	if c.Capabilities != nil {
		out.Capabilities = append([]string(nil), c.Capabilities...)
	}
	if c.SkipReasonCodes != nil {
		out.SkipReasonCodes = append([]string(nil), c.SkipReasonCodes...)
	}
	return out
}

// redundantCombination is one row of the contract's collapse table.
type redundantCombination struct {
	routingMode            string
	toolResponseMode       string
	directToolResponseMode string
	collapsesOnto          string
	reasonCodes            []string
}

// redundantCombinations is the contract's collapse table verbatim: the 7
// configurations of the 3-axis product that are configurable and redundant.
//
// The pattern behind the table, stated once so the rows are checkable rather
// than magic: tool_response_mode is resolved by effectiveToolResponseMode and
// consumed only by the retrieve_tools handler, so no other surface reads it;
// direct_tool_response_mode is resolved by effectiveDirectToolResponseMode and
// every one of its call sites concerns the direct listing, so no other surface
// reads it either; and the code-execution surface additionally overrides
// tool_response_mode to full outright.
var redundantCombinations = []redundantCombination{
	{ModeRetrieveTools, config.ToolResponseModeFull, config.DirectToolResponseModeDeferred, CellRetrieveFull, []string{SkipReasonAxisIgnored}},
	{ModeRetrieveTools, config.ToolResponseModeCompact, config.DirectToolResponseModeDeferred, CellRetrieveCompact, []string{SkipReasonAxisIgnored}},
	{config.RoutingModeDirect, config.ToolResponseModeCompact, config.DirectToolResponseModeFull, CellDirectFull, []string{SkipReasonAxisIgnored}},
	{config.RoutingModeDirect, config.ToolResponseModeCompact, config.DirectToolResponseModeDeferred, CellDirectDeferred, []string{SkipReasonAxisIgnored}},
	{ModeCodeExecution, config.ToolResponseModeCompact, config.DirectToolResponseModeFull, CellCodeExec, []string{SkipReasonForcedFull}},
	{ModeCodeExecution, config.ToolResponseModeFull, config.DirectToolResponseModeDeferred, CellCodeExec, []string{SkipReasonAxisIgnored}},
	{ModeCodeExecution, config.ToolResponseModeCompact, config.DirectToolResponseModeDeferred, CellCodeExec, []string{SkipReasonForcedFull, SkipReasonAxisIgnored}},
}

// validCombinations maps the 5 configurations that ARE the distinct behaviours
// to their cell ids. Together with redundantCombinations it covers all 12
// points of the product exactly once.
var validCombinations = map[string]string{
	combinationKey(ModeRetrieveTools, config.ToolResponseModeFull, config.DirectToolResponseModeFull):            CellRetrieveFull,
	combinationKey(ModeRetrieveTools, config.ToolResponseModeCompact, config.DirectToolResponseModeFull):         CellRetrieveCompact,
	combinationKey(config.RoutingModeDirect, config.ToolResponseModeFull, config.DirectToolResponseModeFull):     CellDirectFull,
	combinationKey(config.RoutingModeDirect, config.ToolResponseModeFull, config.DirectToolResponseModeDeferred): CellDirectDeferred,
	combinationKey(ModeCodeExecution, config.ToolResponseModeFull, config.DirectToolResponseModeFull):            CellCodeExec,
}

// combinationKey builds the stable identifier of one configured combination.
// It doubles as the id of the skip row a redundant combination produces, so a
// reader of the report can see exactly which configuration was collapsed.
func combinationKey(routingMode, toolResponseMode, directToolResponseMode string) string {
	return routingMode + "+" + toolResponseMode + "+" + directToolResponseMode
}

// degenerateCodeExecID is the id of the degenerate skip row. The serialization
// axes are deliberately absent from it: with the surface disabled nothing is
// callable, so no serialization choice can change the outcome and all such
// configurations are one degenerate behaviour, not four.
const degenerateCodeExecID = "code_execution+disabled"

// RedundantCombinations returns the 7 skip rows of the collapse table, in
// contract order. They exist so the report can show that these configurations
// were considered and understood — reported as skipped with a reason, never as
// a zero and never as a missing row (FR-017).
func RedundantCombinations() []ModeCell {
	out := make([]ModeCell, 0, len(redundantCombinations))
	for _, rc := range redundantCombinations {
		out = append(out, rc.cell())
	}
	return out
}

// cell renders one collapse-table row as a skip ModeCell. The configured
// serialization values are preserved verbatim rather than normalized to
// not-applicable: the point of the row is to record what the operator asked
// for and why it did not produce its own measurement.
func (rc redundantCombination) cell() ModeCell {
	return ModeCell{
		ID:                     combinationKey(rc.routingMode, rc.toolResponseMode, rc.directToolResponseMode),
		RoutingMode:            rc.routingMode,
		DiscoverySerialization: rc.toolResponseMode,
		DirectSerialization:    rc.directToolResponseMode,
		Skipped:                true,
		SkipReasonCodes:        append([]string(nil), rc.reasonCodes...),
		CollapsesOnto:          rc.collapsesOnto,
	}
}

// ResolveCombination maps a configured combination to the behaviour that would
// actually run: either one of the 5 distinct cells, or a skip row naming the
// cell it collapses onto (or, for the degenerate configuration, naming
// nothing, because there is nothing equivalent to collapse onto).
//
// An unrecognized combination — an axis value that is not a production
// constant — also yields a skip row rather than an error or a silent default,
// on the same principle: the matrix never invents a measurement it did not take.
func ResolveCombination(comb Combination) ModeCell {
	// The degenerate verdict is checked FIRST and outranks redundancy. With
	// enable_code_execution false the surface can discover tools and call none
	// of them, so no serialization choice can make it equivalent to code_exec.
	if comb.RoutingMode == ModeCodeExecution && !comb.EnableCodeExecution {
		return ModeCell{
			ID:                     degenerateCodeExecID,
			RoutingMode:            comb.RoutingMode,
			DiscoverySerialization: comb.ToolResponseMode,
			DirectSerialization:    comb.DirectToolResponseMode,
			Skipped:                true,
			SkipReasonCodes:        []string{SkipReasonDegenerate},
		}
	}

	key := combinationKey(comb.RoutingMode, comb.ToolResponseMode, comb.DirectToolResponseMode)
	if id, ok := validCombinations[key]; ok {
		cell, found := CellByID(id)
		if found {
			return cell
		}
	}
	for _, rc := range redundantCombinations {
		if combinationKey(rc.routingMode, rc.toolResponseMode, rc.directToolResponseMode) == key {
			return rc.cell()
		}
	}

	return ModeCell{
		ID:                     key,
		RoutingMode:            comb.RoutingMode,
		DiscoverySerialization: comb.ToolResponseMode,
		DirectSerialization:    comb.DirectToolResponseMode,
		Skipped:                true,
		SkipReasonCodes:        []string{SkipReasonDegenerate},
	}
}

// SkipReasonText renders a skip row's reason as the single string the existing
// report shape carries, naming both the code(s) and the cell collapsed onto.
// Contract rule 4 requires a skipped row to carry a reason AND its collapse
// target; the shape it must fit into (ArmResult.SkipReason) is one string, so
// they are joined here rather than by inventing a second skip shape.
func (c ModeCell) SkipReasonText() string {
	if !c.Skipped {
		return ""
	}
	codes := strings.Join(c.SkipReasonCodes, "+")
	if c.CollapsesOnto != "" {
		return codes + ": collapses onto " + c.CollapsesOnto
	}
	if len(c.SkipReasonCodes) == 1 && c.SkipReasonCodes[0] == SkipReasonDegenerate {
		return codes + ": the surface can discover tools and call none of them, so there is no equivalent cell"
	}
	return codes
}

// SkipRow renders this cell as the report's EXISTING skipped-row shape
// (ArmResult.Skipped / SkipReason via SkippedArmResult). Reusing that
// constructor is deliberate: a second skip shape would give consumers two
// places to look for "why is this row empty", and the whole point of FR-017 is
// that the answer is always in exactly one place.
func (c ModeCell) SkipRow(corpusID string) ArmResult {
	return SkippedArmResult(c.ID, corpusID, c.SkipReasonText())
}

// EndpointURL resolves this cell's mounted MCP endpoint against a base URL
// (T015). The routing-mode axis needs no config change and no restart — all
// three routing-mode servers are built at startup and all three endpoints stay
// mounted regardless of config — so moving between routing modes is nothing
// more than changing this URL.
//
// The two serialization cells that share an endpoint are NOT an error: a cell
// is (URL, serialization-config), and the serialization half is applied by a
// hot config reload on the same long-lived instance.
//
// The returned URL is what NewMCPRetrieveCaller (mcpcaller.go) takes, so the
// transport is composed rather than rebuilt per cell.
func (c ModeCell) EndpointURL(baseURL string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("mode cell %q: base URL is required to address an endpoint", c.ID)
	}
	if c.Endpoint == "" {
		return "", fmt.Errorf("mode cell %q has no mcpproxy endpoint", c.ID)
	}
	return strings.TrimRight(baseURL, "/") + c.Endpoint, nil
}

// ProxyTools returns the built-in mcpproxy tool definitions that occupy the
// agent's context window in this cell's routing mode, delegating to
// ProxyToolsForMode so the catalog stays derived from the production tool
// builders and can never drift from what the server really registers.
//
// The direct routing mode returns none: its context cost is the rendered
// upstream catalog itself, which is measured by the cell's encoding arm rather
// than by a fixed built-in list.
func (c ModeCell) ProxyTools() []Tool {
	return ProxyToolsForMode(c.RoutingMode)
}

// ArmName names the existing encoding arm that renders this cell's tool menu,
// for resolution through the bench/arms registry by a caller that may import
// it (package bench cannot: arms imports bench). Returning a NAME rather than
// an arm is what keeps every serialization in the one place it is already
// implemented instead of being re-derived here.
func (c ModeCell) ArmName() string {
	switch c.ID {
	case CellRetrieveCompact:
		return "compact_sig"
	case CellDirectDeferred:
		return "direct_deferred"
	default:
		// baseline, retrieve_full, direct_full and code_exec all render full,
		// schema-bearing definitions — code_exec because that surface forces
		// full, not because it defaults to it.
		return "baseline_json"
	}
}

// capabilityConditions is the FR-016 enumeration: each binary toggle with the
// cells it is available on, in matrix order. Availability is decided by where
// the production surface actually registers the capability, not by where it
// would be useful — a capability that is unavailable and one that is available
// but unhelpful are different findings, and the report must not conflate them.
var capabilityConditions = []CapabilityCondition{
	{ID: CapabilityBatching, AppliesTo: []string{CellRetrieveFull, CellRetrieveCompact, CellDirectFull, CellDirectDeferred}},
	{ID: CapabilityStoredScripts, AppliesTo: []string{CellCodeExec}},
	{ID: CapabilityValidateBeforeDispatch, AppliesTo: []string{CellRetrieveFull, CellRetrieveCompact, CellDirectFull, CellDirectDeferred}},
}

// CapabilityConditions returns the FR-016 binary conditions with their
// applicable rows enumerated. They are conditions over existing cells, not a
// fourth axis: turning one on does not create a new cell, it re-measures the
// cells listed here.
func CapabilityConditions() []CapabilityCondition {
	out := make([]CapabilityCondition, len(capabilityConditions))
	for i, c := range capabilityConditions {
		out[i] = CapabilityCondition{ID: c.ID, AppliesTo: append([]string(nil), c.AppliesTo...)}
	}
	return out
}
