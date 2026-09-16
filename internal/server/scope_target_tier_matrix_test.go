package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 acceptance tables (tasks T009 / T010, gaps FR009-G6 and
// FR009-G7). The tables are GENERATED at the spec's shape (spec.md FR-009
// "Acceptance tables") and every cell carries the SC-002 counting oracle:
//
//	(a) retrieve dispatch — 54 cells = 3 permission sets × 3 target tiers ×
//	    3 call_tool_* variants × strict validation on/off, each classified
//	    BEFORE the run as permission-disallowed → intent-mismatch → allowed;
//	(b) direct-name dispatch (through the handler mcp-go actually registered,
//	    research D15) and nested script dispatch (through the real sandbox,
//	    envelope asserted) — permission sets × target tiers, no variant;
//	(c) the same-tier paired names: "erase" approved and dispatched, its
//	    namespaced sibling "ns:erase" config-denied via disabled_tools or
//	    left without its own approval record — refused with zero upstream
//	    calls for a server-restricted full-tier token, an unrestricted
//	    full-tier token and an administrator (Edge Cases (2), SC-005);
//	(d) the {read,destructive} regression rows (FR009-G7): HasPermission is
//	    exact-match, so a write target is refused on all three paths;
//	(e) auth.AdminContext() control rows on every table;
//	(f) unresolved identity on a KNOWN server (research D4): a name the
//	    discovery snapshot does not contain is refused with the
//	    insufficient-permission body for every caller on retrieve and nested
//	    dispatch (direct is exempt — its handler IS the registration identity).
//
// Cells that already pass on the merge base are regression pins. The cells
// expected to fail on HEAD are the unapproved "ns:erase" rows (the reader
// hands "ns:erase" the collapsed "erase" record / no record → ready) and the
// unresolved-identity rows (an undiscovered name is granted the destructive
// tier and reaches the upstream).

// tierTarget is one target-tier fixture tool: its raw name and the tier its
// annotations derive to (the oracle for the pre-classification).
type tierTarget struct {
	spec toolSpec
	tier string // contracts.OperationType*
}

// tierTargets are the three target tiers every table spans.
func tierTargets() []tierTarget {
	targets := []tierTarget{
		{spec: readSpec("read_thing"), tier: contracts.OperationTypeRead},
		{spec: writeSpec("write_thing"), tier: contracts.OperationTypeWrite},
		{spec: destructiveSpec("destroy_thing"), tier: contracts.OperationTypeDestructive},
	}
	for _, target := range targets {
		derived := contracts.ToolVariantToOperationType[contracts.DeriveCallWith(target.spec.Annotations)]
		if derived != target.tier {
			panic(fmt.Sprintf("fixture %q derives to %q, not %q — the table would classify the wrong tier", target.spec.Name, derived, target.tier))
		}
	}
	return targets
}

// permissionSet is one caller of the tables. admin=true is the
// auth.AdminContext() control row (no permission list applies).
type permissionSet struct {
	name  string
	perms []string
	admin bool
}

func (ps permissionSet) ctx(allowed []string) context.Context {
	if ps.admin {
		return adminCtx()
	}
	return agentCtx(allowed, ps.perms, "")
}

func (ps permissionSet) has(tier string) bool {
	if ps.admin {
		return true
	}
	for _, p := range ps.perms {
		if p == tier {
			return true
		}
	}
	return false
}

// specPermissionSets are the three sets the spec's 54-cell table names.
func specPermissionSets() []permissionSet {
	return []permissionSet{
		{name: "read", perms: []string{auth.PermRead}},
		{name: "read+write", perms: []string{auth.PermRead, auth.PermWrite}},
		{name: "read+write+destructive", perms: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}},
	}
}

// readDestructiveSet is the FR009-G7 row: destructive without write.
func readDestructiveSet() permissionSet {
	return permissionSet{name: "read+destructive", perms: []string{auth.PermRead, auth.PermDestructive}}
}

func adminSet() permissionSet { return permissionSet{name: "admin", admin: true} }

// cellVerdict is the pre-classified outcome of one table cell.
type cellVerdict string

const (
	verdictAllowed                cellVerdict = "allowed"
	verdictInsufficientPermission cellVerdict = "insufficient-permission"
	verdictIntentMismatch         cellVerdict = "intent-mismatch"
)

