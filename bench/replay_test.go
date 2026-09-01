package bench

// replay_test.go — Spec 103 US1 (T019/T020/T021/T030).
//
// These tests exist to hold four boundaries that the spec was corrected
// repeatedly to get right, and each of which is easy to cross by accident:
//
//  1. A recording alone computes NOTHING. A menu is a property of tool
//     definitions and the activity export carries no fleet snapshot, so
//     `-replay <jsonl>` without a fleet input is a hard error, never a
//     degraded run (T019).
//  2. No cell has an absolute complete-workload cost with bodies off, because
//     complete workload includes every consumed response and that text is
//     absent. Only the direct_full <-> direct_deferred delta survives, because
//     their call responses are identical and cancel out of the comparison
//     (T020).
//  3. Two runs over the same inputs are byte-identical once generated_at is
//     pinned (T021, SC-002).
//  4. No replay figure may be emitted without its counterfactual label; FR-004
//     forbids presenting a replay as observed agent behaviour (T030).
//
// The arms are fakes, as in armrun_test.go: package bench cannot import
// bench/arms (arms imports bench), so an arm reaches replay as a structural
// EncodingArm and the tests supply their own.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench/replaycorpus"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// newFakeDeferred stands in for the direct_deferred arm: a strictly smaller
// rendering than the baseline, so the direct delta is non-zero and signed the
// way a real deferral is.
func newFakeDeferred() *fakeArm {
	return &fakeArm{name: "direct_deferred", encode: func(t Tool) (string, error) { return t.Name, nil }}
}

// newFakeCompactSig stands in for the compact_sig arm. Replay must NOT use it
// to price a menu — the compact axis governs retrieve_tools RESPONSES, not the
// built-in tool listing — but it has to be resolvable, because the mode matrix
// names it as retrieve_compact's arm.
func newFakeCompactSig() *fakeArm {
	return &fakeArm{name: "compact_sig", encode: func(t Tool) (string, error) { return t.Name + "()", nil }}
}

func replayArms() map[string]EncodingArm {
	return map[string]EncodingArm{
		"baseline_json":   newFakeBaseline(),
		"direct_deferred": newFakeDeferred(),
		"compact_sig":     newFakeCompactSig(),
	}
}

// replayRecords is the fixture recording. It deliberately contains one of
// every shape the accounting has to survive:
//
//   - ws-1: two top-level calls, one of which issued a sandbox SUB-CALL that
//     carries no work_session_id of its own and joins by parent_id. A sum over
//     session.Calls would miss it; only AllCalls sees it.
//   - ws-2: a call against a tool that is not in the supplied fleet, so the
//     session is unreplayable and must be dropped AND counted.
//   - ws-3: a sensitive-flagged call, dropped and counted.
//   - a quarantine_change record, which is not a call at all and must not
//     inflate any unit of work.
func replayRecords() []contracts.ActivityRecord {
	at := func(sec int) time.Time { return time.Date(2026, 3, 1, 12, 0, sec, 0, time.UTC) }
	return []contracts.ActivityRecord{
		{
			ID: "a1", Type: contracts.ActivityTypeToolCall, Timestamp: at(1),
			WorkSessionID: "ws-1", RequestID: "req-1",
			ServerName: "fs", ToolName: "read_file", Status: "success",
			RequestBytes: 120, ResponseBytes: 4000,
		},
		{
			ID: "a2", Type: contracts.ActivityTypeToolCall, Timestamp: at(2),
			WorkSessionID: "ws-1", RequestID: "req-2",
			ServerName: "git", ToolName: "git_log", Status: "success",
			RequestBytes: 90, ResponseBytes: 2200,
		},
		{
			// Sandbox sub-call: no work session of its own, joins via parent_id,
			// and records both byte counts as zero the way production does.
			ID: "a3", Type: contracts.ActivityTypeToolCall, Timestamp: at(3),
			RequestID: "req-3", ParentID: "req-2",
			ServerName: "time", ToolName: "get_current_time", Status: "success",
		},
		{
			ID: "a4", Type: contracts.ActivityTypeToolCall, Timestamp: at(4),
			WorkSessionID: "ws-2", RequestID: "req-4",
			ServerName: "gone", ToolName: "vanished", Status: "success",
			RequestBytes: 10, ResponseBytes: 20,
		},
		{
			ID: "a5", Type: contracts.ActivityTypeToolCall, Timestamp: at(5),
			WorkSessionID: "ws-3", RequestID: "req-5",
			ServerName: "fs", ToolName: "read_file", Status: "success",
			RequestBytes: 10, ResponseBytes: 20, HasSensitiveData: true,
		},
		{
			ID: "a6", Type: contracts.ActivityTypeQuarantineChange, Timestamp: at(6),
			WorkSessionID: "ws-1",
		},
	}
}

