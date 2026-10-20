package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"pgregory.net/rapid"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Quarantine/approval testing pilot — runtime conformance.
//
// qapilotHarness is the ADAPTER: it maps the model's actions onto real
// Runtime entry points and projects the implementation's persisted state
// into a qapilot.Snapshot. The expected snapshot comes from
// internal/qapilot, which never sees this package. After every action the
// two snapshots must agree.

const qapilotServer = "pilot"

type qapilotHarness struct {
	t       *testing.T
	dir     string
	cfgPath string
	rt      *Runtime
	model   *qapilot.Server
	step    int
}

func newQAPilotHarness(t *testing.T, setup qapilot.Setup) *qapilotHarness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dir
	global := setup.GlobalQuarantine
	cfg.QuarantineEnabled = &global
	sc := &config.ServerConfig{
		Name: qapilotServer, Enabled: setup.Enabled, Quarantined: setup.Quarantined,
		TrustMode: string(setup.Trust), Command: "true", Protocol: "stdio",
	}
	// The operator stated the quarantine value (the #937 admission gate obeys
	// an explicit decision); the JSON round-trip below keeps the key present.
	sc.MarkQuarantineExplicitlySet(true)
	cfg.Servers = []*config.ServerConfig{sc}
	require.NoError(t, config.SaveConfig(cfg, cfgPath))

	h := &qapilotHarness{t: t, dir: dir, cfgPath: cfgPath}
	h.boot()
	h.model = qapilot.NewServer(qapilotServer, setup.Quarantined, setup.Enabled, qapilot.EffectiveTrust(setup.Trust), setup.GlobalQuarantine)
	t.Cleanup(func() {
		if h.rt != nil {
			_ = h.rt.Close()
		}
	})
	return h
}

// boot opens a runtime over the harness's data dir and config file, the way
// the daemon does at start, and reconciles storage with the file.
func (h *qapilotHarness) boot() {
	cfg, err := config.LoadFromFile(h.cfgPath)
	require.NoError(h.t, err)
	rt, err := New(cfg, h.cfgPath, zap.NewNop())
	require.NoError(h.t, err)
	require.NoError(h.t, rt.LoadConfiguredServers(nil))
	h.rt = rt
}

func qapilotTools(inventory map[string]qapilot.Contract) []*config.ToolMetadata {
	names := make([]string, 0, len(inventory))
	for n := range inventory {
		names = append(names, n)
	}
	sort.Strings(names)
	tools := make([]*config.ToolMetadata, 0, len(names))
	for _, n := range names {
		c := inventory[n]
		text, ok := qapilot.ContractText[c.Key]
		if !ok {
			panic("qapilot: contract without text: " + c.Key)
		}
		tools = append(tools, &config.ToolMetadata{
			ServerName: qapilotServer, Name: n, Description: text, ParamsJSON: `{"type":"object"}`,
		})
	}
	return tools
}

