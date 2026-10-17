package main

import (
	"strings"
	"testing"
)

// restFixture is a /api/v1/servers payload in a known mixed state: one healthy
// server, one quarantined, one disabled, one unhealthy.
const restFixture = `{"success":true,"data":{"servers":[
 {"name":"stdio-ok","enabled":true,"quarantined":false,"connected":true,"status":"Ready","tool_count":3,
  "health":{"level":"healthy","admin_state":"enabled","status":"ready","summary":"Connected (3 tools)","actions":[]}},
 {"name":"quar","enabled":true,"quarantined":true,"connected":false,"status":"Disconnected","tool_count":0,
  "health":{"level":"healthy","admin_state":"quarantined","status":"needs_review","summary":"Quarantined for review","action":"approve","actions":["approve"]}},
 {"name":"off","enabled":false,"quarantined":false,"connected":false,"status":"disabled","tool_count":0,
  "health":{"level":"healthy","admin_state":"disabled","status":"disabled","summary":"Disabled","action":"enable","actions":["enable"]}},
 {"name":"broken","enabled":true,"quarantined":false,"connected":false,"status":"Error","tool_count":0,
  "health":{"level":"unhealthy","admin_state":"enabled","status":"error","summary":"Connection refused","action":"restart","actions":["restart"]}}
]}}`

// The CLI's `upstream list -o json` prints the raw REST rows (sorted).
const cliFixture = `[
 {"name":"broken","enabled":true,"quarantined":false,"connected":false,"status":"Error","tool_count":0,
  "health":{"level":"unhealthy","admin_state":"enabled","status":"error","summary":"Connection refused"}},
 {"name":"off","enabled":false,"quarantined":false,"connected":false,"status":"disabled","tool_count":0,
  "health":{"level":"healthy","admin_state":"disabled","status":"disabled","summary":"Disabled"}},
 {"name":"quar","enabled":true,"quarantined":true,"connected":false,"status":"Disconnected","tool_count":0,
  "health":{"level":"healthy","admin_state":"quarantined","status":"needs_review","summary":"Quarantined for review"}},
 {"name":"stdio-ok","enabled":true,"quarantined":false,"connected":true,"status":"Ready","tool_count":3,
  "health":{"level":"healthy","admin_state":"enabled","status":"ready","summary":"Connected (3 tools)"}}
]`

// MCP `upstream_servers list` text result (an independent read path).
const mcpFixture = `{"total":4,"servers":[
 {"name":"stdio-ok","enabled":true,"quarantined":false,"connection_status":{"state":"Ready"},
  "health":{"level":"healthy","admin_state":"enabled","status":"ready"}},
 {"name":"quar","enabled":true,"quarantined":true,"connection_status":{"state":"Disconnected"},
  "health":{"level":"healthy","admin_state":"quarantined","status":"needs_review"}},
 {"name":"off","enabled":false,"quarantined":false,"connection_status":{"state":"Not Started"},
  "health":{"level":"healthy","admin_state":"disabled","status":"disabled"}},
 {"name":"broken","enabled":true,"quarantined":false,"connection_status":{"state":"Error"},
  "health":{"level":"unhealthy","admin_state":"enabled","status":"error"}}
]}`

// What the Web UI reader (e2e/web-ui-sweep/surface-snapshot.mjs) extracts
// from the rendered server cards.
const webuiFixture = `{"ok":true,"rows":[
 {"name":"stdio-ok","chip_class":"badge-success","chip_text":"Connected (3 tools)","tool_count":3,"enabled":true,"quarantined":false},
 {"name":"quar","chip_class":"badge-secondary","chip_text":"Quarantined for review","tool_count":0,"enabled":true,"quarantined":true},
 {"name":"off","chip_class":"badge-neutral","chip_text":"Disabled","tool_count":0,"enabled":false,"quarantined":false},
 {"name":"broken","chip_class":"badge-error","chip_text":"Connection refused","tool_count":0,"enabled":true,"quarantined":false}
]}`

func mustSnapshots(t *testing.T, cli, mcp, webui string) (map[string]surfaceRow, []surfaceSnapshot) {
	t.Helper()
	core, err := coreRowsFromREST([]byte(restFixture))
	if err != nil {
		t.Fatalf("parse REST: %v", err)
	}
	cliRows, err := cliRowsFromJSON([]byte(cli))
	if err != nil {
		t.Fatalf("parse CLI: %v", err)
	}
	mcpRows, err := mcpRowsFromText(mcp)
	if err != nil {
		t.Fatalf("parse MCP: %v", err)
	}
	webRows, err := webuiRowsFromJSON([]byte(webui))
	if err != nil {
		t.Fatalf("parse Web UI: %v", err)
	}
	return core, []surfaceSnapshot{
		{Surface: surfaceCLI, Rows: cliRows},
		{Surface: surfaceMCP, Rows: mcpRows},
		{Surface: surfaceWebUI, Rows: webRows},
	}
}

