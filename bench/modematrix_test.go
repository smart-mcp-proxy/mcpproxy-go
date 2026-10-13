package bench

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 103 US? — T012/T013: the mode matrix
// (specs/103-token-bench/contracts/mode-matrix.md).
//
// The contract's load-bearing claim is arithmetic: the naive 3-axis product is
// 3 routing modes x 2 discovery serializations x 2 direct serializations = 12
// combinations, and those 12 collapse onto 5 distinct behaviours. The other 7
// are configurable and behaviourally redundant — NOT impossible — so each must
// surface as a skip row naming the cell it collapses onto, never as a zero and
// never as a missing row (FR-017).
//
// These tests pin all three halves of that claim: the 5 cells and their
// not-applicable axes, the 7 collapses with their reason codes, and the FR-016
// capability toggles as binary conditions over the cells where each is
// available rather than as a fourth axis.

// allValidCellIDs is the expected cell-id set in contract-table order. Cell ids
// are a stability contract (FR-028): a run from a later release must remain
// comparable to an earlier one, so this list is deliberately hard-coded rather
// than derived from the implementation.
var allValidCellIDs = []string{
	CellRetrieveFull,
	CellRetrieveCompact,
	CellDirectFull,
	CellDirectDeferred,
	CellCodeExec,
}

func TestModeCellsAreExactlyFiveDistinctBehaviours(t *testing.T) {
	cells := ModeCells()
	if len(cells) != 5 {
		t.Fatalf("ModeCells() returned %d cells, want exactly 5 distinct behaviours", len(cells))
	}

	seen := map[string]bool{}
	for i, c := range cells {
		if c.ID != allValidCellIDs[i] {
			t.Errorf("cell %d: id = %q, want %q (cell ids are stable, FR-028)", i, c.ID, allValidCellIDs[i])
		}
		if seen[c.ID] {
			t.Errorf("cell id %q appears twice; the 5 cells must be distinct", c.ID)
		}
		seen[c.ID] = true
		if c.Skipped {
			t.Errorf("cell %q is a valid cell and must not be marked skipped", c.ID)
		}
		if c.CollapsesOnto != "" {
			t.Errorf("cell %q is a valid cell and must not collapse onto %q", c.ID, c.CollapsesOnto)
		}
	}
}

func TestBaselineIsNotAnMcpproxyMode(t *testing.T) {
	b := BaselineCell()
	if b.ID != CellBaseline {
		t.Fatalf("BaselineCell().ID = %q, want %q", b.ID, CellBaseline)
	}
	for _, c := range ModeCells() {
		if c.ID == CellBaseline {
			t.Fatalf("baseline must not appear among the 5 mcpproxy cells")
		}
	}
	if b.Endpoint != "" {
		t.Errorf("baseline endpoint = %q, want empty: baseline loads every upstream tool inline, it is served by no mcpproxy surface", b.Endpoint)
	}
	if _, err := b.EndpointURL("http://127.0.0.1:8080"); err == nil {
		t.Errorf("BaselineCell().EndpointURL() returned no error; the baseline has no endpoint to address")
	}
}

// TestIgnoredAxesAreNotApplicableNeverDefaults pins contract rule 3: an axis
// with no consumer on a cell's surface is recorded as not-applicable, never as
// its default value — a default would imply a measurement that was never taken.
func TestIgnoredAxesAreNotApplicableNeverDefaults(t *testing.T) {
	want := map[string]struct{ discovery, direct string }{
		CellRetrieveFull:    {SerializationFull, SerializationNotApplicable},
		CellRetrieveCompact: {SerializationCompact, SerializationNotApplicable},
		CellDirectFull:      {SerializationNotApplicable, SerializationFull},
		CellDirectDeferred:  {SerializationNotApplicable, SerializationDeferred},
		// code_exec is the one cell where "full" is an override, not a default:
		// the surface overwrites the response mode and blanks the detail param.
		CellCodeExec: {SerializationFull, SerializationNotApplicable},
	}
	for _, c := range ModeCells() {
		w, ok := want[c.ID]
		if !ok {
			t.Fatalf("unexpected cell %q", c.ID)
		}
		if c.DiscoverySerialization != w.discovery {
			t.Errorf("cell %q: discovery serialization = %q, want %q", c.ID, c.DiscoverySerialization, w.discovery)
		}
		if c.DirectSerialization != w.direct {
			t.Errorf("cell %q: direct serialization = %q, want %q", c.ID, c.DirectSerialization, w.direct)
		}
	}
}

