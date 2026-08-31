// replay.go — Spec 103 US1: deterministic cost recomputation over a REAL
// recorded workload (specs/103-token-bench/contracts/replay-input.md).
//
// What replay is. An operator exports their own activity log
// (`mcpproxy activity export --format json`) and supplies a fleet. Replay then
// prices, per mode cell, the tool menu that fleet would put in the agent's
// context, and reports the recorded call shape it was priced against. It costs
// nothing to run, spends no model tokens, and replaces frozen-corpus numbers
// with numbers from a fleet somebody actually uses.
//
// # A fleet input is MANDATORY
//
// A menu is a property of the TOOL DEFINITIONS the agent was shown, and the
// activity export carries no fleet snapshot — not even implicitly, because
// under the direct routing mode the menu is the whole upstream fleet rather
// than a fixed built-in list. So a recording on its own can compute nothing at
// all. That is why `-replay <jsonl>` without a fleet is ErrReplayFleetRequired
// and not a degraded run: there is no reduced figure to degrade TO, and a run
// that quietly produced menu-less output would be a report with no menu cost
// in it, which is the only thing US1 exists to produce.
//
// # What bodies-off can and cannot yield
//
// The default posture reads no request or response bodies (privacy: the
// activity EXPORT path does not mask, so an export is raw user traffic). That
// posture fixes what is knowable, per the contract's table:
//
//   - MENU COST is measured for every cell, from the fleet input.
//   - ABSOLUTE COMPLETE-WORKLOAD COST is available for NO cell, because a
//     complete workload includes every consumed response and that text is
//     absent. It is reported as withheld WITH a reason — never as a zero,
//     which would read as "measured, and it cost nothing".
//   - The CROSS-MODE DELTA between direct_full and direct_deferred IS
//     measurable, because those two cells serve identical call responses and
//     the response term cancels out of the difference. That delta is the
//     honest bodies-off headline. The retrieve cells get no such delta: their
//     serialization axis changes the RESPONSE body, which is exactly the text
//     bodies-off does not have.
//
// Byte lengths do NOT rescue this. `request_bytes` / `response_bytes` are
// pre-truncation BYTE measurements, and the loader deliberately reports them
// as Cost{Basis: CostEstimated, Bytes: n} with NO token count: converting
// bytes to tokens would require a fudge factor nobody sanctioned. So a
// response figure is emitted only when every contributing response was
// genuinely tokenized (bodies-on), and otherwise not at all.
//
// # Every figure is a counterfactual
//
// FR-004 is the spec's most load-bearing constraint and the easiest to lose in
// a headline: a replay recomputes a RECORDED call shape against the SUPPLIED
// fleet. No agent ran under these modes; no model was asked. And because the
// fleet comes from the fleet input rather than the recording, the figures score
// recorded work against TODAY's fleet, not the fleet as it stood when the
// session was recorded — internally valid across modes, not a historical
// reconstruction. ValidateCounterfactual refuses to let a block carrying
// figures leave without saying so.
//
// # Determinism
//
// Two runs over the same inputs must be byte-identical (SC-002), so replay
// reports pin GeneratedAt to ReplayGeneratedAt rather than stamping the wall
// clock. Nothing else here reads a clock, sorts a map, or depends on iteration
// order; the loader already orders sessions deterministically.
package bench

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench/replaycorpus"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ErrReplayFleetRequired is the T019 hard error. It is a sentinel rather than
// a formatted string so a caller (and a test) can assert the RULE, not the
// wording: a recording-only invocation is an error, not a degraded run.
var ErrReplayFleetRequired = errors.New("replay requires a fleet input as well as a recording")

// ReplayGeneratedAt is the pinned generated_at stamp of every replay report
// (T029). SC-002 asks for byte-identical output across runs, and a wall-clock
// stamp defeats that check on its own, silently — a diff that always differs
// in one line trains a reader to ignore diffs. The value is a real RFC 3339
// timestamp so the report still validates against the schema's date-time
// format, and an obviously synthetic one so nobody mistakes it for a run time.
const ReplayGeneratedAt = "1970-01-01T00:00:00Z"