// apply drives one action through the implementation, then the model, and
// returns the implementation error (the model never errors).
func (h *qapilotHarness) apply(a qapilot.Action) error {
	h.step++
	rt := h.rt
	var err error
	switch a.Kind {
	case qapilot.ActDiscover:
		_, err = rt.checkToolApprovals(qapilotServer, qapilotTools(a.Inventory))
	case qapilot.ActApproveTools:
		err = rt.ApproveTools(qapilotServer, a.Names, "admin")
	case qapilot.ActApproveAll:
		_, err = rt.ApproveAllTools(qapilotServer, "admin")
	case qapilot.ActQuarantine:
		err = rt.QuarantineServer(qapilotServer, true)
	case qapilot.ActUnquarantine:
		err = rt.QuarantineServer(qapilotServer, false)
	case qapilot.ActEnable:
		err = rt.EnableServer(qapilotServer, true)
	case qapilot.ActDisable:
		err = rt.EnableServer(qapilotServer, false)
	case qapilot.ActBlockTool:
		_, err = rt.BlockTools(qapilotServer, a.Names, "admin")
	case qapilot.ActUnblockTool:
		err = rt.SetToolEnabled(qapilotServer, a.Names[0], true, "admin")
	case qapilot.ActRestart:
		// Persist the live config the way the daemon does, close, reopen.
		require.NoError(h.t, config.SaveConfig(rt.Config(), h.cfgPath))
		require.NoError(h.t, rt.Close())
		h.rt = nil
		h.boot()
	case qapilot.ActRestartUnstatedFile:
		// Persist, then strip the server's `quarantined` key from the file so
		// the reboot must rely on what config.db recorded.
		require.NoError(h.t, config.SaveConfig(rt.Config(), h.cfgPath))
		require.NoError(h.t, rt.Close())
		h.rt = nil
		raw, err := os.ReadFile(h.cfgPath)
		require.NoError(h.t, err)
		var doc map[string]any
		require.NoError(h.t, json.Unmarshal(raw, &doc))
		servers, _ := doc["mcpServers"].([]any)
		require.NotEmpty(h.t, servers, "config file must list the server")
		for _, s := range servers {
			if m, ok := s.(map[string]any); ok && m["name"] == qapilotServer {
				delete(m, "quarantined")
			}
		}
		raw, err = json.MarshalIndent(doc, "", "  ")
		require.NoError(h.t, err)
		require.NoError(h.t, os.WriteFile(h.cfgPath, raw, 0o600))
		h.boot()
	default:
		h.t.Fatalf("qapilot: unknown action %q", a.Kind)
	}
	h.model.Apply(a)
	return err
}

// actual projects the implementation's persisted state. Two sources are read
// for the server flags — the stored row (what a restart reloads) and the
// published config (what the dispatch path reads) — and a disagreement is a
// finding in its own right.
func (h *qapilotHarness) actual() (qapilot.Snapshot, []string) {
	t := h.t
	rt := h.rt
	var problems []string
	stored, err := rt.StorageManager().GetStoredServer(qapilotServer)
	require.NoError(t, err)
	row := stored.Config
	var live *config.ServerConfig
	for _, sc := range rt.Config().Servers {
		if sc.Name == qapilotServer {
			live = sc
		}
	}
	require.NotNil(t, live, "server must be in the published config")
	if row.Quarantined != live.Quarantined {
		problems = append(problems, fmt.Sprintf("stored quarantined=%v but published config quarantined=%v", row.Quarantined, live.Quarantined))
	}
	if row.Enabled != live.Enabled {
		problems = append(problems, fmt.Sprintf("stored enabled=%v but published config enabled=%v", row.Enabled, live.Enabled))
	}
	marker, err := rt.StorageManager().ToolBaselineDecided(qapilotServer)
	require.NoError(t, err)
	records, err := rt.StorageManager().ListToolApprovals(qapilotServer)
	require.NoError(t, err)
	snap := qapilot.Snapshot{
		Quarantined:     row.Quarantined,
		Enabled:         row.Enabled,
		HasBaseline:     marker,
		BaselineDecided: marker,
		Tools:           map[string]qapilot.Status{},
		ToolsDisabled:   map[string]bool{},
		Dispatch:        map[string]qapilot.DispatchClass{},
		Current:         map[string]string{},
		Approved:        map[string]string{},
	}
	quarantineEnabled := rt.Config().IsQuarantineEnabled()
	for _, rec := range records {
		snap.Tools[rec.ToolName] = qapilot.Status(rec.Status)
		// Stored contract state: map each stored hash back to the fixture
		// contract that produces it for this tool, so a regression that
		// fails to update (or wrongly rewrites) a contract is a disagreement.
		snap.Current[rec.ToolName] = qapilotContractKey(rec.ToolName, rec.CurrentHash)
		if rec.Status == storage.ToolApprovalStatusApproved || rec.Status == storage.ToolApprovalStatusChanged {
			snap.Approved[rec.ToolName] = qapilotContractKey(rec.ToolName, rec.ApprovedHash)
		}
		if rec.Disabled {
			snap.ToolsDisabled[rec.ToolName] = true
		}
		if rec.Status == storage.ToolApprovalStatusApproved || rec.Status == storage.ToolApprovalStatusChanged {
			snap.HasBaseline = true
		}
		// The dispatch verdict the implementation would give for this tool,
		// computed by the production classifier from the persisted facts
		// (internal/server/qapilot_dispatch_test.go proves, with a counting
		// upstream, that this classifier is what gates real dispatch).
		class := preflight.ClassifyTool(preflight.ClassifyInputs{
			Server: preflight.ServerPolicy{
				Found: true, Enabled: row.Enabled, Quarantined: row.Quarantined,
				AutoApproveToolChanges: row.IsQuarantineSkipped(),
			},
			QuarantineEnabled: quarantineEnabled,
			Approval: &preflight.ApprovalState{
				Status: rec.Status, Disabled: rec.Disabled,
				CurrentHash: rec.CurrentHash, HashSchemaVersion: rec.HashSchemaVersion,
			},
			Discovered: true,
		})
		snap.Dispatch[rec.ToolName] = qapilot.DispatchClass(class)
	}
	if len(snap.ToolsDisabled) == 0 {
		snap.ToolsDisabled = nil
	}
	return snap, problems
}