// TestProductIsTwelveCombinationsFiveDistinct walks the FULL naive product and
// asserts the collapse arithmetic end to end: 12 configurable combinations,
// 5 distinct behaviours, 7 skip rows.
func TestProductIsTwelveCombinationsFiveDistinct(t *testing.T) {
	routing := []string{config.RoutingModeRetrieveTools, config.RoutingModeDirect, config.RoutingModeCodeExecution}
	discovery := []string{config.ToolResponseModeFull, config.ToolResponseModeCompact}
	direct := []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred}

	distinct := map[string]bool{}
	skips := 0
	total := 0
	for _, r := range routing {
		for _, d := range discovery {
			for _, dd := range direct {
				total++
				got := ResolveCombination(Combination{
					RoutingMode:            r,
					ToolResponseMode:       d,
					DirectToolResponseMode: dd,
					// code_execution is enabled throughout: the degenerate
					// configuration is a separate case, tested below.
					EnableCodeExecution: true,
				})
				if got.Skipped {
					skips++
					if got.CollapsesOnto == "" {
						t.Errorf("%s/%s/%s: skipped row carries no collapse target", r, d, dd)
					}
					if _, ok := CellByID(got.CollapsesOnto); !ok {
						t.Errorf("%s/%s/%s: collapses onto unknown cell %q", r, d, dd, got.CollapsesOnto)
					}
					continue
				}
				distinct[got.ID] = true
			}
		}
	}

	if total != 12 {
		t.Fatalf("walked %d combinations, want 12", total)
	}
	if len(distinct) != 5 {
		t.Errorf("resolved %d distinct behaviours (%v), want 5", len(distinct), sortedKeys(distinct))
	}
	if skips != 7 {
		t.Errorf("produced %d skip rows, want 7", skips)
	}
}

// TestRedundantCombinationsNameTheirCollapse pins the contract's collapse table
// row for row, including the one combination that carries TWO reason codes.
func TestRedundantCombinationsNameTheirCollapse(t *testing.T) {
	cases := []struct {
		routing, discovery, direct string
		collapsesOnto              string
		reasons                    []string
	}{
		{config.RoutingModeRetrieveTools, "full", "deferred", CellRetrieveFull, []string{SkipReasonAxisIgnored}},
		{config.RoutingModeRetrieveTools, "compact", "deferred", CellRetrieveCompact, []string{SkipReasonAxisIgnored}},
		{config.RoutingModeDirect, "compact", "full", CellDirectFull, []string{SkipReasonAxisIgnored}},
		{config.RoutingModeDirect, "compact", "deferred", CellDirectDeferred, []string{SkipReasonAxisIgnored}},
		{config.RoutingModeCodeExecution, "compact", "full", CellCodeExec, []string{SkipReasonForcedFull}},
		{config.RoutingModeCodeExecution, "full", "deferred", CellCodeExec, []string{SkipReasonAxisIgnored}},
		{config.RoutingModeCodeExecution, "compact", "deferred", CellCodeExec, []string{SkipReasonForcedFull, SkipReasonAxisIgnored}},
	}

	if got := len(RedundantCombinations()); got != len(cases) {
		t.Fatalf("RedundantCombinations() returned %d rows, want %d", got, len(cases))
	}

	for _, tc := range cases {
		name := tc.routing + "/" + tc.discovery + "/" + tc.direct
		got := ResolveCombination(Combination{
			RoutingMode:            tc.routing,
			ToolResponseMode:       tc.discovery,
			DirectToolResponseMode: tc.direct,
			EnableCodeExecution:    true,
		})
		if !got.Skipped {
			t.Errorf("%s: want a skip row, got valid cell %q", name, got.ID)
			continue
		}
		if got.CollapsesOnto != tc.collapsesOnto {
			t.Errorf("%s: collapses onto %q, want %q", name, got.CollapsesOnto, tc.collapsesOnto)
		}
		if strings.Join(got.SkipReasonCodes, "+") != strings.Join(tc.reasons, "+") {
			t.Errorf("%s: reason codes %v, want %v", name, got.SkipReasonCodes, tc.reasons)
		}
	}
}

