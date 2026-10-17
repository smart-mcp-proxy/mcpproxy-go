package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// Spec 081 T4 (US5, FR-018) — surface-state consistency.
//
// The core's own projection, GET /api/v1/servers, is the reference. Every
// other surface is read through ITS OWN path and reduced to a surfaceRow, and
// compareSurfaces reports each (surface, server, field) where the surface
// disagrees with the core:
//
//	surface  read path                               fields compared
//	cli      `mcpproxy upstream list -o json`        enabled, quarantined, connected, connection_state,
//	         (daemon client mode → REST rows)        health.level, health.admin_state, health.status, tool_count
//	mcp      /mcp `upstream_servers` operation=list  enabled, quarantined, connection_state,
//	         (storage + upstream manager + runtime   health.level, health.admin_state, health.status
//	         health — an independent projection;    (no tool_count: the MCP row does not carry one)
//	         see internal/server/mcp.go handleListUpstreams)
//	webui    rendered server cards on /ui/servers    enabled (⋯ menu Enable/Disable), quarantined (⋯ Review),
//	         (e2e/web-ui-sweep/surface-snapshot.mjs) tool_count, webui.chip_class, webui.chip_text
//	tray     — Spec 081 T3 (macOS runner)            not compared here; recorded as not-compared
//
// The Web UI never prints health.level as text (Spec 109 FR-011), so its
// expected row is DERIVED from the core row with the same rules ServerCard.vue
// applies (expectedWebUIRow): chip colour from admin_state/level/sign-in, chip
// text from summary → status label.

// surfaceRow is one server's state as one surface shows it: field → value.
type surfaceRow map[string]string

// surfaceSnapshot is one capture of one surface. Err is set when the surface
// could not be read at all (that is itself a disagreement).
type surfaceSnapshot struct {
	Surface string                `json:"surface"`
	Rows    map[string]surfaceRow `json:"rows,omitempty"`
	Err     string                `json:"error,omitempty"`
}

// fieldMismatch names the surface, server, field and both values (FR-018).
type fieldMismatch struct {
	Surface string `json:"surface"`
	Server  string `json:"server"`
	Field   string `json:"field"`
	Core    string `json:"core"`
	Got     string `json:"got"`
}

const (
	surfaceCore  = "core"
	surfaceCLI   = "cli"
	surfaceMCP   = "mcp"
	surfaceWebUI = "webui"
	surfaceTray  = "tray"
)

const (
	fieldPresence    = "presence"
	fieldCapture     = "capture"
	fieldEnabled     = "enabled"
	fieldQuarantined = "quarantined"
	fieldConnected   = "connected"
	fieldConnection  = "connection_state"
	fieldLevel       = "health.level"
	fieldAdmin       = "health.admin_state"
	fieldStatus      = "health.status"
	fieldToolCount   = "tool_count"
	fieldChipClass   = "webui.chip_class"
	fieldChipText    = "webui.chip_text"

	// Inputs to the Web UI derivation; never compared directly.
	hiddenSummary        = "_health.summary"
	hiddenHealthAction   = "_health.action"
	hiddenDiagnosticCode = "_diagnostic.code"
)

// surfaceFields lists what each surface is compared on.
var surfaceFields = map[string][]string{
	surfaceCLI:   {fieldEnabled, fieldQuarantined, fieldConnected, fieldConnection, fieldLevel, fieldAdmin, fieldStatus, fieldToolCount},
	surfaceMCP:   {fieldEnabled, fieldQuarantined, fieldConnection, fieldLevel, fieldAdmin, fieldStatus},
	surfaceWebUI: {fieldEnabled, fieldQuarantined, fieldToolCount, fieldChipClass, fieldChipText},
}