// qapilotContractKey names the fixture contract whose approval hash for
// toolName equals hash, or "?<hash>" when none matches (a contract the model
// never presented).
func qapilotContractKey(toolName, hash string) string {
	for _, c := range qapilot.FixtureContracts() {
		h := calculateToolApprovalHashWithOutputSchema(toolName, qapilot.ContractText[c.Key], normalizeJSON(`{"type":"object"}`), normalizeJSON(""), nil)
		if h == hash {
			return c.Key
		}
	}
	return "?" + hash
}

// persistedState is the full persisted approval state of the pilot server:
// every tool record as stored (all fields), the stored server row, and the
// baseline marker. Denial checks compare it before and after a refused write.
func (h *qapilotHarness) persistedState() string {
	t := h.t
	records, err := h.rt.StorageManager().ListToolApprovals(qapilotServer)
	require.NoError(t, err)
	sort.Slice(records, func(i, j int) bool { return records[i].ToolName < records[j].ToolName })
	stored, err := h.rt.StorageManager().GetStoredServer(qapilotServer)
	require.NoError(t, err)
	decision, hasMarker, err := h.rt.StorageManager().GetToolBaselineDecision(qapilotServer)
	require.NoError(t, err)
	raw, err := json.Marshal(struct {
		Records   []*storage.ToolApprovalRecord
		Row       *config.ServerConfig
		Stamp     any
		HasMarker bool
		Marker    any
	}{records, stored.Config, stored.Stamp, hasMarker, decision})
	require.NoError(t, err)
	return string(raw)
}

// compare fails the test on the first disagreement, naming the step.
func (h *qapilotHarness) compare(tb interface {
	Helper()
	Fatalf(string, ...any)
}, label string) {
	tb.Helper()
	actual, problems := h.actual()
	expected := h.model.Snapshot()
	diff := append(problems, qapilot.Diff(expected, actual)...)
	if len(diff) > 0 {
		tb.Fatalf("qapilot conformance: implementation disagrees with the model after %s (step %d):\n  %s\nexpected: %+v\nactual:   %+v",
			label, h.step, strings.Join(diff, "\n  "), expected, actual)
	}
}

// TestQAPilot_Scenarios runs the mandatory deterministic inventory. Every
// scenario is its own subtest so a failure names the scenario, and every
// step is compared, so a transient wrong state between two "correct" states
// is caught too.
func TestQAPilot_Scenarios(t *testing.T) {
	for _, sc := range qapilot.Scenarios {
		t.Run(sc.ID, func(t *testing.T) {
			qapilot.Track(t, sc.ID, sc.Requirements...)
			h := newQAPilotHarness(t, sc.Setup)
			h.compare(t, "setup")
			for i, a := range sc.Steps {
				err := h.apply(a)
				require.NoError(t, err, "%s step %d (%s)", sc.ID, i+1, a.Kind)
				h.compare(t, fmt.Sprintf("%s step %d (%s)", sc.ID, i+1, a.Kind))
			}
		})
	}
}