// retrieveCell is one generated cell of table (a).
type retrieveCell struct {
	strict  bool
	caller  permissionSet
	target  tierTarget
	variant string
	verdict cellVerdict
	// want is the substring the refusal body must carry; empty for allowed.
	want string
}

func (c retrieveCell) name() string {
	return fmt.Sprintf("strict=%s/perms=%s/target=%s/%s", onOff(c.strict), c.caller.name, c.target.tier, c.variant)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// classifyRetrieveCell applies FR-009's classification order: the caller's set
// must contain the selected variant's tier AND the target's tier (either miss
// → insufficient permission; the variant gate fires first in the handler, so
// its wording is the one expected); then the existing strict-validation
// predicate (a destructive target through a non-destructive variant, strict
// on) → intent mismatch; the remainder is dispatched.
func classifyRetrieveCell(strict bool, caller permissionSet, target tierTarget, variant string) retrieveCell {
	cell := retrieveCell{strict: strict, caller: caller, target: target, variant: variant}
	variantTier := contracts.ToolVariantToOperationType[variant]
	switch {
	case !caller.has(variantTier):
		cell.verdict = verdictInsufficientPermission
		cell.want = fmt.Sprintf("Insufficient permissions: '%s' requires '%s' permission", variant, variantTier)
	case !caller.has(target.tier):
		cell.verdict = verdictInsufficientPermission
		cell.want = fmt.Sprintf("Permission denied: token does not have '%s' permission required for tool 'a:%s'", target.tier, target.spec.Name)
	case strict && target.tier == contracts.OperationTypeDestructive && variant != contracts.ToolVariantDestructive:
		cell.verdict = verdictIntentMismatch
		cell.want = fmt.Sprintf("Tool 'a:%s' is marked destructive by server, use call_tool_destructive", target.spec.Name)
	default:
		cell.verdict = verdictAllowed
	}
	return cell
}

// generateRetrieveTable builds the cells for the given callers over both
// strict settings, every target tier and every variant.
func generateRetrieveTable(callers []permissionSet) []retrieveCell {
	var cells []retrieveCell
	for _, strict := range []bool{true, false} {
		for _, caller := range callers {
			for _, target := range tierTargets() {
				for _, variant := range []string{contracts.ToolVariantRead, contracts.ToolVariantWrite, contracts.ToolVariantDestructive} {
					cells = append(cells, classifyRetrieveCell(strict, caller, target, variant))
				}
			}
		}
	}
	return cells
}

// tierMatrixFixture is one proxy + runtime + counting upstream "a" exposing
// the three target-tier tools (plus any extra specs a table needs).
type tierMatrixFixture struct {
	proxy *MCPProxyServer
	rt    *runtime.Runtime
	up    *countingUpstream
}

func newTierMatrixFixture(t *testing.T, strict bool, serverCfg *config.ServerConfig, extra ...toolSpec) *tierMatrixFixture {
	t.Helper()
	if serverCfg == nil {
		serverCfg = &config.ServerConfig{Name: "a", Enabled: true}
	}
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{serverCfg})
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: strict}
	specs := make([]toolSpec, 0, 3+len(extra))
	for _, target := range tierTargets() {
		specs = append(specs, target.spec)
	}
	specs = append(specs, extra...)
	up := startCountingUpstream(t, proxy, rt, "a", specs...)
	return &tierMatrixFixture{proxy: proxy, rt: rt, up: up}
}

// oracle snapshots the upstream witness before a cell runs.
type upstreamOracle struct {
	up     *countingUpstream
	before int64
	names  int
}

func (f *tierMatrixFixture) oracle() upstreamOracle {
	return upstreamOracle{up: f.up, before: f.up.count.Load(), names: len(f.up.dispatched())}
}

// refused asserts the cell made ZERO upstream calls.
func (o upstreamOracle) refused(t *testing.T) {
	t.Helper()
	assert.Equal(t, o.before, o.up.count.Load(), "a refused cell must never reach the upstream (dispatched: %v)", o.up.dispatched()[o.names:])
}

// admitted asserts exactly ONE upstream call arrived, under the exact raw name.
func (o upstreamOracle) admitted(t *testing.T, rawName string) {
	t.Helper()
	require.Equal(t, o.before+1, o.up.count.Load(), "an allowed cell must reach the upstream exactly once")
	names := o.up.dispatched()
	require.Len(t, names, o.names+1)
	assert.Equal(t, rawName, names[o.names], "the upstream must receive the exact raw tool name that was authorized")
}