// writeReplayRecording writes the fixture as activity JSONL into t.TempDir().
// The directory matters: the loader refuses an input path inside the
// repository working tree, because replay inputs are raw recorded traffic and
// must never be committed.
func writeReplayRecording(t *testing.T, records []contracts.ActivityRecord) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	var buf strings.Builder
	for i := range records {
		line, err := json.Marshal(records[i])
		if err != nil {
			t.Fatalf("marshal record %d: %v", i, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	return path
}

func replayOptions(t *testing.T) ReplayOptions {
	t.Helper()
	return ReplayOptions{
		RecordingPath: writeReplayRecording(t, replayRecords()),
		Fleet:         runnerCorpus(),
		FleetID:       "corpus_test@1",
		Arms:          replayArms(),
		Warnf:         func(string, ...any) {},
	}
}

// T019 — a recording-only invocation is an ERROR, not a degraded run. Without
// a fleet there is no menu to cost, so there is nothing to degrade to.
func TestRunReplay_RecordingWithoutFleetIsHardError(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)
	opts.Fleet = nil

	block, err := RunReplay(tk, opts)
	if err == nil {
		t.Fatal("replay with no fleet input must fail: a menu is a property of the tool definitions, and the export carries no fleet snapshot")
	}
	if !errors.Is(err, ErrReplayFleetRequired) {
		t.Fatalf("want ErrReplayFleetRequired, got %v", err)
	}
	if block != nil {
		t.Fatalf("a refused replay must return no block, got %+v", block)
	}
}

// A fleet with no tools is the same failure wearing a different hat.
func TestRunReplay_EmptyFleetIsHardError(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)
	opts.Fleet = &Corpus{Version: "empty"}

	if _, err := RunReplay(tk, opts); !errors.Is(err, ErrReplayFleetRequired) {
		t.Fatalf("want ErrReplayFleetRequired for an empty fleet, got %v", err)
	}
}

// T020 — bodies off, NO cell may report an absolute complete-workload cost,
// and the only cross-mode figure produced is the direct_full <-> direct_deferred
// delta.
func TestRunReplay_NoAbsoluteWorkloadCostBodiesOff(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}

	if block.BodiesIncluded {
		t.Fatal("bodies-off is the default posture; the block must say so")
	}
	if len(block.Cells) != len(ModeCells()) {
		t.Fatalf("want one row per mode cell (%d), got %d", len(ModeCells()), len(block.Cells))
	}
	for _, cell := range block.Cells {
		if !cell.AbsoluteWorkloadWithheld {
			t.Errorf("cell %s: an absolute complete-workload cost is NOT available bodies-off — every consumed response is missing", cell.CellID)
		}
		if strings.TrimSpace(cell.WithheldReason) == "" {
			t.Errorf("cell %s: a withheld figure must carry its reason, never render as zero", cell.CellID)
		}
		if cell.ResponseTokens != nil {
			t.Errorf("cell %s: response tokens must be absent bodies-off (byte lengths are not token counts), got %d", cell.CellID, *cell.ResponseTokens)
		}
		if cell.MenuTokens <= 0 {
			t.Errorf("cell %s: menu cost IS measurable from the fleet input and must be populated", cell.CellID)
		}
		if cell.Provenance != ProvenanceMeasured {
			t.Errorf("cell %s: menu cost is counted by the deterministic tokenizer over the supplied fleet, want %q, got %q", cell.CellID, ProvenanceMeasured, cell.Provenance)
		}
	}

	if block.DirectDelta == nil {
		t.Fatal("the direct_full <-> direct_deferred delta IS measurable bodies-off (identical call responses cancel) and is the honest headline")
	}
	if block.DirectDelta.FromCellID != CellDirectFull || block.DirectDelta.ToCellID != CellDirectDeferred {
		t.Errorf("delta must be %s -> %s, got %s -> %s", CellDirectFull, CellDirectDeferred, block.DirectDelta.FromCellID, block.DirectDelta.ToCellID)
	}
	if block.DirectDelta.Provenance != ProvenanceComputed {
		t.Errorf("the delta is arithmetic over two measured menu costs, want %q, got %q", ProvenanceComputed, block.DirectDelta.Provenance)
	}
	if block.DirectDelta.DeltaTokens <= 0 {
		t.Errorf("fixture deferral renders strictly less than the baseline; want a positive delta, got %d", block.DirectDelta.DeltaTokens)
	}

	// The retrieve cells must state that no cross-mode delta is available to
	// them either — their serialization changes the RESPONSE body, which is
	// exactly the text bodies-off does not have.
	for _, cell := range block.Cells {
		if cell.CellID != CellRetrieveFull && cell.CellID != CellRetrieveCompact {
			continue
		}
		if !strings.Contains(cell.WithheldReason, "delta") {
			t.Errorf("cell %s: the withheld reason must say why no cross-mode delta is available, got %q", cell.CellID, cell.WithheldReason)
		}
	}
}

