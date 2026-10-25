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

	// Schema v16 (Spec 087 / Spec 101 US3): TPA bundle refresh counters. Each is
	// a non-negative integer in the reporting window; omitted when zero so an
	// install that never refreshes keeps its v15 tpa_scanner shape. They carry
	// no age, URL, host, reason or digest (FR-021, SC-006); see
	// RecordTPABundleRefreshOn for the unit of each.
	BundleRefreshCycles      int64 `json:"bundle_refresh_cycles,omitempty"`
	BundleRefreshActivations int64 `json:"bundle_refresh_activations,omitempty"`
	BundleRefreshRejections  int64 `json:"bundle_refresh_rejections,omitempty"`
	BundleRefreshUnavailable int64 `json:"bundle_refresh_unavailable,omitempty"`
	BundleReprotectHits      int64 `json:"bundle_reprotect_hits,omitempty"`

	// Schema v15 (Spec 101 FR-021): the ACTIVE bundle's provenance tuple. This
	// is per-heartbeat STATE merged from the TPABundleSnapshot provider after
	// the counters are applied; it is not a reset-on-accept counter. Typed and
	// kept in its own group so tpaScannerScalarKeys stays integer-only. The
	// backstop (scanTPABundle) enforces the whole tuple.
	BundleSource string `json:"bundle_source,omitempty"`
	// BundleSignatureVerified is a pointer so an explicit false is transmitted
	// while a heartbeat without a bundle snapshot omits the key.
	BundleSignatureVerified *bool `json:"bundle_signature_verified,omitempty"`
	// BundleVersion is "other" or a "<major>.<minor>" schema line.
	BundleVersion string `json:"bundle_version,omitempty"`
	// BundleSequence is present only together with a schema-line BundleVersion.
	BundleSequence *uint64 `json:"bundle_sequence,omitempty"`
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
	if t.BundleRefreshCycles != 0 || t.BundleRefreshActivations != 0 || t.BundleRefreshRejections != 0 ||
		t.BundleRefreshUnavailable != 0 || t.BundleReprotectHits != 0 {
		return false
	}
	if t.BundleSource != "" || t.BundleSignatureVerified != nil || t.BundleVersion != "" || t.BundleSequence != nil {
		return false
	}
	for _, n := range t.Findings {
		if n != 0 {
			return false
		}
	}
	return true
}