func TestCompareSurfaces_AgreeingMixedState(t *testing.T) {
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, webuiFixture)
	if len(core) != 4 {
		t.Fatalf("core rows = %d, want 4", len(core))
	}
	if mm := compareSurfaces(core, snaps); len(mm) != 0 {
		t.Fatalf("agreeing surfaces reported mismatches: %+v", mm)
	}
}

func TestCompareSurfaces_CLIStaleHealthLevel(t *testing.T) {
	stale := strings.Replace(cliFixture, `"level":"unhealthy"`, `"level":"healthy"`, 1)
	core, snaps := mustSnapshots(t, stale, mcpFixture, webuiFixture)
	mm := compareSurfaces(core, snaps)
	want := fieldMismatch{Surface: surfaceCLI, Server: "broken", Field: fieldLevel, Core: "unhealthy", Got: "healthy"}
	if len(mm) != 1 || mm[0] != want {
		t.Fatalf("mismatches = %+v, want exactly %+v", mm, want)
	}
}

func TestCompareSurfaces_MCPConnectionStateDiverges(t *testing.T) {
	bad := strings.Replace(mcpFixture, `"state":"Ready"`, `"state":"Connecting"`, 1)
	core, snaps := mustSnapshots(t, cliFixture, bad, webuiFixture)
	mm := compareSurfaces(core, snaps)
	if len(mm) != 1 || mm[0].Surface != surfaceMCP || mm[0].Server != "stdio-ok" || mm[0].Field != fieldConnection {
		t.Fatalf("mismatches = %+v, want one mcp/stdio-ok/%s", mm, fieldConnection)
	}
}

func TestCompareSurfaces_WebUIShowsQuarantinedAsHealthy(t *testing.T) {
	bad := strings.Replace(webuiFixture, `"chip_class":"badge-secondary"`, `"chip_class":"badge-success"`, 1)
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, bad)
	mm := compareSurfaces(core, snaps)
	want := fieldMismatch{Surface: surfaceWebUI, Server: "quar", Field: fieldChipClass, Core: "badge-secondary", Got: "badge-success"}
	if len(mm) != 1 || mm[0] != want {
		t.Fatalf("mismatches = %+v, want exactly %+v", mm, want)
	}
}

func TestCompareSurfaces_WebUIStaleToolCount(t *testing.T) {
	bad := strings.Replace(webuiFixture, `"tool_count":3`, `"tool_count":0`, 1)
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, bad)
	mm := compareSurfaces(core, snaps)
	if len(mm) != 1 || mm[0].Field != fieldToolCount || mm[0].Core != "3" || mm[0].Got != "0" {
		t.Fatalf("mismatches = %+v, want one tool_count 3 vs 0", mm)
	}
}

func TestCompareSurfaces_MissingAndExtraServers(t *testing.T) {
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, webuiFixture)
	delete(snaps[0].Rows, "off")                              // CLI lost a server
	snaps[2].Rows["ghost"] = surfaceRow{fieldEnabled: "true"} // Web UI shows a removed one
	mm := compareSurfaces(core, snaps)
	var sawMissing, sawExtra bool
	for _, m := range mm {
		if m.Surface == surfaceCLI && m.Server == "off" && m.Field == fieldPresence && m.Got == "absent" {
			sawMissing = true
		}
		if m.Surface == surfaceWebUI && m.Server == "ghost" && m.Field == fieldPresence && m.Core == "absent" {
			sawExtra = true
		}
	}
	if !sawMissing || !sawExtra || len(mm) != 2 {
		t.Fatalf("mismatches = %+v, want missing cli/off and extra webui/ghost", mm)
	}
}

func TestCompareSurfaces_SurfaceCaptureErrorFails(t *testing.T) {
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, webuiFixture)
	snaps[0] = surfaceSnapshot{Surface: surfaceCLI, Err: "exit status 1: no daemon"}
	mm := compareSurfaces(core, snaps)
	if len(mm) != 1 || mm[0].Surface != surfaceCLI || mm[0].Field != fieldCapture {
		t.Fatalf("mismatches = %+v, want one cli capture failure", mm)
	}
}