// Sub-calls hang off their parent and are absent from session.Calls by design.
// A replay that walked Calls would silently undercount the recorded workload.
func TestRunReplay_CountsSubCalls(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	for _, cell := range block.Cells {
		if cell.Calls != 3 {
			t.Fatalf("cell %s: want 3 recorded calls (2 top-level + 1 sandbox sub-call), got %d", cell.CellID, cell.Calls)
		}
	}
}

// Every exclusion is counted and reported (FR-003, SC-008), and the sessions
// that were DROPPED account exactly for supplied minus used.
func TestRunReplay_ExclusionsAccountForEverySession(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}

	if block.SessionsSupplied != 3 {
		t.Errorf("want 3 supplied sessions (ws-1, ws-2, ws-3), got %d", block.SessionsSupplied)
	}
	if block.SessionsUsed != 1 {
		t.Errorf("want 1 used session (ws-1), got %d", block.SessionsUsed)
	}
	if err := block.ValidateExclusionBalance(); err != nil {
		t.Errorf("exclusion accounting does not balance: %v", err)
	}

	counts := map[string]int{}
	for _, ex := range block.Exclusions {
		counts[ex.Reason] = ex.Sessions
	}
	if counts[ReplayExclusionSensitive] != 1 {
		t.Errorf("want 1 session dropped as sensitive, got %d", counts[ReplayExclusionSensitive])
	}
	if counts[ReplayExclusionUnreplayable] != 1 {
		t.Errorf("want 1 session dropped as unreplayable, got %d", counts[ReplayExclusionUnreplayable])
	}
	if counts[ReplayExclusionBodiesMissed] != 3 {
		t.Errorf("bodies-off flags every supplied session; want 3, got %d", counts[ReplayExclusionBodiesMissed])
	}
	if counts[ReplayExclusionUnattributed] != 1 {
		t.Errorf("the quarantine_change record belongs to no unit of work; want 1, got %d", counts[ReplayExclusionUnattributed])
	}
	if !block.SensitiveFlagBestEffort {
		t.Error("the sensitive flag is derived from detection metadata written asynchronously AFTER persistence; the report must restate that it is a reducer, not a guarantee")
	}
}