// TestQAPilot_FixtureVerdicts keeps the model's scan labels honest: each
// fixture contract is scanned by the real offline scanner exactly as the
// trust_mode:scan gate scans a new tool, and the verdict must be the label
// the model assumes.
func TestQAPilot_FixtureVerdicts(t *testing.T) {
	qapilot.Track(t, "fixture/verdicts", "QP-19")
	for _, c := range qapilot.FixtureContracts() {
		tool := &config.ToolMetadata{ServerName: qapilotServer, Name: "probe", Description: qapilot.ContractText[c.Key], ParamsJSON: `{"type":"object"}`}
		verdict, _, coverageOK := scanner.ScanToolMetadataVerdict(qapilotServer, []*config.ToolMetadata{tool}, nil)
		require.True(t, coverageOK, "fixture %s: scanner coverage must be complete", c.Key)
		switch c.Verdict {
		case qapilot.VerdictClean:
			require.Equal(t, "clean", verdict, "fixture %s must scan clean", c.Key)
		case qapilot.VerdictDangerous:
			require.NotEqual(t, "clean", verdict, "fixture %s must not scan clean", c.Key)
		}
	}
}

// TestQAPilot_InvariantTable checks the implementation's transition guard
// against the policy's permitted-reason lists for every (old, new, reason)
// combination (QP-13).
func TestQAPilot_InvariantTable(t *testing.T) {
	qapilot.Track(t, "invariant/table", "QP-13")
	statuses := []qapilot.Status{qapilot.StatusPending, qapilot.StatusChanged, qapilot.StatusApproved}
	for _, from := range statuses {
		for _, to := range statuses {
			for _, reason := range qapilot.AllReasons {
				want := qapilot.TransitionAllowed(from, to, reason)
				err := assertToolApprovalInvariant(string(from), string(to), TransitionReason(reason))
				if got := err == nil; got != want {
					t.Errorf("%s→%s with %q: policy allows=%v, implementation allows=%v (%v)", from, to, reason, want, got, err)
				}
			}
		}
	}
}

// TestQAPilot_StaleReviewWritesNothing: a review-bound approval whose
// expected hash is out of date is refused and leaves the record untouched
// (QP-17).
func TestQAPilot_StaleReviewWritesNothing(t *testing.T) {
	qapilot.Track(t, "review-binding/stale-hash", "QP-17")
	h := newQAPilotHarness(t, qapilot.Setup{Quarantined: true, Enabled: true, Trust: qapilot.TrustManual, GlobalQuarantine: true})
	require.NoError(t, h.apply(qapilot.Action{Kind: qapilot.ActDiscover, Inventory: map[string]qapilot.Contract{"t1": qapilot.Benign}}))
	h.compare(t, "discover")
	before := h.persistedState()
	n, err := h.rt.ApproveToolsReviewed(qapilotServer, []string{"t1"}, map[string]string{"t1": "rv1-0000000000000000000000000000000000000000000000000000000000000000"}, "admin")
	require.Error(t, err, "a stale review must be refused")
	require.Equal(t, 0, n)
	require.Contains(t, strings.ToLower(err.Error()), "out of date")
	// Every persisted byte of the server's approval state is unchanged, not
	// just the status: record fields, evidence, the stored row, the marker.
	require.Equal(t, before, h.persistedState(), "nothing may be written on a stale review")
	h.compare(t, "stale review refused")
}