// TestDegenerateCodeExecutionConfiguration: code_execution with
// enable_code_execution:false is not part of the product — the surface can
// discover tools and call none of them — so it is skipped as `degenerate` and
// collapses onto nothing.
func TestDegenerateCodeExecutionConfiguration(t *testing.T) {
	got := ResolveCombination(Combination{
		RoutingMode:            config.RoutingModeCodeExecution,
		ToolResponseMode:       config.ToolResponseModeFull,
		DirectToolResponseMode: config.DirectToolResponseModeFull,
		EnableCodeExecution:    false,
	})
	if !got.Skipped {
		t.Fatalf("code_execution with enable_code_execution:false resolved to valid cell %q, want a skip row", got.ID)
	}
	if len(got.SkipReasonCodes) != 1 || got.SkipReasonCodes[0] != SkipReasonDegenerate {
		t.Errorf("reason codes = %v, want [%s]", got.SkipReasonCodes, SkipReasonDegenerate)
	}
	if got.CollapsesOnto != "" {
		t.Errorf("collapses onto %q, want empty: a degenerate configuration is not a redundant one", got.CollapsesOnto)
	}

	// The degenerate verdict outranks redundancy: with the surface disabled the
	// serialization axes cannot change anything.
	also := ResolveCombination(Combination{
		RoutingMode:            config.RoutingModeCodeExecution,
		ToolResponseMode:       config.ToolResponseModeCompact,
		DirectToolResponseMode: config.DirectToolResponseModeDeferred,
		EnableCodeExecution:    false,
	})
	if len(also.SkipReasonCodes) != 1 || also.SkipReasonCodes[0] != SkipReasonDegenerate {
		t.Errorf("disabled code_execution with redundant axes: reason codes = %v, want [%s]", also.SkipReasonCodes, SkipReasonDegenerate)
	}
}

// TestSkipRowReusesExistingArmResultShape pins contract rule 4: skip rows go
// through the EXISTING skipped-row shape (ArmResult.Skipped/SkipReason via
// SkippedArmResult), and the rendered reason names both the code and the cell
// collapsed onto so a reader never has to infer a zero.
func TestSkipRowReusesExistingArmResultShape(t *testing.T) {
	skip := ResolveCombination(Combination{
		RoutingMode:            config.RoutingModeCodeExecution,
		ToolResponseMode:       config.ToolResponseModeCompact,
		DirectToolResponseMode: config.DirectToolResponseModeDeferred,
		EnableCodeExecution:    true,
	})
	row := skip.SkipRow("corpus_v2@test")

	if !row.Skipped {
		t.Errorf("ArmResult.Skipped = false, want true")
	}
	if row.Arm != skip.ID {
		t.Errorf("ArmResult.Arm = %q, want the cell id %q", row.Arm, skip.ID)
	}
	if row.CorpusID != "corpus_v2@test" {
		t.Errorf("ArmResult.CorpusID = %q, want %q", row.CorpusID, "corpus_v2@test")
	}
	for _, code := range skip.SkipReasonCodes {
		if !strings.Contains(row.SkipReason, code) {
			t.Errorf("SkipReason %q does not name reason code %q", row.SkipReason, code)
		}
	}
	if !strings.Contains(row.SkipReason, skip.CollapsesOnto) {
		t.Errorf("SkipReason %q does not name the cell it collapses onto (%q)", row.SkipReason, skip.CollapsesOnto)
	}

	// Identical to the existing constructor: no second skip shape exists.
	if want := SkippedArmResult(skip.ID, "corpus_v2@test", skip.SkipReasonText()); !reflect.DeepEqual(row, want) {
		t.Errorf("SkipRow() = %+v, want SkippedArmResult(...) = %+v", row, want)
	}
}

// --- T015: endpoint mapping -------------------------------------------------