// Replay ALWAYS has a fleet input, so the loader must always have been able to
// evaluate replayability. FleetChecked == false is a bug in this package's own
// wiring, not a property of the data — and it would silently turn "nothing was
// unreplayable" into "replayability was never evaluated".
func TestRunReplay_AlwaysResolvesTheFleet(t *testing.T) {
	opts := replayOptions(t)
	corpus, err := replaycorpus.LoadFile(opts.RecordingPath, replaycorpus.Options{
		FleetResolver: FleetResolver(opts.Fleet),
		Warnf:         func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !corpus.FleetChecked {
		t.Fatal("FleetResolver must be wired for every replay load; an unchecked fleet reports 'nothing unreplayable' when it means 'never evaluated'")
	}
}

// T021 — two runs over the same inputs are byte-identical (SC-002). The whole
// point of pinning generated_at is that this check means something.
func TestRunReplay_ByteIdenticalAcrossRuns(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)

	render := func() []byte {
		block, err := RunReplay(tk, opts)
		if err != nil {
			t.Fatalf("RunReplay: %v", err)
		}
		report := ReplayReport(tk, block)
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return data
	}

	first, second := render(), render()
	if string(first) != string(second) {
		t.Fatal("two replay runs over identical inputs must be byte-identical")
	}
	if !strings.Contains(string(first), `"generated_at": "`+ReplayGeneratedAt+`"`) {
		t.Fatalf("replay reports must pin generated_at to %q so SC-002's determinism check is meaningful; got:\n%s", ReplayGeneratedAt, first)
	}
	if ReplayGeneratedAt == GeneratedAtNow() {
		t.Fatal("the pinned stamp must not be a wall-clock time")
	}
}

// T030 — no replay figure may be emitted without the counterfactual marker.
func TestReplayBlock_RefusesFigureWithoutCounterfactualMarker(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	if err := block.ValidateCounterfactual(); err != nil {
		t.Fatalf("a block built by RunReplay must already carry its label: %v", err)
	}

	stripped := *block
	stripped.Counterfactual = ""
	if err := stripped.ValidateCounterfactual(); err == nil {
		t.Fatal("a block carrying cells or a delta must be refused without its counterfactual label (FR-004)")
	}

	mislabelled := *block
	mislabelled.Counterfactual = "measured over a live agent session"
	if err := mislabelled.ValidateCounterfactual(); err == nil {
		t.Fatal("a label that presents replay as observed behaviour must be refused (FR-004)")
	}
}

// T031's substance: the label must say the three things a reader needs — that
// the figures are a counterfactual, that no agent behaviour was observed, and
// that the recorded work is scored against the SUPPLIED fleet rather than the
// fleet as it stood when the session was recorded.
func TestReplayCounterfactualLabel_SaysWhatItMustSay(t *testing.T) {
	label := strings.ToLower(ReplayCounterfactualLabel)
	for _, want := range []string{"counterfactual", "not observed", "recorded", "supplied fleet"} {
		if !strings.Contains(label, want) {
			t.Errorf("the counterfactual label must mention %q; got: %s", want, ReplayCounterfactualLabel)
		}
	}
}

// The replay block must satisfy the same emission-time contract as every other
// additive block: a populated accounting source and a provenance on every row.
func TestRunReplay_BlockPassesAdditiveBlockValidation(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	if block.AccountingSource.Kind != AccountingKindTokenizer {
		t.Errorf("replay is counted by the deterministic tokenizer, want kind %q, got %q", AccountingKindTokenizer, block.AccountingSource.Kind)
	}
	if err := ReplayReport(tk, block).ValidateAdditiveBlocks(); err != nil {
		t.Fatalf("ValidateAdditiveBlocks: %v", err)
	}
}

// The mode matrix names an arm per cell; a missing arm is a wiring bug and
// must fail loudly rather than quietly omitting a row.
func TestRunReplay_MissingArmIsAnError(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)
	delete(opts.Arms, "direct_deferred")

	if _, err := RunReplay(tk, opts); err == nil {
		t.Fatal("a cell whose arm was not supplied must fail the run, not disappear from the report")
	}
}

// The fleet input decides the menu for the DIRECT cells (there the menu is the
// whole upstream fleet) and the built-in proxy catalog for the others. The
// discovery-serialization axis is invisible bodies-off, because it governs
// retrieve_tools RESPONSES rather than the built-in listing — so the two
// retrieve cells must cost the same, and that sameness is a finding, not a bug.
func TestRunReplay_MenuCostsFollowTheSurface(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	menu := map[string]int{}
	for _, cell := range block.Cells {
		menu[cell.CellID] = cell.MenuTokens
	}
	if menu[CellRetrieveFull] != menu[CellRetrieveCompact] {
		t.Errorf("the compact axis governs retrieve_tools responses, not the built-in menu; want equal menu costs, got %d vs %d",
			menu[CellRetrieveFull], menu[CellRetrieveCompact])
	}
	if menu[CellDirectDeferred] >= menu[CellDirectFull] {
		t.Errorf("deferred direct rendering must cost less than full: %d vs %d", menu[CellDirectDeferred], menu[CellDirectFull])
	}
	if block.FleetShape.ToolCount != len(liveFleetCorpus().Tools) {
		t.Errorf("the fleet shape travels with every figure; want %d tools, got %d", len(runnerCorpus().Tools), block.FleetShape.ToolCount)
	}
}