// TestQAPilot_ServerReplacedWritesNothing: an approval or release bound to an
// incarnation the server no longer has is refused and changes nothing
// (QP-18).
func TestQAPilot_ServerReplacedWritesNothing(t *testing.T) {
	qapilot.Track(t, "incarnation/server-replaced", "QP-18")
	h := newQAPilotHarness(t, qapilot.Setup{Quarantined: true, Enabled: true, Trust: qapilot.TrustManual, GlobalQuarantine: true})
	require.NoError(t, h.apply(qapilot.Action{Kind: qapilot.ActDiscover, Inventory: map[string]qapilot.Contract{"t1": qapilot.Benign}}))
	current, ok := h.rt.ServerIncarnation(qapilotServer)
	require.True(t, ok)
	wrong := current + 1000
	before := h.persistedState()
	err := h.rt.ApproveToolsAt(qapilotServer, []string{"t1"}, "admin", wrong)
	require.ErrorIs(t, err, ErrServerReplaced)
	require.Equal(t, before, h.persistedState(), "ApproveToolsAt with a replaced incarnation must write nothing")
	_, err = h.rt.ApproveAllToolsAt(qapilotServer, "admin", wrong)
	require.ErrorIs(t, err, ErrServerReplaced)
	require.Equal(t, before, h.persistedState(), "ApproveAllToolsAt with a replaced incarnation must write nothing")
	err = h.rt.QuarantineServerAt(qapilotServer, false, wrong)
	require.ErrorIs(t, err, ErrServerReplaced)
	require.Equal(t, before, h.persistedState(), "QuarantineServerAt with a replaced incarnation must write nothing")
	// The model saw none of these: the server must still be quarantined with
	// t1 pending.
	h.compare(t, "replaced-incarnation refusals")
}

// TestQAPilot_Explore is the randomized exploration lane: rapid draws action
// sequences from the same vocabulary the scenarios use and compares after
// every action. Failures are shrunk by rapid to a minimal sequence and the
// reproduction is written to testdata/rapid/. It runs only when
// QAPILOT_EXPLORE=1 (the scheduled lane) because every iteration boots a
// runtime; the mandatory inventory above runs on every PR.
func TestQAPilot_Explore(t *testing.T) {
	if os.Getenv("QAPILOT_EXPLORE") == "" {
		t.Skip("set QAPILOT_EXPLORE=1 to run the randomized exploration (scheduled lane)")
	}
	skipQuarantinePropertyTestOnWindows(t)
	setups := []qapilot.Setup{
		{Quarantined: true, Enabled: true, Trust: qapilot.TrustManual, GlobalQuarantine: true},
		{Quarantined: false, Enabled: true, Trust: qapilot.TrustManual, GlobalQuarantine: true},
		{Quarantined: true, Enabled: true, Trust: qapilot.TrustScan, GlobalQuarantine: true},
		{Quarantined: false, Enabled: true, Trust: qapilot.TrustScan, GlobalQuarantine: true},
		{Quarantined: false, Enabled: true, Trust: qapilot.TrustAuto, GlobalQuarantine: true},
		// quarantine_enabled:false is deliberately absent: a changed definition
		// under it reproduces the documented deviation D01 (record `changed`,
		// dispatch ready), which TestQAPilot_KnownDeviations pins. S17 covers
		// its additions deterministically.
	}
	contracts := qapilot.FixtureContracts()
	toolNames := []string{"t1", "t2", "t3"}
	rapid.Check(t, func(rt *rapid.T) {
		setup := setups[rapid.IntRange(0, len(setups)-1).Draw(rt, "setup")]
		h := newQAPilotHarness(t, setup)
		defer func() { _ = h.rt.Close(); h.rt = nil }()
		inventory := map[string]qapilot.Contract{}
		n := rapid.IntRange(3, 14).Draw(rt, "steps")
		for i := 0; i < n; i++ {
			var a qapilot.Action
			switch rapid.IntRange(0, 9).Draw(rt, fmt.Sprintf("kind_%d", i)) {
			case 0, 1, 2: // discover: grow or redefine the monotone inventory
				count := rapid.IntRange(1, len(toolNames)).Draw(rt, fmt.Sprintf("count_%d", i))
				if count < len(inventory) {
					count = len(inventory)
				}
				next := map[string]qapilot.Contract{}
				for _, name := range toolNames[:count] {
					next[name] = contracts[rapid.IntRange(0, len(contracts)-1).Draw(rt, fmt.Sprintf("contract_%d_%s", i, name))]
				}
				inventory = next
				a = qapilot.Action{Kind: qapilot.ActDiscover, Inventory: next}
			case 3:
				if len(inventory) == 0 {
					continue
				}
				a = qapilot.Action{Kind: qapilot.ActApproveTools, Names: []string{toolNames[rapid.IntRange(0, len(inventory)-1).Draw(rt, fmt.Sprintf("approve_%d", i))]}}
			case 4:
				a = qapilot.Action{Kind: qapilot.ActApproveAll}
			case 5:
				a = qapilot.Action{Kind: qapilot.ActUnquarantine}
			case 6:
				a = qapilot.Action{Kind: qapilot.ActQuarantine}
			case 7:
				if len(inventory) == 0 {
					continue
				}
				name := toolNames[rapid.IntRange(0, len(inventory)-1).Draw(rt, fmt.Sprintf("block_%d", i))]
				if rapid.Bool().Draw(rt, fmt.Sprintf("blockdir_%d", i)) {
					a = qapilot.Action{Kind: qapilot.ActBlockTool, Names: []string{name}}
				} else {
					a = qapilot.Action{Kind: qapilot.ActUnblockTool, Names: []string{name}}
				}
			case 8:
				if rapid.Bool().Draw(rt, fmt.Sprintf("enable_%d", i)) {
					a = qapilot.Action{Kind: qapilot.ActEnable}
				} else {
					a = qapilot.Action{Kind: qapilot.ActDisable}
				}
			case 9:
				a = qapilot.Action{Kind: qapilot.ActRestart}
			}
			if err := h.apply(a); err != nil {
				rt.Fatalf("step %d %s: %v", i+1, a.Kind, err)
			}
			h.compare(rt, fmt.Sprintf("explore step %d (%s)", i+1, a.Kind))
		}
	})
}