// registeredVariantHandler returns the call_tool_* handler mcp-go dispatches
// to on the default (retrieve) server — not one the test built for itself.
func (f *tierMatrixFixture) registeredVariantHandler(t *testing.T, variant string) mcpserver.ToolHandlerFunc {
	t.Helper()
	st, ok := f.proxy.server.ListTools()[variant]
	require.Truef(t, ok, "%s must be registered on the retrieve server", variant)
	return st.Handler
}

// retrieve drives one canonical id through the registered variant handler.
func (f *tierMatrixFixture) retrieve(t *testing.T, ctx context.Context, variant, canonical string) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = variant
	req.Params.Arguments = map[string]interface{}{"name": canonical}
	result, err := f.registeredVariantHandler(t, variant)(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	return result
}

func matrixText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if text, ok := result.Content[0].(mcp.TextContent); ok {
		return text.Text
	}
	return fmt.Sprintf("%v", result.Content[0])
}

// runRetrieveCell executes one table-(a) cell against its fixture and asserts
// the verdict's own oracle plus the counting oracle.
func runRetrieveCell(t *testing.T, f *tierMatrixFixture, cell retrieveCell) {
	t.Helper()
	oracle := f.oracle()
	result := f.retrieve(t, cell.caller.ctx([]string{"a"}), cell.variant, "a:"+cell.target.spec.Name)
	text := matrixText(result)
	switch cell.verdict {
	case verdictAllowed:
		require.False(t, result.IsError, "allowed cell must be dispatched: %s", text)
		oracle.admitted(t, cell.target.spec.Name)
	case verdictInsufficientPermission:
		require.True(t, result.IsError, "permission-disallowed cell must be refused: %s", text)
		assert.Contains(t, text, cell.want)
		assert.NotContains(t, text, "marked destructive", "permission must be decided before intent")
		oracle.refused(t)
	case verdictIntentMismatch:
		require.True(t, result.IsError, "intent-mismatch cell must be refused: %s", text)
		assert.Contains(t, text, cell.want)
		assert.NotContains(t, text, "Permission denied", "an intent mismatch is not a permission refusal")
		assert.NotContains(t, text, "Insufficient permissions")
		oracle.refused(t)
	}
}

// TestScopeTargetTier_RetrieveTable is acceptance table (a): the 54 spec
// cells plus the admin control rows, on one fixture per strict setting.
func TestScopeTargetTier_RetrieveTable(t *testing.T) {
	specCells := generateRetrieveTable(specPermissionSets())
	require.Len(t, specCells, 54, "3 permission sets × 3 target tiers × 3 variants × strict on/off")
	counts := map[cellVerdict]int{}
	for _, cell := range specCells {
		counts[cell.verdict]++
	}
	// The classification itself is pinned: 3 permission sets over 3 variants
	// and 3 targets leave exactly 14 cells passing both permission checks per
	// strict setting ({read}: 1, {read,write}: 4, full: 9) and 13 refused for
	// permission; strict on turns the 2 destructive-target / non-destructive-
	// variant cells of the full set into intent mismatches.
	assert.Equal(t, map[cellVerdict]int{
		verdictInsufficientPermission: 26,
		verdictIntentMismatch:         2,
		verdictAllowed:                26,
	}, counts)

	adminCells := generateRetrieveTable([]permissionSet{adminSet()})
	require.Len(t, adminCells, 18)
	for _, cell := range adminCells {
		require.NotEqual(t, verdictInsufficientPermission, cell.verdict, "an administrator is never permission-disallowed")
	}

	for _, strict := range []bool{true, false} {
		t.Run("strict="+onOff(strict), func(t *testing.T) {
			f := newTierMatrixFixture(t, strict, nil)
			for _, cell := range append(specCells, adminCells...) {
				if cell.strict != strict {
					continue
				}
				t.Run(cell.name(), func(t *testing.T) { runRetrieveCell(t, f, cell) })
			}
		})
	}
}