// T032 — the dashboard must render the replay block, and must put the
// exclusion report BEFORE the headline. Ordering is not decoration here: a
// figure computed over a third of the supplied sessions reads as a figure over
// all of them unless the reader meets the exclusions first.
func TestReplayDashboard_RendersExclusionsBeforeTheHeadline(t *testing.T) {
	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	path := filepath.Join(t.TempDir(), "dashboard.html")
	if err := ReplayReport(tk, block).WriteHTML(path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	html := string(raw)

	for _, want := range []string{
		"What did not count",
		"Per-cell cost",
		"COUNTERFACTUAL",
		"not observed agent behaviour",
		"today's fleet, not the fleet as it stood when the sessions were recorded",
		"withheld",
		CellDirectDeferred,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}

	exclusions := strings.Index(html, "What did not count")
	headline := strings.Index(html, "Per-cell cost")
	if exclusions < 0 || headline < 0 || exclusions > headline {
		t.Errorf("the exclusion report must be readable BEFORE the per-cell headline (exclusions at %d, headline at %d)", exclusions, headline)
	}
	if strings.Contains(html, "http://") || strings.Contains(html, "https://") {
		t.Error("the dashboard must stay self-contained: no external resource loads")
	}
}

// The replay block must validate against the reviewed contract schema, not
// merely against this package's own expectations. The schema has no
// additionalProperties:false and cannot require a field to be non-empty, so
// this check catches the shape errors the Go types cannot — a reason outside
// the closed exclusion enum above all.
func TestReplayReport_SchemaValidationPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command(python, "-c", "import jsonschema").Run(); err != nil {
		t.Skip("python3 jsonschema module not available")
	}

	tk := runnerTokenizer(t)
	block, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	dir := t.TempDir()
	reportPath, err := ReplayReport(tk, block).WriteJSON(dir)
	if err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	schemaPath := filepath.Clean("../specs/083-discovery-profiler/contracts/report-v2.schema.json")

	script := fmt.Sprintf(`
import json, jsonschema
schema = json.load(open(%q))
report = json.load(open(%q))
jsonschema.validate(report, schema)
`, schemaPath, reportPath)
	if out, err := exec.Command(python, "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("jsonschema validation failed: %v\n%s", err, out)
	}
}

// The withheld reason must track the POSTURE, not just the cell.
//
// This is the defect the adversarial verification pass caught: the reason was a
// pure function of the cell, so a bodies-on run printed "that text is absent
// from a bodies-off replay" in the very row that carried a response figure
// proving otherwise. A caveat that states a false premise is worse than none —
// a reader who checks it against the number loses trust in both.
func TestRunReplay_WithheldReasonTracksTheBodiesPosture(t *testing.T) {
	tk := runnerTokenizer(t)
	off, err := RunReplay(tk, replayOptions(t))
	if err != nil {
		t.Fatalf("bodies-off replay: %v", err)
	}
	for _, cell := range off.Cells {
		if !strings.Contains(cell.WithheldReason, "absent from a bodies-off replay") {
			t.Errorf("cell %s: bodies-off reason must say the text is absent, got %q", cell.CellID, cell.WithheldReason)
		}
	}

	optsOn := replayOptions(t)
	optsOn.Bodies = replaycorpus.BodiesOnUnmasked
	on, err := RunReplay(tk, optsOn)
	if err != nil {
		t.Fatalf("bodies-on replay: %v", err)
	}
	for _, cell := range on.Cells {
		if strings.Contains(cell.WithheldReason, "absent from a bodies-off replay") {
			t.Errorf("cell %s: a bodies-on run must NOT claim the response text is absent; got %q", cell.CellID, cell.WithheldReason)
		}
		if !cell.AbsoluteWorkloadWithheld {
			t.Errorf("cell %s: an absolute complete-workload cost stays withheld even bodies-on", cell.CellID)
		}
		if strings.TrimSpace(cell.WithheldReason) == "" {
			t.Errorf("cell %s: a withheld figure must always carry a reason", cell.CellID)
		}
	}
}