// rawServer is the union of the server-row shapes REST, CLI and MCP emit.
type rawServer struct {
	Name             string `json:"name"`
	Enabled          *bool  `json:"enabled"`
	Quarantined      *bool  `json:"quarantined"`
	Connected        *bool  `json:"connected"`
	Status           string `json:"status"`
	ToolCount        *int   `json:"tool_count"`
	ConnectionStatus *struct {
		State string `json:"state"`
	} `json:"connection_status"`
	Health *struct {
		Level      string `json:"level"`
		AdminState string `json:"admin_state"`
		Status     string `json:"status"`
		Summary    string `json:"summary"`
		Action     string `json:"action"`
	} `json:"health"`
	Diagnostic *struct {
		Code string `json:"code"`
	} `json:"diagnostic"`
}

// row reduces a raw server to a surfaceRow. connState is the surface's own
// connection-state field (REST/CLI `status`, MCP connection_status.state).
func (s rawServer) row(connState string, withToolCount, withConnected bool) surfaceRow {
	r := surfaceRow{}
	if s.Enabled != nil {
		r[fieldEnabled] = strconv.FormatBool(*s.Enabled)
	}
	if s.Quarantined != nil {
		r[fieldQuarantined] = strconv.FormatBool(*s.Quarantined)
	}
	if withConnected && s.Connected != nil {
		r[fieldConnected] = strconv.FormatBool(*s.Connected)
	}
	r[fieldConnection] = normalizeConnState(connState)
	if withToolCount && s.ToolCount != nil {
		r[fieldToolCount] = strconv.Itoa(*s.ToolCount)
	}
	if s.Health != nil {
		r[fieldLevel] = s.Health.Level
		r[fieldAdmin] = s.Health.AdminState
		r[fieldStatus] = s.Health.Status
		r[hiddenSummary] = s.Health.Summary
		r[hiddenHealthAction] = s.Health.Action
	}
	if s.Diagnostic != nil {
		r[hiddenDiagnosticCode] = s.Diagnostic.Code
	}
	return r
}

// normalizeConnState folds the casing difference between the StateView
// (REST) and the managed client's State.String() (MCP: "Ready", "Pending Auth").
func normalizeConnState(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_")
}

func rowsByName(servers []rawServer, conv func(rawServer) surfaceRow) (map[string]surfaceRow, error) {
	out := make(map[string]surfaceRow, len(servers))
	for _, s := range servers {
		if s.Name == "" {
			return nil, fmt.Errorf("server row without a name")
		}
		if _, dup := out[s.Name]; dup {
			return nil, fmt.Errorf("server %q listed twice", s.Name)
		}
		out[s.Name] = conv(s)
	}
	return out, nil
}

func restLikeRow(s rawServer) surfaceRow { return s.row(s.Status, true, true) }