// TestScopeTargetTier_RetrieveTable_ReadDestructive is FR009-G7 on the
// retrieve path: {read,destructive} × write target is refused through every
// variant under both strict settings (exact-match HasPermission), while the
// read and destructive targets stay reachable as controls.
func TestScopeTargetTier_RetrieveTable_ReadDestructive(t *testing.T) {
	cells := generateRetrieveTable([]permissionSet{readDestructiveSet()})
	require.Len(t, cells, 18)
	writeRows := 0
	for _, cell := range cells {
		if cell.target.tier == contracts.OperationTypeWrite {
			writeRows++
			require.Equal(t, verdictInsufficientPermission, cell.verdict, "%s: destructive must not imply write", cell.name())
		}
	}
	require.Equal(t, 6, writeRows)

	for _, strict := range []bool{true, false} {
		t.Run("strict="+onOff(strict), func(t *testing.T) {
			f := newTierMatrixFixture(t, strict, nil)
			for _, cell := range cells {
				if cell.strict != strict {
					continue
				}
				t.Run(cell.name(), func(t *testing.T) { runRetrieveCell(t, f, cell) })
			}
		})
	}
}

// --- table (b): direct-name dispatch through the registered handler ---------

// stageDirectCatalog renders and registers a direct catalog for the fixture's
// tools exactly as RefreshDirectModeTools does (render → SetTools → publish),
// with the annotations the StateView carries, so the handler mcp-go would
// dispatch to closes over the same registration identity. It delegates to
// stageDirectCatalogFor (mcp_routing_test.go), the multi-upstream stager the
// direct handler cells share.
func (f *tierMatrixFixture) stageDirectCatalog(t *testing.T) {
	t.Helper()
	stageDirectCatalogFor(t, f.proxy, f.up)
}