// SC-008: a cost suppressed INSIDE an admitted session must still be reported.
//
// The session-level exclusion rows cannot express this — they only count units
// of work that never got in. Without the loader-level accounting a withheld
// response cost collapses a cell's response total to nil and nothing anywhere
// says why, which is the silent accounting the criterion forbids.
func TestRunReplay_SurfacesLoaderLevelAccounting(t *testing.T) {
	tk := runnerTokenizer(t)
	at := func(sec int) time.Time { return time.Date(2026, 3, 1, 12, 0, sec, 0, time.UTC) }
	records := append(replayRecords(), contracts.ActivityRecord{
		// An internal record whose stored response is LARGER than the one the
		// agent consumed: admitted to its session, but its cost is withheld
		// because tokenizing the stored text would overstate what was paid.
		ID:                "a-trunc",
		Type:              contracts.ActivityType("internal_tool_call"),
		Timestamp:         at(7),
		WorkSessionID:     "ws-1",
		ToolName:          "retrieve_tools",
		Status:            "success",
		Response:          "a stored response larger than the agent received",
		ResponseTruncated: true,
		ResponseBytes:     4096,
	})

	opts := replayOptions(t)
	opts.RecordingPath = writeReplayRecording(t, records)
	opts.Bodies = replaycorpus.BodiesOnUnmasked

	block, err := RunReplay(tk, opts)
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	acc := block.LoaderAccounting
	if acc == nil {
		t.Fatal("loader accounting must be reported when the loader withheld, dropped or flagged anything")
	}
	if acc.CostsWithheld == 0 {
		t.Errorf("a truncated internal record must be counted as a withheld cost, got %+v", acc)
	}
	if len(acc.Withheld) == 0 {
		t.Error("a withheld cost must carry its reason, not just a total — the total alone is not actionable")
	}
}

// --- Spec 103: the live fleet source ---------------------------------------
//
// A replay may take its fleet from a running proxy instead of a frozen corpus
// (bench/mcptools.go). These three tests hold the invocation rules AT THE
// RunReplay boundary, because that is where a caller meets them: the mapping
// and stub-guard arithmetic is covered in mcptools_test.go.

// liveFleetCorpus is the shape a LIVE surface actually serves: real input
// schemas on every tool. runnerCorpus() is the frozen schema-less shape, which
// the stub guard refuses off a live source by design.
func liveFleetCorpus() *Corpus {
	return &Corpus{
		Version: "live:127.0.0.1:18421/mcp/all",
		Tools: []Tool{
			{ToolID: "fs:read_file", Server: "fs", Name: "read_file",
				Description: "Read the contents of a file from disk",
				Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
			{ToolID: "git:git_log", Server: "git", Name: "git_log",
				Description: "Show recent commit history of a repository",
				Schema:      json.RawMessage(`{"type":"object","properties":{"repo_path":{"type":"string"},"max_count":{"type":"integer","default":10}},"required":["repo_path"]}`)},
			{ToolID: "time:get_current_time", Server: "time", Name: "get_current_time",
				Description: "Get current time in a specific timezone",
				Schema:      json.RawMessage(`{"type":"object","properties":{"timezone":{"type":"string"}}}`)},
		},
	}
}

// A live fleet drives a complete replay, and the block is quoted for the live
// fleet's id — not the corpus version it never had.
func TestRunReplay_AcceptsALiveFleetSource(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)
	opts.Fleet, opts.FleetID = nil, ""
	// A real live fleet carries real schemas — runnerCorpus() is the frozen
	// schema-less shape, which the stub guard rightly refuses off a live
	// surface. Use a schema-bearing fleet so this exercises the happy path
	// rather than the guard.
	opts.LiveFleet = &fakeFleetSource{id: "live:127.0.0.1:18421/mcp/all", corpus: liveFleetCorpus()}

	block, err := RunReplay(tk, opts)
	if err != nil {
		t.Fatalf("a live fleet must be a valid fleet input: %v", err)
	}
	if block.FleetShape.ID != "live:127.0.0.1:18421/mcp/all" {
		t.Errorf("the block must be quoted for the live fleet id, got %q", block.FleetShape.ID)
	}
	if block.FleetShape.ToolCount != len(liveFleetCorpus().Tools) {
		t.Errorf("live fleet tool count not carried through: %d", block.FleetShape.ToolCount)
	}
	for _, cell := range block.Cells {
		if cell.MenuTokens <= 0 {
			t.Errorf("cell %s priced no menu over the live fleet", cell.CellID)
		}
	}
}

