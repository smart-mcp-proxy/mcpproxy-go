package server

import (
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// updateFailureStageCodes maps the tray's closed stage enum (spec 095) onto the
// diagnostics codes the heartbeat surfaces. The handler validates the enum too;
// this map is the authority for what may actually be persisted.
var updateFailureStageCodes = map[string]diagnostics.Code{
	"appcast":  diagnostics.UpdateAppcastFailed,
	"download": diagnostics.UpdateDownloadFailed,
	"install":  diagnostics.UpdateInstallFailed,
	"other":    diagnostics.UpdateOtherFailed,
}

// RecordUpdateFailure records one desktop auto-update failure occurrence
// (spec 095 FR-011/FR-012/FR-013). Gate evaluation and the durable increment
// happen inside one call so no window exists where telemetry is disabled
// between the two — an occurrence during opt-out can never become
// transmissible later.
//
// recorded=false means the gate was closed at event time and nothing was
// persisted; that is a deliberate no-op, and the caller still answers 204.
func (s *Server) RecordUpdateFailure(stage string) (bool, error) {
	if s.runtime == nil {
		return false, nil
	}

	var (
		store telemetry.DiagnosticsCounterStore
		db    *bbolt.DB
	)
	if ts := s.runtime.TelemetryService(); ts != nil {
		store = ts.DiagnosticsCounterStore()
		db = ts.DiagnosticsCounterDB()
	}

	cfg, _ := s.runtime.GetCurrentConfig().(*config.Config)
	return recordUpdateFailure(cfg, httpapi.GetBuildVersion(), store, db, stage)
}

// recordUpdateFailure is the testable core: gate, map, persist. Kept free of
// *Server so every gate can be exercised against a real BBolt store without
// standing up a runtime.
func recordUpdateFailure(
	cfg *config.Config,
	version string,
	store telemetry.DiagnosticsCounterStore,
	db *bbolt.DB,
	stage string,
) (bool, error) {
	code, ok := updateFailureStageCodes[stage]
	if !ok {
		return false, fmt.Errorf("unknown update failure stage %q", stage)
	}

	// Event-time gate (FR-013): config opt-out, env opt-outs (DO_NOT_TRACK /
	// CI / MCPPROXY_TELEMETRY), and dev builds. EffectiveTelemetryEnabled and
	// IsValidSemverVersion are the same predicates the heartbeat itself uses,
	// so this never admits an occurrence the heartbeat would refuse to send.
	if !telemetry.EffectiveTelemetryEnabled(cfg) || !telemetry.IsValidSemverVersion(version) {
		return false, nil
	}
	if store == nil || db == nil {
		// Telemetry counters are not wired (short-lived CLI process).
		return false, nil
	}

	// Synchronous: 204 promises the increment is durable (FR-011).
	if err := store.RecordErrorCode(db, string(code)); err != nil {
		return false, err
	}
	return true, nil
}