func TestExpectedWebUIChip(t *testing.T) {
	cases := []struct {
		name string
		row  surfaceRow
		want string
	}{
		{"disabled", surfaceRow{fieldAdmin: "disabled", fieldLevel: "unhealthy"}, "badge-neutral"},
		{"quarantined", surfaceRow{fieldAdmin: "quarantined", fieldLevel: "healthy"}, "badge-secondary"},
		{"quarantined needs login", surfaceRow{fieldAdmin: "quarantined", hiddenHealthAction: "login"}, "badge-warning"},
		{"healthy", surfaceRow{fieldAdmin: "enabled", fieldLevel: "healthy"}, "badge-success"},
		{"degraded", surfaceRow{fieldAdmin: "enabled", fieldLevel: "degraded"}, "badge-warning"},
		{"unhealthy", surfaceRow{fieldAdmin: "enabled", fieldLevel: "unhealthy"}, "badge-error"},
		{"reauth diagnostic", surfaceRow{fieldAdmin: "enabled", fieldLevel: "unhealthy", hiddenDiagnosticCode: "MCPX_OAUTH_REFRESH_EXPIRED"}, "badge-warning"},
		{"unknown level", surfaceRow{fieldAdmin: "enabled", fieldLevel: "weird"}, "badge-ghost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expectedWebUIRow(tc.row)[fieldChipClass]; got != tc.want {
				t.Fatalf("chip class = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpectedWebUIChipText(t *testing.T) {
	// summary wins; sign-in overrides it unless disabled; else the status label.
	if got := expectedWebUIRow(surfaceRow{fieldAdmin: "enabled", hiddenSummary: "Connected (3 tools)", fieldStatus: "ready"})[fieldChipText]; got != "Connected (3 tools)" {
		t.Fatalf("summary text = %q", got)
	}
	if got := expectedWebUIRow(surfaceRow{fieldAdmin: "enabled", hiddenHealthAction: "login", hiddenSummary: "x"})[fieldChipText]; got != "Sign-in required" {
		t.Fatalf("sign-in text = %q", got)
	}
	if got := expectedWebUIRow(surfaceRow{fieldAdmin: "enabled", fieldStatus: "ready"})[fieldChipText]; got == "" || got == "ready" {
		t.Fatalf("status-label fallback = %q, want the human label", got)
	}
}

func TestApplySeedMismatch(t *testing.T) {
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, webuiFixture)
	seed, err := parseSeedMismatch("cli:stdio-ok:health.level=degraded")
	if err != nil {
		t.Fatalf("parse seed: %v", err)
	}
	applySeed(seed, snaps)
	mm := compareSurfaces(core, snaps)
	want := fieldMismatch{Surface: surfaceCLI, Server: "stdio-ok", Field: fieldLevel, Core: "healthy", Got: "degraded"}
	if len(mm) != 1 || mm[0] != want {
		t.Fatalf("seeded mismatch = %+v, want %+v", mm, want)
	}
	for _, bad := range []string{"", "cli", "cli:x", "cli:x:field", "nope:x:f=v"} {
		if _, err := parseSeedMismatch(bad); err == nil {
			t.Fatalf("parseSeedMismatch(%q) accepted", bad)
		}
	}
}

func TestMismatchReportIsSortedAndNamesBothValues(t *testing.T) {
	mm := []fieldMismatch{
		{Surface: surfaceWebUI, Server: "b", Field: fieldLevel, Core: "x", Got: "y"},
		{Surface: surfaceCLI, Server: "a", Field: fieldLevel, Core: "healthy", Got: "degraded"},
	}
	s := formatMismatches(mm)
	if !strings.HasPrefix(s, "cli a health.level: core=healthy cli=degraded") {
		t.Fatalf("formatted diff = %q", s)
	}
}

// Sol 6.1 round 1, finding 1: connection state stays compared for disabled
// and quarantined servers — exactly for the CLI (same projection as core),
// as connected-vs-not for MCP (whose vocabulary for an inactive server
// differs: "Not Started"/"Disconnected" vs the StateView's "idle"/"disabled").
func TestCompareSurfaces_InactiveServerConnectionState(t *testing.T) {
	core, snaps := mustSnapshots(t, cliFixture, mcpFixture, webuiFixture)
	if mm := compareSurfaces(core, snaps); len(mm) != 0 {
		t.Fatalf("vocabulary-only difference flagged: %+v", mm)
	}

	badCLI := strings.Replace(cliFixture, `"status":"disabled"`, `"status":"Ready"`, 1)
	core, snaps = mustSnapshots(t, badCLI, mcpFixture, webuiFixture)
	mm := compareSurfaces(core, snaps)
	if len(mm) != 1 || mm[0].Surface != surfaceCLI || mm[0].Server != "off" || mm[0].Field != fieldConnection {
		t.Fatalf("CLI disabled-server state divergence: got %+v", mm)
	}

	badMCP := strings.Replace(mcpFixture, `"state":"Not Started"`, `"state":"Ready"`, 1)
	core, snaps = mustSnapshots(t, cliFixture, badMCP, webuiFixture)
	mm = compareSurfaces(core, snaps)
	if len(mm) != 1 || mm[0].Surface != surfaceMCP || mm[0].Server != "off" || mm[0].Field != fieldConnection {
		t.Fatalf("MCP disabled server reported Ready: got %+v", mm)
	}
}

// Sol 6.1 round 1, finding 2: the Web UI expectation mirrors ServerCard.vue's
// no-health branch and healthStatusText's usable=false fallback.
func TestExpectedWebUIRow_Fallbacks(t *testing.T) {
	cases := []struct {
		name           string
		row            surfaceRow
		chip, chipText string
	}{
		{"no health, connected", surfaceRow{fieldConnected: "true"}, "badge-success", "Connected"},
		{"no health, connecting", surfaceRow{fieldConnected: "false", hiddenConnecting: "true"}, "badge-warning", "Connecting"},
		{"no health, down", surfaceRow{fieldConnected: "false"}, "badge-error", "Disconnected"},
		{"no health, sign-in", surfaceRow{fieldConnected: "false", hiddenDiagnosticCode: "MCPX_OAUTH_LOGIN_REQUIRED"}, "badge-warning", "Sign-in required"},
		{"health, unusable but connected", surfaceRow{hiddenHasHealth: "true", fieldAdmin: "enabled", fieldLevel: "degraded",
			fieldConnected: "true", hiddenUsable: "false"}, "badge-warning", "Unavailable"},
		{"health, empty text, connected", surfaceRow{hiddenHasHealth: "true", fieldAdmin: "enabled", fieldLevel: "healthy",
			fieldConnected: "true", hiddenUsable: "true"}, "badge-success", "Connected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expectedWebUIRow(tc.row)
			if got[fieldChipClass] != tc.chip || got[fieldChipText] != tc.chipText {
				t.Fatalf("got chip=%q text=%q, want %q %q", got[fieldChipClass], got[fieldChipText], tc.chip, tc.chipText)
			}
		})
	}
}

// Sol 6.1 round 1, finding 3: a seed that cannot change the verdict is
// refused rather than silently passing the "prove it fails" run.
func TestSeedMismatch_RefusesNoOpSeeds(t *testing.T) {
	if _, err := parseSeedMismatch("cli:x:health.leveel=degraded"); err == nil {
		t.Fatal("a field the surface is not compared on was accepted")
	}
	if _, err := parseSeedMismatch("mcp:x:tool_count=9"); err == nil {
		t.Fatal("tool_count is not compared for mcp, seed must be refused")
	}
	_, snaps := mustSnapshots(t, cliFixture, mcpFixture, webuiFixture)
	seed, _ := parseSeedMismatch("cli:no-such-server:health.level=degraded")
	if applySeed(seed, snaps) {
		t.Fatal("seed for an absent server reported as applied")
	}
	seed, _ = parseSeedMismatch("cli:stdio-ok:health.level=degraded")
	if !applySeed(seed, snaps) {
		t.Fatal("valid seed not applied")
	}
}

// Sol 6.1 round 1 (livecmd finding 1): the CLI child must not inherit any
// MCPPROXY_* override that could point it at another daemon (MCPPROXY_DATA
// selects a socket ahead of the configured TCP endpoint).
func TestCLIEnvDropsEveryMCPProxyOverride(t *testing.T) {
	env := cliChildEnv([]string{"PATH=/bin", "MCPPROXY_DATA=/home/u/.mcpproxy", "MCPPROXY_TRAY_ENDPOINT=x", "HOME=/h"})
	for _, kv := range env {
		if strings.HasPrefix(kv, "MCPPROXY_") {
			t.Fatalf("CLI env kept %s", kv)
		}
	}
	if len(env) != 2 {
		t.Fatalf("non-mcpproxy vars dropped: %v", env)
	}
}