// ReplayCounterfactualLabel is the FR-004 label carried by every replay block.
//
// It states three things a reader needs at once, because dropping any one of
// them turns the figures into a claim the data cannot support: that this is a
// counterfactual, that no agent behaviour was observed, and that recorded work
// is scored against the supplied fleet rather than the fleet of the day.
const ReplayCounterfactualLabel = "COUNTERFACTUAL — not observed agent behaviour. " +
	"These figures recompute a RECORDED call shape (call sequence, tool mix and call counts, taken from an activity export) " +
	"against the SUPPLIED FLEET under each mode cell. No agent ran under these modes and no model was asked, " +
	"so nothing here measures how an agent behaves. The fleet comes from the fleet input, not from the recording, " +
	"so a replay scores recorded work against TODAY's fleet rather than the fleet as it stood when the session was recorded: " +
	"internally valid across modes, not a historical reconstruction."

// replayDroppingReasons are the exclusion reasons that REMOVE a session from
// the replay. They alone account for SessionsSupplied minus SessionsUsed;
// truncated and bodies_missing are reported as flags on sessions that still
// contributed, and unattributed counts records that never became a session at
// all. Keeping the two kinds distinguishable is what makes the accounting
// checkable rather than merely present.
var replayDroppingReasons = []string{ReplayExclusionSensitive, ReplayExclusionUnreplayable}

// replayExclusionOrder fixes the row order of the exclusion table: what was
// DROPPED first, then what was flagged, then what never reached a session.
// Deterministic, and it also puts the consequential rows where a reader meets
// them before the headline.
var replayExclusionOrder = []string{
	ReplayExclusionSensitive,
	ReplayExclusionUnreplayable,
	ReplayExclusionTruncated,
	ReplayExclusionBodiesMissed,
	ReplayExclusionUnattributed,
}

// ReplayOptions configures one replay run.
type ReplayOptions struct {
	// RecordingPath is the activity JSONL export. It must live OUTSIDE the
	// repository working tree — the loader refuses an in-tree path, because a
	// replay input is raw recorded traffic and must never be committed.
	RecordingPath string

	// Fleet is the MANDATORY fleet input: the tool definitions the menu cost
	// is computed from. Either a frozen corpus (the committed corpora already
	// serve this) or a snapshot of a live proxy's catalog.
	Fleet *Corpus

	// FleetID names the fleet shape every figure is quoted for (IC-004).
	// Empty falls back to the corpus version.
	FleetID string

	// Bodies is the loader's privacy posture. The ZERO VALUE is bodies-off,
	// which is the safe default and the one this file's contract is written
	// for; bodies-on is an explicit opt-in that warns.
	Bodies replaycorpus.BodyPolicy

	// Encoding is the tiktoken encoding the loader counts bodies with on a
	// bodies-on run. Empty means the loader default.
	Encoding string

	// Arms resolves the encoding arms named by the mode matrix, keyed by arm
	// name. Package bench cannot import bench/arms (arms imports bench), so
	// arms arrive here as structural EncodingArm values the way armrun.go
	// takes them. ReplayArmNames lists exactly what must be present.
	Arms map[string]EncodingArm

	// Warnf receives the loader's operator-facing warnings. Nil means stderr.
	Warnf func(format string, args ...any)
}