// coreRowsFromREST parses the GET /api/v1/servers envelope (the reference).
func coreRowsFromREST(body []byte) (map[string]surfaceRow, error) {
	var env struct {
		Data struct {
			Servers []rawServer `json:"servers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse /api/v1/servers: %w", err)
	}
	return rowsByName(env.Data.Servers, restLikeRow)
}

// cliRowsFromJSON parses `mcpproxy upstream list -o json` stdout.
func cliRowsFromJSON(out []byte) (map[string]surfaceRow, error) {
	var servers []rawServer
	if err := json.Unmarshal(out, &servers); err != nil {
		return nil, fmt.Errorf("parse `upstream list -o json` output: %w (output: %s)", err, truncateStr(string(out), 300))
	}
	return rowsByName(servers, restLikeRow)
}

// mcpRowsFromText parses the text content of upstream_servers operation=list.
func mcpRowsFromText(text string) (map[string]surfaceRow, error) {
	var env struct {
		Servers []rawServer `json:"servers"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		return nil, fmt.Errorf("parse upstream_servers list: %w (text: %s)", err, truncateStr(text, 300))
	}
	return rowsByName(env.Servers, func(s rawServer) surfaceRow {
		state := ""
		if s.ConnectionStatus != nil {
			state = s.ConnectionStatus.State
		}
		return s.row(state, false, false)
	})
}

// webuiCard is one rendered server card (surface-snapshot.mjs output).
type webuiCard struct {
	Name        string `json:"name"`
	ChipClass   string `json:"chip_class"`
	ChipText    string `json:"chip_text"`
	ToolCount   *int   `json:"tool_count"`
	Enabled     *bool  `json:"enabled"`
	Quarantined bool   `json:"quarantined"`
}

// webuiRowsFromJSON parses one surface-snapshot.mjs response line.
func webuiRowsFromJSON(line []byte) (map[string]surfaceRow, error) {
	var resp struct {
		OK    bool        `json:"ok"`
		Error string      `json:"error"`
		Rows  []webuiCard `json:"rows"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse Web UI snapshot: %w (line: %s)", err, truncateStr(string(line), 300))
	}
	if !resp.OK {
		return nil, fmt.Errorf("web UI snapshot failed: %s", resp.Error)
	}
	out := make(map[string]surfaceRow, len(resp.Rows))
	for _, c := range resp.Rows {
		if c.Name == "" {
			return nil, fmt.Errorf("web UI card without a server name")
		}
		if _, dup := out[c.Name]; dup {
			return nil, fmt.Errorf("web UI renders server %q twice", c.Name)
		}
		r := surfaceRow{
			fieldChipClass:   c.ChipClass,
			fieldChipText:    c.ChipText,
			fieldQuarantined: strconv.FormatBool(c.Quarantined),
		}
		if c.Enabled != nil {
			r[fieldEnabled] = strconv.FormatBool(*c.Enabled)
		}
		if c.ToolCount != nil {
			r[fieldToolCount] = strconv.Itoa(*c.ToolCount)
		}
		out[c.Name] = r
	}
	return out, nil
}

// oauthReauthCodes mirrors frontend/src/utils/health.ts OAuthReauthCodes.
var oauthReauthCodes = map[string]bool{
	"MCPX_OAUTH_REAUTH_REQUIRED": true,
	"MCPX_OAUTH_REFRESH_EXPIRED": true,
	"MCPX_OAUTH_REFRESH_403":     true,
}

// expectedWebUIRow derives what ServerCard.vue must render for a core row
// (statusBadgeClass / statusText; oauthSignInState from utils/health.ts).
func expectedWebUIRow(core surfaceRow) surfaceRow {
	code := core[hiddenDiagnosticCode]
	signIn := oauthReauthCodes[code] || core[hiddenHealthAction] == health.ActionLogin ||
		code == "MCPX_OAUTH_LOGIN_REQUIRED"
	admin := core[fieldAdmin]

	var chip string
	switch {
	case admin == health.StateDisabled:
		chip = "badge-neutral"
	case signIn:
		chip = "badge-warning"
	case admin == health.StateQuarantined:
		chip = "badge-secondary"
	default:
		switch core[fieldLevel] {
		case health.LevelHealthy:
			chip = "badge-success"
		case health.LevelDegraded:
			chip = "badge-warning"
		case health.LevelUnhealthy:
			chip = "badge-error"
		default:
			chip = "badge-ghost"
		}
	}

	var text string
	switch {
	case signIn && admin != health.StateDisabled:
		text = health.StatusLabel(health.StatusSignInRequired)
	case core[hiddenSummary] != "":
		text = core[hiddenSummary]
	case core[fieldStatus] != "":
		text = health.StatusLabel(core[fieldStatus])
	case core[fieldConnected] == "true":
		text = "Connected"
	default:
		text = "Disconnected"
	}

	return surfaceRow{
		fieldEnabled:     core[fieldEnabled],
		fieldQuarantined: core[fieldQuarantined],
		fieldToolCount:   core[fieldToolCount],
		fieldChipClass:   chip,
		fieldChipText:    text,
	}
}

// connectionStateMeaningful: a disabled or quarantined server has no live
// connection both projections are obliged to agree on ("Not Started" in the
// manager vs "disabled" in the StateView is not a user-visible divergence;
// the admin state and health fields cover those servers).
func connectionStateMeaningful(core surfaceRow) bool {
	return core[fieldEnabled] == "true" && core[fieldQuarantined] == "false"
}

// compareSurfaces compares every surface snapshot against the core rows.
// The result is sorted (surface, server, field) so diffs are stable.
func compareSurfaces(core map[string]surfaceRow, snaps []surfaceSnapshot) []fieldMismatch {
	var out []fieldMismatch
	for _, snap := range snaps {
		if snap.Err != "" {
			out = append(out, fieldMismatch{Surface: snap.Surface, Server: "*", Field: fieldCapture,
				Core: "readable", Got: snap.Err})
			continue
		}
		fields := surfaceFields[snap.Surface]
		for name, coreRow := range core {
			got, ok := snap.Rows[name]
			if !ok {
				out = append(out, fieldMismatch{Surface: snap.Surface, Server: name, Field: fieldPresence,
					Core: "present", Got: "absent"})
				continue
			}
			want := coreRow
			if snap.Surface == surfaceWebUI {
				want = expectedWebUIRow(coreRow)
			}
			for _, f := range fields {
				if f == fieldConnection && !connectionStateMeaningful(coreRow) {
					continue
				}
				if want[f] != got[f] {
					out = append(out, fieldMismatch{Surface: snap.Surface, Server: name, Field: f,
						Core: want[f], Got: got[f]})
				}
			}
		}
		for name := range snap.Rows {
			if _, ok := core[name]; !ok {
				out = append(out, fieldMismatch{Surface: snap.Surface, Server: name, Field: fieldPresence,
					Core: "absent", Got: "present"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Surface != b.Surface {
			return a.Surface < b.Surface
		}
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		return a.Field < b.Field
	})
	return out
}

// formatMismatches renders a per-field diff, one line per mismatch.
func formatMismatches(mm []fieldMismatch) string {
	sorted := append([]fieldMismatch(nil), mm...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Surface != sorted[j].Surface {
			return sorted[i].Surface < sorted[j].Surface
		}
		return sorted[i].Server < sorted[j].Server
	})
	lines := make([]string, 0, len(sorted))
	for _, m := range sorted {
		lines = append(lines, fmt.Sprintf("%s %s %s: core=%s %s=%s", m.Surface, m.Server, m.Field, m.Core, m.Surface, m.Got))
	}
	return strings.Join(lines, "\n")
}

// seedMismatch overrides one captured field on every capture so the check's
// negative path can be proven end-to-end (`--seed-mismatch`, workflow_dispatch
// input `seed_consistency_mismatch`). Never set on a tag run.
type seedMismatch struct {
	Surface, Server, Field, Value string
}

// parseSeedMismatch parses "surface:server:field=value".
func parseSeedMismatch(s string) (*seedMismatch, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("seed %q: want surface:server:field=value", s)
	}
	fv := strings.SplitN(parts[2], "=", 2)
	if len(fv) != 2 || fv[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("seed %q: want surface:server:field=value", s)
	}
	if _, ok := surfaceFields[parts[0]]; !ok {
		return nil, fmt.Errorf("seed %q: unknown surface %q (cli, mcp, webui)", s, parts[0])
	}
	return &seedMismatch{Surface: parts[0], Server: parts[1], Field: fv[0], Value: fv[1]}, nil
}

func applySeed(seed *seedMismatch, snaps []surfaceSnapshot) {
	if seed == nil {
		return
	}
	for i := range snaps {
		if snaps[i].Surface != seed.Surface || snaps[i].Rows == nil {
			continue
		}
		if row, ok := snaps[i].Rows[seed.Server]; ok {
			row[seed.Field] = seed.Value
		}
	}
}