// TestCellEndpointURL pins contract rule 1/2: the routing-mode axis is selected
// by URL alone — all three routing-mode endpoints stay mounted regardless of
// config — while the serialization axes are config, which is why two cells can
// legitimately share one URL.
func TestCellEndpointURL(t *testing.T) {
	const base = "http://127.0.0.1:18080"
	want := map[string]string{
		CellRetrieveFull:    base + "/mcp/call",
		CellRetrieveCompact: base + "/mcp/call",
		CellDirectFull:      base + "/mcp/all",
		CellDirectDeferred:  base + "/mcp/all",
		CellCodeExec:        base + "/mcp/code",
	}
	for _, c := range ModeCells() {
		got, err := c.EndpointURL(base)
		if err != nil {
			t.Fatalf("cell %q: EndpointURL: %v", c.ID, err)
		}
		if got != want[c.ID] {
			t.Errorf("cell %q: endpoint URL = %q, want %q", c.ID, got, want[c.ID])
		}
	}

	// A trailing slash on the base must not double up.
	got, err := ModeCells()[0].EndpointURL(base + "/")
	if err != nil {
		t.Fatalf("EndpointURL with trailing slash: %v", err)
	}
	if got != want[CellRetrieveFull] {
		t.Errorf("trailing-slash base produced %q, want %q", got, want[CellRetrieveFull])
	}

	if _, err := ModeCells()[0].EndpointURL(""); err == nil {
		t.Errorf("EndpointURL(\"\") returned no error; a cell cannot be addressed without a base URL")
	}
}

// TestSerializationCellsShareOneEndpoint states the operational consequence
// tested above as its own claim: the matrix crosses on ONE long-lived instance,
// with a config apply — not a restart — between serialization cells.
func TestSerializationCellsShareOneEndpoint(t *testing.T) {
	pairs := [][2]string{
		{CellRetrieveFull, CellRetrieveCompact},
		{CellDirectFull, CellDirectDeferred},
	}
	for _, p := range pairs {
		a, _ := CellByID(p[0])
		b, _ := CellByID(p[1])
		if a.Endpoint != b.Endpoint {
			t.Errorf("cells %q and %q differ only in serialization config, so they must share endpoint %q vs %q", p[0], p[1], a.Endpoint, b.Endpoint)
		}
		if a.DiscoverySerialization == b.DiscoverySerialization && a.DirectSerialization == b.DirectSerialization {
			t.Errorf("cells %q and %q share an endpoint AND a serialization: they are not distinct", p[0], p[1])
		}
	}
}

// TestCellProxyToolsComposeExistingCatalog: the matrix must not re-derive a
// serialization or a tool catalog. A cell's built-in proxy tools come from
// ProxyToolsForMode (proxytools.go), which itself derives from the production
// tool builders.
func TestCellProxyToolsComposeExistingCatalog(t *testing.T) {
	c, ok := CellByID(CellRetrieveFull)
	if !ok {
		t.Fatalf("CellByID(%q) not found", CellRetrieveFull)
	}
	got := c.ProxyTools()
	want := ProxyToolsForMode(ModeRetrieveTools)
	if len(got) == 0 {
		t.Fatalf("cell %q has no proxy tools; the catalog was not composed", c.ID)
	}
	if len(got) != len(want) {
		t.Fatalf("cell %q proxy tools = %d, want %d (ProxyToolsForMode)", c.ID, len(got), len(want))
	}
	for i := range got {
		if got[i].ToolID != want[i].ToolID {
			t.Errorf("proxy tool %d = %q, want %q", i, got[i].ToolID, want[i].ToolID)
		}
	}
}

// --- T013: FR-016 capability conditions ------------------------------------

// TestCapabilityConditionsAreBinaryNotAnAxis pins contract rule 5: batching,
// stored scripts and validate-before-dispatch are binary conditions applied to
// the cells where each is available, and the report enumerates the applicable
// rows. They multiply nothing: the matrix stays 5 cells with or without them.
func TestCapabilityConditionsAreBinaryNotAnAxis(t *testing.T) {
	conds := CapabilityConditions()
	if len(conds) != 3 {
		t.Fatalf("CapabilityConditions() returned %d conditions, want 3 (batching, stored scripts, validate-before-dispatch)", len(conds))
	}

	wantApplies := map[string][]string{
		// describe_tool (batch <=5 ids) is registered on the retrieve_tools
		// surface (Spec 085) and the direct surface (Spec 102), and is
		// deliberately absent from the code-execution surface.
		CapabilityBatching: {CellRetrieveFull, CellRetrieveCompact, CellDirectFull, CellDirectDeferred},
		// Stored scripts are a code_execution feature and exist nowhere else.
		CapabilityStoredScripts: {CellCodeExec},
		// Pre-dispatch argument validation runs on the call_tool_* variants
		// (Spec 085 FR-013) and on direct dispatch (Spec 102 US3); the
		// code-execution surface dispatches from the sandbox and does not.
		CapabilityValidateBeforeDispatch: {CellRetrieveFull, CellRetrieveCompact, CellDirectFull, CellDirectDeferred},
	}

	seen := map[string]bool{}
	for _, c := range conds {
		want, ok := wantApplies[c.ID]
		if !ok {
			t.Errorf("unexpected capability %q", c.ID)
			continue
		}
		if seen[c.ID] {
			t.Errorf("capability %q enumerated twice", c.ID)
		}
		seen[c.ID] = true
		if len(c.AppliesTo) == 0 {
			t.Errorf("capability %q applies to no cell; a condition with no rows is not measurable", c.ID)
		}
		if strings.Join(c.AppliesTo, ",") != strings.Join(want, ",") {
			t.Errorf("capability %q applies to %v, want %v", c.ID, c.AppliesTo, want)
		}
		for _, id := range c.AppliesTo {
			if _, ok := CellByID(id); !ok {
				t.Errorf("capability %q names unknown cell %q", c.ID, id)
			}
			if id == CellBaseline {
				t.Errorf("capability %q applies to the baseline; the baseline is not an mcpproxy surface", c.ID)
			}
		}
	}

	if len(ModeCells()) != 5 {
		t.Errorf("capability conditions changed the cell count; they are conditions, not a fourth axis")
	}
}