// Two fleets is an error at the RunReplay boundary too: a report carries ONE
// fleet_id, and preferring one input silently would make it false.
func TestRunReplay_CorpusAndLiveProxyTogetherIsAnError(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t) // already carries a frozen corpus
	opts.LiveFleet = &fakeFleetSource{id: "live:x", corpus: runnerCorpus()}

	block, err := RunReplay(tk, opts)
	if !errors.Is(err, ErrReplayFleetAmbiguous) {
		t.Fatalf("want ErrReplayFleetAmbiguous, got %v", err)
	}
	if block != nil {
		t.Fatalf("a refused replay must return no block, got %+v", block)
	}
}

// The live source ADDS a way to supply a fleet. It does not make one optional:
// a nil source is still no fleet.
func TestRunReplay_NilLiveFleetLeavesTheFleetMandatory(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)
	opts.Fleet, opts.FleetID, opts.LiveFleet = nil, "", nil

	if _, err := RunReplay(tk, opts); !errors.Is(err, ErrReplayFleetRequired) {
		t.Fatalf("want ErrReplayFleetRequired, got %v", err)
	}
}

// A live fleet whose schemas are stubbed must abort the whole run.
//
// The previous version of this test injected ErrStubToolSchemas from a fake
// source, which proved only that RunReplay propagates an error it was handed —
// it passed with guardToolSchemas deleted entirely. This one feeds REAL stubbed
// tool definitions through the real guard, so deleting the guard fails it.
func TestRunReplay_StubSchemaFleetRefusesTheWholeRun(t *testing.T) {
	tk := runnerTokenizer(t)
	opts := replayOptions(t)
	opts.Fleet, opts.FleetID = nil, ""
	opts.LiveFleet = &fakeFleetSource{
		id: "live:deferred",
		corpus: &Corpus{
			Version: "live:deferred",
			Tools: []Tool{
				// Exactly what /mcp/all serves under
				// direct_tool_response_mode:"deferred": the schema is folded
				// into the description and replaced by a bare placeholder.
				{ToolID: "git:git_log", Server: "git", Name: "git_log",
					Description: "Shows the commit logs",
					Schema:      json.RawMessage(`{"type":"object"}`)},
				{ToolID: "fs:read_file", Server: "fs", Name: "read_file",
					Description: "Read a file",
					Schema:      json.RawMessage(`{"type":"object"}`)},
			},
		},
	}

	if _, err := RunReplay(tk, opts); !errors.Is(err, ErrStubToolSchemas) {
		t.Fatalf("a stub-schema live fleet must abort the replay, got %v", err)
	}
}

// A PARTIALLY stubbed fleet passes the guard — the guard only refuses an
// all-stub population — so the report must at least record how much of it could
// not be priced.
//
// Without this the difference between a clean 45-tool fleet and one where 20
// tools lost their schemas is invisible in the output, while the second quietly
// shrinks the baseline and inflates the headline.
func TestRunReplay_CountsPartiallyStubbedFleet(t *testing.T) {
	tk := runnerTokenizer(t)
	partial := liveFleetCorpus()
	// One of the three loses its schema: a partial regression, not a stubbed
	// surface, so the run proceeds.
	partial.Tools[1].Schema = json.RawMessage(`{"type":"object"}`)

	opts := replayOptions(t)
	opts.Fleet, opts.FleetID = nil, ""
	opts.LiveFleet = &fakeFleetSource{id: "live:partial", corpus: partial}

	block, err := RunReplay(tk, opts)
	if err != nil {
		t.Fatalf("a partially stubbed fleet still runs: %v", err)
	}
	if block.FleetShape.SchemalessTools != 1 {
		t.Errorf("the report must record the unpriceable remainder; want 1, got %d",
			block.FleetShape.SchemalessTools)
	}
	if block.FleetShape.ToolCount != 3 {
		t.Errorf("tool count %d", block.FleetShape.ToolCount)
	}
}
