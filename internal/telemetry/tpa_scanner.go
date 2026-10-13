package telemetry

// tpaSeverityKeys is the fixed severity enum emitted under
// tpa_scanner.findings. It mirrors the scanner's severity constants
// (internal/security/scanner/types.go) but is duplicated here deliberately:
// the telemetry package must not import the scanner package, and the enum is
// the anonymity contract — anything outside this list is dropped rather than
// transmitted.
var tpaSeverityKeys = []string{"critical", "high", "medium", "low", "info"}

// tpaSeverityAllowList is the set form of tpaSeverityKeys.
var tpaSeverityAllowList = func() map[string]struct{} {
	m := make(map[string]struct{}, len(tpaSeverityKeys))
	for _, k := range tpaSeverityKeys {
		m[k] = struct{}{}
	}
	return m
}()

// IsTPASeverity reports whether sev is a member of the fixed severity enum
// permitted in the heartbeat's tpa_scanner.findings map.
func IsTPASeverity(sev string) bool {
	_, ok := tpaSeverityAllowList[sev]
	return ok
}

// TPAScannerStats is the security-scanner sub-object of the heartbeat
// payload. It answers "is the TPA / security scanner actually running in the
// fleet, does it fail, and does it find anything?" using counts alone.
//
// Unit of measure for the v8 job counters (ScansCompleted/ScansFailed/
// ScansWithFindings/Findings): ONE NON-DEEP-SCAN (PASS 1) SCAN JOB. The
// Pass-2 deep supply-chain audit and dry-run jobs are not counted, and a job
// with several failing scanners counts once — see internal/security/scanner
// (scanCallbackAdapter.countsForTelemetry), the only producer.
//
// The v9 funnel counters (ToolChangeGateScans/PromptScans) have a DIFFERENT
// unit — one synchronous scan of one changed tool / one aggregated prompt —
// and different producers. They exist because those two paths run for
// ordinary users who never start a scan job, so the v8 counters alone read as
// "the fleet never scans". Do not sum the two groups.
//
// Privacy contract (enforced by ScanForPII, rule "v8_field_invalid"):
//   - every value is a non-negative integer count;
//   - Findings keys are drawn ONLY from the fixed severity enum
//     (critical/high/medium/low/info);
//   - no server names, scanner ids, rule ids, finding titles, paths, or any
//     other free text ever reaches this struct — the registry's Record*
//     methods do not even accept them.
type TPAScannerStats struct {
	// ScansCompleted is the number of terminal, successful scans in the
	// reporting window (counters reset after each accepted heartbeat).
	ScansCompleted int64 `json:"scans_completed"`
	// ScansFailed is the number of terminal scan failures in the window.
	ScansFailed int64 `json:"scans_failed"`
	// ScansWithFindings is the subset of ScansCompleted that produced at
	// least one finding of any severity.
	ScansWithFindings int64 `json:"scans_with_findings"`
	// Findings is the per-severity finding total across all completed scans
	// in the window. Sparse: severities with a zero total are omitted.
	Findings map[string]int64 `json:"findings,omitempty"`

	// ToolChangeGateScans (schema v9) is the number of SYNCHRONOUS trust_mode:
	// scan tool-change gate scans in the window — one per changed tool put
	// through internal/runtime.scanChangeIsClean. Unlike the job counters
	// above, this path runs inline on the config/tool-refresh hot path for
	// ordinary users, so it is the first TPA counter most installs ever move.
	// It counts gate INVOCATIONS, not outcomes: whether the gate approved or
	// held the change is deliberately not transmitted.
	ToolChangeGateScans int64 `json:"tool_change_gate_scans"`
	// PromptScans (schema v9) is the number of aggregated upstream PROMPTS put
	// through the poisoning filter in the window — one per prompt scanned by
	// internal/server.scanAggregatedPrompts, counted per prompt (not per
	// refresh). Same posture as ToolChangeGateScans: invocations only, never
	// the prompt name, the server, or the verdict.
	PromptScans int64 `json:"prompt_scans"`
}

// isZero reports whether nothing at all was recorded, in which case the
// heartbeat omits the whole sub-object (same posture as DiagnosticsCounters).
func (t *TPAScannerStats) isZero() bool {
	if t == nil {
		return true
	}
	if t.ScansCompleted != 0 || t.ScansFailed != 0 || t.ScansWithFindings != 0 {
		return false
	}
	if t.ToolChangeGateScans != 0 || t.PromptScans != 0 {
		return false
	}
	for _, n := range t.Findings {
		if n != 0 {
			return false
		}
	}
	return true
}