// direct drives one raw tool through the handler registered for its display
// name on the direct server.
func (f *tierMatrixFixture) direct(t *testing.T, ctx context.Context, rawName string) *mcp.CallToolResult {
	t.Helper()
	display := FormatDirectToolName(f.up.Server, rawName)
	st, ok := f.proxy.directServer.ListTools()[display]
	require.Truef(t, ok, "%q must be registered on the direct server", display)
	req := mcp.CallToolRequest{}
	req.Params.Name = display
	req.Params.Arguments = map[string]interface{}{}
	result, err := st.Handler(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	return result
}

type directCell struct {
	caller  permissionSet
	target  tierTarget
	verdict cellVerdict
	want    string
}

func (c directCell) name() string {
	return fmt.Sprintf("perms=%s/target=%s", c.caller.name, c.target.tier)
}

// classifyDirectCell: no variant exists on the direct surface, so the only
// permission check is the target tier; there is no intent validation.
func classifyDirectCell(caller permissionSet, target tierTarget) directCell {
	cell := directCell{caller: caller, target: target, verdict: verdictAllowed}
	if !caller.has(target.tier) {
		cell.verdict = verdictInsufficientPermission
		cell.want = fmt.Sprintf("Permission denied: token does not have '%s' permission required for tool 'a:%s'", target.tier, target.spec.Name)
	}
	return cell
}

func generateDirectTable(callers []permissionSet) []directCell {
	var cells []directCell
	for _, caller := range callers {
		for _, target := range tierTargets() {
			cells = append(cells, classifyDirectCell(caller, target))
		}
	}
	return cells
}

func runDirectCell(t *testing.T, f *tierMatrixFixture, cell directCell) {
	t.Helper()
	oracle := f.oracle()
	result := f.direct(t, cell.caller.ctx([]string{"a"}), cell.target.spec.Name)
	text := matrixText(result)
	switch cell.verdict {
	case verdictAllowed:
		require.False(t, result.IsError, "allowed cell must be dispatched: %s", text)
		oracle.admitted(t, cell.target.spec.Name)
	default:
		require.True(t, result.IsError, "permission-disallowed cell must be refused: %s", text)
		assert.Contains(t, text, cell.want)
		oracle.refused(t)
	}
}

// TestScopeTargetTier_DirectTable is acceptance table (b), direct half:
// permission sets × target tiers through the registered handler, with the
// {read,destructive} (FR009-G7) and administrator rows.
func TestScopeTargetTier_DirectTable(t *testing.T) {
	specCells := generateDirectTable(specPermissionSets())
	require.Len(t, specCells, 9, "3 permission sets × 3 target tiers")
	g7 := generateDirectTable([]permissionSet{readDestructiveSet()})
	for _, cell := range g7 {
		if cell.target.tier == contracts.OperationTypeWrite {
			require.Equal(t, verdictInsufficientPermission, cell.verdict, "destructive must not imply write on the direct surface")
		}
	}
	adminCells := generateDirectTable([]permissionSet{adminSet()})
	for _, cell := range adminCells {
		require.Equal(t, verdictAllowed, cell.verdict)
	}

	f := newTierMatrixFixture(t, true, nil)
	f.stageDirectCatalog(t)
	for _, cell := range append(append(specCells, g7...), adminCells...) {
		t.Run(cell.name(), func(t *testing.T) { runDirectCell(t, f, cell) })
	}
}

// --- table (b): nested script dispatch through the real sandbox -------------

// nestedEnvelope is what the script saw from call_tool(): the {ok, error}
// envelope jsruntime hands back, lifted out of the code_execution result.
type nestedEnvelope struct {
	OK      bool
	Code    string
	Message string
}

// nested runs `call_tool(server, tool, {})` through the registered
// code_execution handler (the real jsruntime sandbox with the proxy's
// upstreamToolCaller behind it) and returns the envelope the script observed.
func (f *tierMatrixFixture) nested(t *testing.T, ctx context.Context, rawName string) nestedEnvelope {
	t.Helper()
	st, ok := f.proxy.codeExecServer.ListTools()["code_execution"]
	require.True(t, ok, "code_execution must be registered on the code-execution server")

	code := fmt.Sprintf(`var r = call_tool(%q, %q, {});
({ ok: r.ok === true, code: r.error ? String(r.error.code) : "", message: r.error ? String(r.error.message) : "" })`, f.up.Server, rawName)
	req := mcp.CallToolRequest{}
	req.Params.Name = "code_execution"
	req.Params.Arguments = map[string]interface{}{
		"code":    code,
		"input":   map[string]interface{}{},
		"options": map[string]interface{}{"timeout_ms": 10000},
	}
	result, err := st.Handler(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "the code_execution wrapper itself must succeed: %s", matrixText(result))

	var outer struct {
		Ok    bool `json:"ok"`
		Value struct {
			OK      bool   `json:"ok"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"value"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(matrixText(result)), &outer), "code_execution result must be the jsruntime.Result JSON: %s", matrixText(result))
	require.Truef(t, outer.Ok, "the script must run to completion (error: %+v)", outer.Error)
	return nestedEnvelope{OK: outer.Value.OK, Code: outer.Value.Code, Message: outer.Value.Message}
}

type nestedCell struct {
	caller  permissionSet
	target  tierTarget
	verdict cellVerdict
}

func (c nestedCell) name() string {
	return fmt.Sprintf("perms=%s/target=%s", c.caller.name, c.target.tier)
}

func classifyNestedCell(caller permissionSet, target tierTarget) nestedCell {
	cell := nestedCell{caller: caller, target: target, verdict: verdictAllowed}
	if !caller.has(target.tier) {
		cell.verdict = verdictInsufficientPermission
	}
	return cell
}

func generateNestedTable(callers []permissionSet) []nestedCell {
	var cells []nestedCell
	for _, caller := range callers {
		for _, target := range tierTargets() {
			cells = append(cells, classifyNestedCell(caller, target))
		}
	}
	return cells
}

func runNestedCell(t *testing.T, f *tierMatrixFixture, cell nestedCell) {
	t.Helper()
	oracle := f.oracle()
	env := f.nested(t, cell.caller.ctx([]string{"a"}), cell.target.spec.Name)
	switch cell.verdict {
	case verdictAllowed:
		require.True(t, env.OK, "allowed cell must be dispatched: %s %s", env.Code, env.Message)
		oracle.admitted(t, cell.target.spec.Name)
	default:
		require.False(t, env.OK, "permission-disallowed cell must be refused")
		assert.Equal(t, "PERMISSION_DENIED", env.Code, "the sandbox must answer with the permission envelope: %s", env.Message)
		assert.Contains(t, env.Message, fmt.Sprintf("'%s' permission for tool 'a:%s'", cell.target.tier, cell.target.spec.Name))
		oracle.refused(t)
	}
}

// TestScopeTargetTier_NestedTable is acceptance table (b), nested half:
// permission sets × target tiers through the real sandbox, envelope asserted,
// with the {read,destructive} (FR009-G7) and administrator rows.
func TestScopeTargetTier_NestedTable(t *testing.T) {
	specCells := generateNestedTable(specPermissionSets())
	require.Len(t, specCells, 9, "3 permission sets × 3 target tiers")
	g7 := generateNestedTable([]permissionSet{readDestructiveSet()})
	for _, cell := range g7 {
		if cell.target.tier == contracts.OperationTypeWrite {
			require.Equal(t, verdictInsufficientPermission, cell.verdict, "destructive must not imply write in the sandbox")
		}
	}
	adminCells := generateNestedTable([]permissionSet{adminSet()})

	f := newTierMatrixFixture(t, true, nil)
	for _, cell := range append(append(specCells, g7...), adminCells...) {
		t.Run(cell.name(), func(t *testing.T) { runNestedCell(t, f, cell) })
	}
}

// --- paired names: erase approved, ns:erase config-denied / unapproved ------

// pairedCallers are the callers every paired-name row is driven for: the
// spec's server-restricted full-tier token, the unrestricted full-tier token
// of Edge Cases (2) and the administrator recorded by SC-005.
func pairedCallers() []struct {
	name string
	ctx  context.Context
} {
	full := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	return []struct {
		name string
		ctx  context.Context
	}{
		{name: "a-only full-tier token", ctx: agentCtx([]string{"a"}, full, "")},
		{name: "unrestricted full-tier token", ctx: agentCtx([]string{"*"}, full, "")},
		{name: "administrator", ctx: adminCtx()},
	}
}

// pairedFixture builds the same-tier pair on server "a": read-tier "erase"
// approved, read-tier "ns:erase" either denied by disabled_tools or left
// without its own approval record while "erase"'s approved record exists.
func pairedFixture(t *testing.T, configDenied bool) *tierMatrixFixture {
	t.Helper()
	serverCfg := &config.ServerConfig{Name: "a", Enabled: true}
	nsErase := readSpec("ns:erase")
	if configDenied {
		serverCfg.DisabledTools = []string{"ns:erase"}
	} else {
		nsErase.NoRecord = true
	}
	f := newTierMatrixFixture(t, false, serverCfg, readSpec("erase"), nsErase)

	// Fixture guards: the pair must be exactly as described or a passing cell
	// proves nothing.
	require.Equal(t, contracts.OperationTypeRead, f.proxy.lookupToolPermission("a", "erase"))
	require.Equal(t, contracts.OperationTypeRead, f.proxy.lookupToolPermission("a", "ns:erase"), "same-tier pair")
	rec, err := f.proxy.storage.GetToolApproval("a", "erase")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
	if configDenied {
		require.True(t, f.rt.IsToolConfigDenied("a", "ns:erase"), "disabled_tools must deny the exact raw name")
		require.False(t, f.rt.IsToolConfigDenied("a", "erase"), "the sibling stays allowed")
	} else {
		_, err = f.proxy.storage.GetToolApproval("a", "ns:erase")
		require.Error(t, err, "ns:erase must have no record of its own")
		require.False(t, f.rt.IsToolConfigDenied("a", "ns:erase"))
	}
	return f
}

// assertPairedRefusal is the paired-row refusal oracle: zero upstream calls
// and a policy body (config denial names the operator policy; the unapproved
// sibling must answer with an approval-state body, never a dispatch result).
func assertPairedRefusal(t *testing.T, oracle upstreamOracle, configDenied bool, text string) {
	t.Helper()
	oracle.refused(t)
	if configDenied {
		assert.Contains(t, text, "denied by server config", "config denial must be reported as operator policy")
	} else {
		assert.Contains(t, text, "approv", "an unapproved namespaced tool must be refused on approval grounds, not dispatched: %s", text)
	}
}

// TestScopeTargetTier_PairedNames_Retrieve drives the pair through the
// registered call_tool_read handler for every paired caller.
func TestScopeTargetTier_PairedNames_Retrieve(t *testing.T) {
	for _, configDenied := range []bool{true, false} {
		mode := map[bool]string{true: "config-denied", false: "unapproved"}[configDenied]
		t.Run("ns:erase "+mode, func(t *testing.T) {
			f := pairedFixture(t, configDenied)
			for _, caller := range pairedCallers() {
				t.Run(caller.name, func(t *testing.T) {
					oracle := f.oracle()
					result := f.retrieve(t, caller.ctx, contracts.ToolVariantRead, "a:erase")
					require.False(t, result.IsError, "erase is approved and must dispatch: %s", matrixText(result))
					oracle.admitted(t, "erase")

					oracle = f.oracle()
					result = f.retrieve(t, caller.ctx, contracts.ToolVariantRead, "a:ns:erase")
					assertPairedRefusal(t, oracle, configDenied, matrixText(result))
				})
			}
		})
	}
}

// TestScopeTargetTier_PairedNames_Direct drives the pair through the
// registered direct handlers for every paired caller.
func TestScopeTargetTier_PairedNames_Direct(t *testing.T) {
	for _, configDenied := range []bool{true, false} {
		mode := map[bool]string{true: "config-denied", false: "unapproved"}[configDenied]
		t.Run("ns:erase "+mode, func(t *testing.T) {
			f := pairedFixture(t, configDenied)
			f.stageDirectCatalog(t)
			for _, caller := range pairedCallers() {
				t.Run(caller.name, func(t *testing.T) {
					oracle := f.oracle()
					result := f.direct(t, caller.ctx, "erase")
					require.False(t, result.IsError, "erase is approved and must dispatch: %s", matrixText(result))
					oracle.admitted(t, "erase")

					oracle = f.oracle()
					result = f.direct(t, caller.ctx, "ns:erase")
					assertPairedRefusal(t, oracle, configDenied, matrixText(result))
				})
			}
		})
	}
}

// TestScopeTargetTier_PairedNames_Nested drives the pair through the real
// sandbox for every paired caller, asserting the envelope.
func TestScopeTargetTier_PairedNames_Nested(t *testing.T) {
	for _, configDenied := range []bool{true, false} {
		mode := map[bool]string{true: "config-denied", false: "unapproved"}[configDenied]
		t.Run("ns:erase "+mode, func(t *testing.T) {
			f := pairedFixture(t, configDenied)
			for _, caller := range pairedCallers() {
				t.Run(caller.name, func(t *testing.T) {
					oracle := f.oracle()
					env := f.nested(t, caller.ctx, "erase")
					require.True(t, env.OK, "erase is approved and must dispatch: %s %s", env.Code, env.Message)
					oracle.admitted(t, "erase")

					oracle = f.oracle()
					env = f.nested(t, caller.ctx, "ns:erase")
					require.False(t, env.OK, "ns:erase must be refused in the sandbox")
					assert.NotEmpty(t, env.Code)
					assertPairedRefusal(t, oracle, configDenied, env.Message)
				})
			}
		})
	}
}

// --- unresolved identity on a known server (research D4) --------------------

// TestScopeTargetTier_UnresolvedIdentity: a name the discovery snapshot of
// known server "a" does not contain is refused with the insufficient-
// permission body and zero upstream calls on retrieve and nested dispatch,
// for a full-tier server-restricted token, an unrestricted token and an
// administrator alike. Direct dispatch is exempt: an unregistered display
// name has no handler at all.
func TestScopeTargetTier_UnresolvedIdentity(t *testing.T) {
	f := newTierMatrixFixture(t, false, nil)
	_, found := f.proxy.lookupExactToolAnnotations("a", "ghost")
	require.False(t, found, "fixture: ghost must be absent from the snapshot")
	_, connected := f.proxy.upstreamManager.GetClient("a")
	require.True(t, connected, "fixture: the server itself is known and connected")

	for _, caller := range pairedCallers() {
		t.Run(caller.name, func(t *testing.T) {
			t.Run("retrieve", func(t *testing.T) {
				oracle := f.oracle()
				result := f.retrieve(t, caller.ctx, contracts.ToolVariantRead, "a:ghost")
				text := matrixText(result)
				require.True(t, result.IsError, "an unresolved identity must be refused: %s", text)
				assert.Contains(t, text, "Permission denied", "the refusal must carry the insufficient-permission body: %s", text)
				assert.NotContains(t, text, "CallTool failed", "the call must never be dispatched upstream")
				oracle.refused(t)
			})
			t.Run("nested", func(t *testing.T) {
				oracle := f.oracle()
				env := f.nested(t, caller.ctx, "ghost")
				require.False(t, env.OK, "an unresolved identity must be refused in the sandbox")
				assert.Equal(t, "PERMISSION_DENIED", env.Code, "the sandbox must answer with the permission envelope: %s", env.Message)
				oracle.refused(t)
			})
		})
	}

	// Control: the same callers reach a discovered read-tier tool.
	for _, caller := range pairedCallers() {
		oracle := f.oracle()
		result := f.retrieve(t, caller.ctx, contracts.ToolVariantRead, "a:read_thing")
		require.False(t, result.IsError, "control: %s must reach read_thing: %s", caller.name, matrixText(result))
		oracle.admitted(t, "read_thing")
	}
}