// TestCellCapabilitiesAgreeWithConditions: the per-cell view and the
// per-capability enumeration are two renderings of one fact and must not drift.
func TestCellCapabilitiesAgreeWithConditions(t *testing.T) {
	fromConditions := map[string]map[string]bool{}
	for _, cond := range CapabilityConditions() {
		for _, cellID := range cond.AppliesTo {
			if fromConditions[cellID] == nil {
				fromConditions[cellID] = map[string]bool{}
			}
			fromConditions[cellID][cond.ID] = true
		}
	}

	for _, c := range ModeCells() {
		fromCell := map[string]bool{}
		for _, capID := range c.Capabilities {
			if fromCell[capID] {
				t.Errorf("cell %q lists capability %q twice", c.ID, capID)
			}
			fromCell[capID] = true
		}
		want := fromConditions[c.ID]
		if len(fromCell) != len(want) {
			t.Errorf("cell %q capabilities %v disagree with the enumeration %v", c.ID, sortedKeys(fromCell), sortedKeys(want))
			continue
		}
		for capID := range want {
			if !fromCell[capID] {
				t.Errorf("cell %q is missing capability %q that the enumeration claims", c.ID, capID)
			}
		}
	}

	if caps := BaselineCell().Capabilities; len(caps) != 0 {
		t.Errorf("baseline capabilities = %v, want none", caps)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestBenchModeConstantsMatchProductionRoutingModes guards the string identity
// the cell table leans on: bench's own mode names (tokens.go) and the
// production routing modes (internal/config) are the same strings, which is
// why a cell's RoutingMode can be handed straight to ProxyToolsForMode. If
// either side is ever renamed, this fails here rather than silently resolving
// every cell to an empty proxy catalog.
func TestBenchModeConstantsMatchProductionRoutingModes(t *testing.T) {
	if ModeRetrieveTools != config.RoutingModeRetrieveTools {
		t.Errorf("ModeRetrieveTools = %q, config.RoutingModeRetrieveTools = %q", ModeRetrieveTools, config.RoutingModeRetrieveTools)
	}
	if ModeCodeExecution != config.RoutingModeCodeExecution {
		t.Errorf("ModeCodeExecution = %q, config.RoutingModeCodeExecution = %q", ModeCodeExecution, config.RoutingModeCodeExecution)
	}
}

// TestCellArmNamesNameExistingArms pins the composition seam: a cell says WHICH
// existing encoding arm renders its menu and the caller resolves that name
// through the bench/arms registry, so no serialization is re-derived here.
// (The names are string literals because package bench cannot import
// bench/arms — arms imports bench.)
func TestCellArmNamesNameExistingArms(t *testing.T) {
	want := map[string]string{
		CellBaseline:        "baseline_json",
		CellRetrieveFull:    "baseline_json",
		CellRetrieveCompact: "compact_sig",
		CellDirectFull:      "baseline_json",
		CellDirectDeferred:  "direct_deferred",
		// The code-execution surface forces full serialization, so it renders
		// with the full arm — an override, not a default.
		CellCodeExec: "baseline_json",
	}
	for _, c := range AllCells() {
		if got := c.ArmName(); got != want[c.ID] {
			t.Errorf("cell %q: ArmName = %q, want %q", c.ID, got, want[c.ID])
		}
	}
}