// ReplayArmNames returns the arm names a replay needs resolved, in a stable
// order. It is smaller than the set of arms the mode matrix names, and
// deliberately so — see replayMenuArmName for why the discovery-serialization
// arm never prices a menu.
func ReplayArmNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, cell := range ModeCells() {
		name := replayMenuArmName(cell)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// FleetResolver builds the loader's replayability predicate from a fleet
// input: does the (server, tool) a record names still exist in the fleet this
// replay scores against?
//
// It answers true for mcpproxy's OWN built-in tools regardless of the fleet.
// A recording of a proxy session is full of retrieve_tools / call_tool_* /
// describe_tool records, and those are not upstream tools — treating them as
// vanished would mark essentially every real recording unreplayable and empty
// the report, which is a wiring bug dressed as a data finding.
//
// It is exported because a caller assembling its own load (a test, or a future
// bodies-on path) must be able to wire the SAME predicate: the loader treats a
// nil resolver as "replayability was never evaluated", and a replay that
// silently took that branch would report "nothing was unreplayable" when it
// meant "nobody looked".
func FleetResolver(fleet *Corpus) func(serverName, toolName string) bool {
	if fleet == nil {
		return nil
	}
	inFleet := make(map[string]bool, len(fleet.Tools)*2)
	for _, tl := range fleet.Tools {
		inFleet[fleetKey(tl.Server, tl.Name)] = true
		if tl.ToolID != "" {
			inFleet[strings.ToLower(tl.ToolID)] = true
		}
	}
	builtin := make(map[string]bool)
	for _, mode := range []string{ModeRetrieveTools, ModeCodeExecution} {
		for _, tl := range ProxyToolsForMode(mode) {
			builtin[strings.ToLower(tl.Name)] = true
		}
	}
	return func(serverName, toolName string) bool {
		server, tool := splitToolIdentity(serverName, toolName)
		if builtin[strings.ToLower(tool)] {
			return true
		}
		if inFleet[fleetKey(server, tool)] {
			return true
		}
		return inFleet[strings.ToLower(server+":"+tool)]
	}
}

func fleetKey(server, tool string) string {
	return strings.ToLower(server) + "\x00" + strings.ToLower(tool)
}

// splitToolIdentity recovers (server, tool) when the record carried the two
// fused into the tool name. Both proxy surfaces are in play in a real export:
// "<server>:<tool>" on the retrieve_tools surface and "<server>__<tool>" on
// the direct one.
func splitToolIdentity(serverName, toolName string) (server, tool string) {
	if serverName != "" {
		return serverName, toolName
	}
	if idx := strings.Index(toolName, "__"); idx > 0 {
		return toolName[:idx], toolName[idx+2:]
	}
	if idx := strings.Index(toolName, ":"); idx > 0 {
		return toolName[:idx], toolName[idx+1:]
	}
	return serverName, toolName
}

// RunReplay loads a recording, prices every mode cell's menu against the
// supplied fleet, and returns the report block.
//
// The fleet check comes FIRST, before the recording is even opened: refusing
// early is what makes the refusal a rule about the invocation rather than an
// accident of whichever file happened to be readable.
func RunReplay(tk *Tokenizer, opts ReplayOptions) (*ReplayBlock, error) {
	if tk == nil {
		return nil, errors.New("replay: a tokenizer is required")
	}
	if opts.Fleet == nil || len(opts.Fleet.Tools) == 0 {
		return nil, fmt.Errorf("%w: a menu is a property of the tool definitions and the activity export carries no fleet snapshot, "+
			"so a recording on its own computes nothing — pass a frozen corpus or a live-proxy catalog "+
			"(this is an error, not a degraded run)", ErrReplayFleetRequired)
	}
	if opts.RecordingPath == "" {
		return nil, errors.New("replay: a recording path is required (mcpproxy activity export --format json)")
	}
	if err := requireReplayArms(opts.Arms); err != nil {
		return nil, err
	}

	corpus, err := replaycorpus.LoadFile(opts.RecordingPath, replaycorpus.Options{
		Bodies:        opts.Bodies,
		Encoding:      opts.Encoding,
		Warnf:         opts.Warnf,
		FleetResolver: FleetResolver(opts.Fleet),
	})
	if err != nil {
		return nil, fmt.Errorf("replay: load recording: %w", err)
	}
	// A replay ALWAYS supplies a fleet, so the loader must always have been
	// able to evaluate replayability. False here is not a fact about the data;
	// it means this function failed to pass the resolver, and the difference
	// between "nothing was unreplayable" and "nobody looked" would then be
	// invisible in the report.
	if !corpus.FleetChecked {
		return nil, errors.New("replay: internal wiring error — the loader ran without a fleet resolver, " +
			"so 'unreplayable' was never evaluated and its absence would be reported as a clean result")
	}

	return buildReplayBlock(tk, corpus, opts)
}

// requireReplayArms fails loudly on a missing arm. A cell whose arm could not
// be resolved must not quietly vanish from the report: an absent row reads as
// "not applicable", while the truth is "we could not measure it".
func requireReplayArms(available map[string]EncodingArm) error {
	var missing []string
	for _, name := range ReplayArmNames() {
		if available[name] == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("replay: no encoding arm supplied for %s — every mode cell must be priced or the row would be silently absent",
			strings.Join(missing, ", "))
	}
	return nil
}

// buildReplayBlock assembles the report block from a loaded corpus.
func buildReplayBlock(tk *Tokenizer, corpus *replaycorpus.Corpus, opts ReplayOptions) (*ReplayBlock, error) {
	used, exclusions := partitionReplaySessions(corpus)

	calls := 0
	for _, session := range used {
		// AllCalls, never Calls: code-execution sub-calls hang off their
		// parent's SubCalls and are absent from Calls by design, so a sum over
		// Calls undercounts every sandbox-issued call in the recording.
		calls += len(session.AllCalls())
	}
	responseTokens := measuredResponseTokens(used)

	shape, err := replayFleetShape(tk, opts)
	if err != nil {
		return nil, err
	}

	cells := make([]ReplayCellCost, 0, len(ModeCells()))
	menuByCell := make(map[string]int, len(cells))
	for _, cell := range ModeCells() {
		menu, err := replayMenuTokens(tk, cell, opts.Fleet, opts.Arms)
		if err != nil {
			return nil, err
		}
		menuByCell[cell.ID] = menu
		row := ReplayCellCost{
			CellID: cell.ID,
			// Measured: the menu is counted by the pinned deterministic
			// tokenizer over real tool definitions from the fleet input.
			// (The delta row below is `computed` — arithmetic over two of
			// these — and carries its own badge for exactly that reason.)
			Provenance: ProvenanceMeasured,
			Calls:      calls,
			MenuTokens: menu,
			// True for EVERY cell, always. See withheldWorkloadReason.
			AbsoluteWorkloadWithheld: true,
			WithheldReason:           withheldWorkloadReason(cell, opts.Bodies),
		}
		if responseTokens != nil && isDirectCell(cell) {
			// Only the two direct cells serve identical call responses, so a
			// measured response total from one recording is meaningful for
			// them alone. It is still NOT a complete workload — hence the
			// withheld flag above stays set.
			total := *responseTokens
			row.ResponseTokens = &total
		}
		cells = append(cells, row)
	}

	block := &ReplayBlock{
		AccountingSource:        AccountingSource{Kind: AccountingKindTokenizer, Identity: tk.encoding},
		Counterfactual:          ReplayCounterfactualLabel,
		BodiesIncluded:          opts.Bodies == replaycorpus.BodiesOnUnmasked,
		FleetShape:              shape,
		SessionsSupplied:        len(corpus.Sessions),
		SessionsUsed:            len(used),
		Exclusions:              exclusions,
		LoaderAccounting:        loaderAccounting(&corpus.Exclusions),
		Cells:                   cells,
		DirectDelta:             replayDirectDelta(menuByCell),
		SensitiveFlagBestEffort: true,
	}

	// Emission-time validation, not a rendering concern: a block that escaped
	// unlabelled or unbalanced would carry its defect into every downstream
	// consumer, and the caveat is the first thing a summary drops.
	if err := block.ValidateCounterfactual(); err != nil {
		return nil, err
	}
	if err := block.ValidateExclusionBalance(); err != nil {
		return nil, err
	}
	return block, nil
}

// partitionReplaySessions splits the loaded sessions into those that
// contribute and the exclusion accounting for everything that does not.
//
// The two kinds of row are kept apart on purpose. A session is DROPPED for
// sensitivity or unreplayability, and those two counts explain
// supplied-minus-used exactly, because each dropped session is attributed to
// one primary reason. Truncation and missing bodies do not drop a session:
// bodies-off no response figure is emitted at all, so a truncated record has
// nothing to understate — but it is still counted and reported, because
// understating cost in the project's favour is the failure this whole feature
// exists to prevent, and silence is how it would happen.
func partitionReplaySessions(corpus *replaycorpus.Corpus) ([]*replaycorpus.ReplaySession, []ReplayExclusion) {
	counts := map[string]int{}
	used := make([]*replaycorpus.ReplaySession, 0, len(corpus.Sessions))

	for _, session := range corpus.Sessions {
		flags := session.Usability
		if flags.Truncated {
			counts[ReplayExclusionTruncated]++
		}
		if flags.BodiesMissing {
			counts[ReplayExclusionBodiesMissed]++
		}
		switch {
		case flags.Sensitive:
			// Privacy outranks everything: a session flagged sensitive is not
			// priced, whatever else is true of it.
			counts[ReplayExclusionSensitive]++
		case flags.Unreplayable:
			counts[ReplayExclusionUnreplayable]++
		default:
			used = append(used, session)
		}
	}

	// Records the loader dropped before they could join any unit of work:
	// non-call activity (quarantine changes, policy decisions), records with
	// no tool name, and records with no work session to belong to. They are
	// counted in RECORDS rather than sessions — they never became a session,
	// so no session count exists for them — and the dashboard says so.
	counts[ReplayExclusionUnattributed] = corpus.Exclusions.TotalDropped()

	rows := make([]ReplayExclusion, 0, len(replayExclusionOrder))
	for _, reason := range replayExclusionOrder {
		if counts[reason] > 0 {
			rows = append(rows, ReplayExclusion{Reason: reason, Sessions: counts[reason]})
		}
	}
	return used, rows
}

// loaderAccounting carries the loader's own exclusion detail through to the
// report, alongside the session-level rows above.
//
// The session rows answer "how many units of work did not make it in". They
// cannot answer "what was dropped from a unit that DID make it in", and that
// second question has teeth: a withheld response cost collapses a cell's
// response total to nil, and without a row saying why, the reader sees a
// missing figure with no explanation. SC-008 asks that nothing be counted
// silently; a suppressed cost inside an admitted session is exactly the kind
// of silence it means. OrphanedSubCalls is here for the same reason — those
// records ARE counted in the workload, but their attribution to a parent was
// lost, and a total that hides that is a total the reader cannot audit.
func loaderAccounting(rep *replaycorpus.ExclusionReport) *ReplayLoaderAccounting {
	if rep == nil {
		return nil
	}
	acc := &ReplayLoaderAccounting{
		RecordsDropped:   rep.TotalDropped(),
		CostsWithheld:    rep.TotalWithheld(),
		RecordsFlagged:   rep.TotalFlagged(),
		OrphanedSubCalls: rep.OrphanedSubCalls,
	}
	for _, r := range rep.DroppedRows() {
		acc.Dropped = append(acc.Dropped, ReplayLoaderReason{Reason: string(r.Reason), Count: r.Count})
	}
	for _, r := range rep.WithheldRows() {
		acc.Withheld = append(acc.Withheld, ReplayLoaderReason{Reason: string(r.Reason), Count: r.Count})
	}
	for _, r := range rep.FlaggedRows() {
		acc.Flagged = append(acc.Flagged, ReplayLoaderReason{Reason: string(r.Reason), Count: r.Count})
	}
	if acc.RecordsDropped == 0 && acc.CostsWithheld == 0 && acc.RecordsFlagged == 0 && acc.OrphanedSubCalls == 0 {
		return nil
	}
	return acc
}

// measuredResponseTokens sums response cost across the contributing sessions,
// and returns nil unless EVERY contributing response was genuinely tokenized.
//
// The all-or-nothing rule is the point. Cost.Tokens is populated only for
// CostMeasured; CostEstimated carries a pre-truncation BYTE length and no
// token count on purpose, and CostUnavailable carries zero for both plus a
// reason. Summing naively would let an unavailable cost fall through as a
// zero and understate the total — the one direction of error this benchmark
// must not make.
func measuredResponseTokens(sessions []*replaycorpus.ReplaySession) *int {
	total := 0
	seen := 0
	for _, session := range sessions {
		for _, call := range session.AllCalls() {
			if call.ResponseCost.Basis != replaycorpus.CostMeasured {
				return nil
			}
			total += call.ResponseCost.Tokens
			seen++
		}
	}
	if seen == 0 {
		return nil
	}
	return &total
}

// isDirectCell reports whether a cell is served by the direct enumeration
// surface — the only pair whose call responses are identical, and therefore
// the only pair a cross-mode comparison can cancel the response term out of.
func isDirectCell(cell ModeCell) bool { return cell.RoutingMode == config.RoutingModeDirect }

// withheldWorkloadReason states, per cell, why no absolute complete-workload
// figure is available — and, for the cells that have no cross-mode delta
// either, why that is so. A withheld figure must always carry its reason:
// rendering it as a zero would read as "measured, and it cost nothing".
func withheldWorkloadReason(cell ModeCell, bodies replaycorpus.BodyPolicy) string {
	// The reason MUST track the posture. A bodies-on run does carry response
	// text, so telling the reader it is "absent" while the same row shows a
	// response figure states a false premise next to the number that
	// contradicts it. The figure is still not a complete workload — a
	// recording is a sample of one agent's traffic, not the whole of what a
	// mode would cost — but the reason for withholding is different, and
	// saying so is the difference between an honest caveat and a wrong one.
	base := "an absolute complete-workload cost includes every consumed response, and that text is absent from a bodies-off replay"
	if bodies != replaycorpus.BodiesOff {
		base = "this replay carries response bodies, so a response total is measurable — but it is still not an absolute " +
			"complete-workload cost: it prices the recorded traffic, not the workload a mode would incur in general, and any " +
			"record whose cost was withheld or estimated is excluded from it"
	}
	if isDirectCell(cell) {
		return base + "; the comparable figure for this cell is the " + CellDirectFull + " <-> " + CellDirectDeferred +
			" delta, whose identical call responses cancel out of the comparison"
	}
	return base + "; no cross-mode delta is available for this cell either, because its serialization changes the response body — " +
		"the very text a bodies-off replay does not carry"
}

// replayMenuArmName picks the arm that prices a cell's menu.
//
// Only the DIRECT cells use their own arm, and the reason is worth stating
// because the alternative looks more consistent and is wrong. The
// discovery-serialization axis (`tool_response_mode`: full vs compact)
// governs the retrieve_tools RESPONSE — it is resolved by
// effectiveToolResponseMode, whose only production consumer is the
// retrieve_tools handler. It does not touch the built-in tool LISTING those
// cells put in the agent's context, which is served in full either way. So
// pricing retrieve_compact's menu with the compact arm would invent a saving
// the wire does not deliver.
//
// The consequence is deliberate and is itself a finding: bodies-off,
// retrieve_full and retrieve_compact have IDENTICAL menu cost, because the
// axis that separates them lives entirely in responses.
func replayMenuArmName(cell ModeCell) string {
	if isDirectCell(cell) {
		return cell.ArmName()
	}
	return baselineArmName
}

// replayMenuTools returns what actually occupies the agent's context in a cell.
//
// Under the direct routing mode that is the WHOLE upstream fleet, enumerated;
// under the other modes it is mcpproxy's built-in catalog for that mode, and
// the fleet stays out of context — which is the entire proposition being
// measured.
//
// One modelling gap, stated rather than hidden: the direct surface also
// registers describe_tool, which this menu does not include. It is one
// definition against the whole fleet and identical in both direct cells, so it
// cancels from the delta's numerator — but savings is a ratio, so omitting it
// shrinks the denominator and biases the delta PERCENTAGE slightly upward.
// Treat the delta as a comparative instrument, not an absolute wire model.
func replayMenuTools(cell ModeCell, fleet *Corpus) []Tool {
	if isDirectCell(cell) {
		return fleet.Tools
	}
	return cell.ProxyTools()
}

func replayMenuTokens(tk *Tokenizer, cell ModeCell, fleet *Corpus, available map[string]EncodingArm) (int, error) {
	name := replayMenuArmName(cell)
	arm := available[name]
	if arm == nil {
		return 0, fmt.Errorf("replay: mode cell %q needs the %q arm, which was not supplied", cell.ID, name)
	}
	tools := replayMenuTools(cell, fleet)
	if len(tools) == 0 {
		return 0, fmt.Errorf("replay: mode cell %q has an empty menu — a zero menu cost would read as a measurement", cell.ID)
	}
	text, err := arm.EncodeListing(tools)
	if err != nil {
		return 0, fmt.Errorf("replay: mode cell %q: encode menu with arm %q: %w", cell.ID, name, err)
	}
	return tk.Count(text), nil
}

// replayDirectDelta produces the honest bodies-off headline, or nothing.
//
// Nothing is the right answer when either direct cell is missing: a delta
// against an absent term is not a smaller delta, it is not a delta.
func replayDirectDelta(menuByCell map[string]int) *ReplayDirectDelta {
	full, okFull := menuByCell[CellDirectFull]
	deferred, okDeferred := menuByCell[CellDirectDeferred]
	if !okFull || !okDeferred || full <= 0 {
		return nil
	}
	delta := full - deferred
	return &ReplayDirectDelta{
		FromCellID: CellDirectFull,
		ToCellID:   CellDirectDeferred,
		// Computed, not measured: arithmetic over two measured menu costs.
		Provenance:  ProvenanceComputed,
		DeltaTokens: delta,
		DeltaPct:    float64(delta) / float64(full) * 100,
	}
}

// replayFleetShape describes the fleet every figure is quoted for (IC-004).
// A saving quoted without its fleet shape is a saving quoted at an unstated
// fleet size, which is how spec 102 over-projected from a single corpus.
//
// The per-tool distribution is measured with the baseline arm — the canonical
// full-definition rendering — so mean and p95 are comparable with every other
// definition-size figure the benchmark publishes.
// It deliberately takes no recording: the shape describes the FLEET a figure
// is quoted for, and folding the recording into it would let a small recording
// look like a small fleet.
func replayFleetShape(tk *Tokenizer, opts ReplayOptions) (FleetShape, error) {
	id := opts.FleetID
	if id == "" {
		id = opts.Fleet.Version
	}
	shape := FleetShape{ID: id, ToolCount: len(opts.Fleet.Tools)}

	arm := opts.Arms[baselineArmName]
	if arm == nil {
		return shape, fmt.Errorf("replay: the %q arm is required to describe the fleet shape", baselineArmName)
	}
	sizes := make([]int, 0, len(opts.Fleet.Tools))
	total := 0
	for _, tl := range opts.Fleet.Tools {
		text, err := arm.EncodeTool(tl)
		if err != nil {
			return shape, fmt.Errorf("replay: fleet shape: encode tool %s: %w", tl.ToolID, err)
		}
		n := tk.Count(text)
		sizes = append(sizes, n)
		total += n
	}
	if len(sizes) > 0 {
		shape.MeanDefinitionTokens = float64(total) / float64(len(sizes))
		sort.Ints(sizes)
		shape.P95DefinitionTokens = intPercentile(sizes, 95)
	}
	return shape, nil
}

// ValidateCounterfactual refuses a block that carries figures without its
// FR-004 label (T030/T031).
//
// It checks the label's SUBSTANCE, not merely its presence, because the
// failure mode is not an empty string — it is a well-meaning summary that
// replaces the caveat with something that reads better and claims more. A
// label must name itself a counterfactual and say the behaviour was not
// observed; a block with no figures needs no label at all.
func (b *ReplayBlock) ValidateCounterfactual() error {
	if b == nil {
		return errors.New("replay block: nil block cannot carry the FR-004 counterfactual label")
	}
	if len(b.Cells) == 0 && b.DirectDelta == nil {
		return nil
	}
	label := strings.ToLower(strings.TrimSpace(b.Counterfactual))
	if label == "" {
		return errors.New("replay block: figures were emitted without the FR-004 counterfactual label — " +
			"a replay recomputes recorded traffic and must never be presented as observed agent behaviour")
	}
	for _, marker := range []string{"counterfactual", "not observed"} {
		if !strings.Contains(label, marker) {
			return fmt.Errorf("replay block: the counterfactual label must state %q; got %q", marker, b.Counterfactual)
		}
	}
	return nil
}

// ValidateExclusionBalance asserts the FR-003 accounting closes: the sessions
// that were dropped account exactly for supplied minus used.
//
// Without this the exclusion table is decorative — present, plausible, and
// free to disagree with the numbers beside it. SC-008 asks for every exclusion
// to be counted, and a count nobody reconciles is how one goes missing.
func (b *ReplayBlock) ValidateExclusionBalance() error {
	if b == nil {
		return errors.New("replay block: nil block has no exclusion accounting")
	}
	dropping := map[string]bool{}
	for _, reason := range replayDroppingReasons {
		dropping[reason] = true
	}
	dropped := 0
	for _, ex := range b.Exclusions {
		if dropping[ex.Reason] {
			dropped += ex.Sessions
		}
	}
	if want := b.SessionsSupplied - b.SessionsUsed; dropped != want {
		return fmt.Errorf("replay block: %d sessions supplied minus %d used = %d, but the dropping exclusions account for %d",
			b.SessionsSupplied, b.SessionsUsed, want, dropped)
	}
	return nil
}

// ReplayReport wraps a replay block in the v2 report envelope.
//
// GeneratedAt is PINNED here rather than stamped (T029): a replay report's
// value is that two runs over the same inputs are byte-identical, and a
// wall-clock field defeats that check quietly. Corpora and Arms are present
// but empty — replay measures no encoding arms over a corpus; it crosses the
// mode matrix, which is why it is a block rather than one more OfflineSection.
func ReplayReport(tk *Tokenizer, block *ReplayBlock) *ReportV2 {
	return &ReportV2{
		ReportVersion: ReportVersion2,
		GeneratedAt:   ReplayGeneratedAt,
		Tokenizer:     TokenizerInfo{Name: tk.encoding, Caveat: TokenizerCaveatText},
		Corpora:       []CorpusDescriptor{},
		Arms:          []ArmResult{},
		Replay:        block,
		Provenance: map[string]string{
			// Section-level badges. The menu costs are measured; the delta is
			// arithmetic over them and carries its own per-row badge, which is
			// the whole reason per-row provenance exists (FR-013).
			"replay":              ProvenanceMeasured,
			"replay_direct_delta": ProvenanceComputed,
		},
	}
}