// TestQAPilot_KnownDeviations runs the recorded policy/implementation
// deviations and asserts each is still exactly the recorded one: the model
// (documented policy) MUST disagree with the implementation in the recorded
// way. When a deviation is fixed this test fails, which is the signal to
// promote the sequence into the mandatory inventory. It never gates a PR on
// the deviation itself.
func TestQAPilot_KnownDeviations(t *testing.T) {
	for _, sc := range qapilot.KnownDeviations {
		t.Run(sc.ID, func(t *testing.T) {
			qapilot.Track(t, "deviation/"+sc.ID, sc.Requirements...)
			h := newQAPilotHarness(t, sc.Setup)
			for _, a := range sc.Steps {
				require.NoError(t, h.apply(a))
			}
			actual, problems := h.actual()
			require.Empty(t, problems)
			diff := qapilot.Diff(h.model.Snapshot(), actual)
			require.NotEmpty(t, diff, "%s: the recorded deviation no longer reproduces — the implementation now matches the policy; move this sequence into qapilot.Scenarios", sc.ID)
			switch sc.ID {
			case "D01":
				// Record status differs (changed vs approved), and with it the
				// approved contract (the implementation keeps the original
				// approval; the policy would have re-approved the new one);
				// callability does not differ (both ready). Anything else is a
				// NEW deviation.
				require.Equal(t, []string{
					`tool "t1": expected status "approved", actual "changed"`,
					`tool "t1": expected approved contract "malicious-v1", actual "benign-v1"`,
				}, diff)
				require.Equal(t, qapilot.DispatchReady, actual.Dispatch["t1"], "the gate is off, so the tool must still be callable")
			default:
				t.Fatalf("deviation %s has no recorded shape", sc.ID)
			}
		})
	}
}
