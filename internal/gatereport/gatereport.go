// Package gatereport defines the release-qa-gate report schema (Spec 081
// FR-004/FR-010) and the merger that combines per-check JSON fragments into
// the single gate-report.json verdict artifact plus a human-readable summary.
//
// Every gate check (matrix cell, invariant, assembled suite job) writes one
// Fragment file into a shared report directory. The merger compares the
// fragments against a HARDCODED manifest of expected entries: a missing
// fragment for a blocking entry is a FAIL (no silent skips, FR-004), and
// reserved extension slots (web-ui-sweep, macos-smoke, surface-consistency —
// Spec 081 T2-T4) are recorded as `not-run` with reason
// "not-implemented-yet" until their stages land.
package gatereport

import (
	"strings"
	"time"
)

// Status is the lifecycle status of a gate entry (FR-004).
type Status string

// Fragment statuses. `flaky` means pass-on-retry (FR-010) and counts as
// non-blocking-green; everything else except `pass` blocks when the entry is
// blocking.
const (
	StatusPass         Status = "pass"
	StatusFail         Status = "fail"
	StatusFlaky        Status = "flaky"
	StatusSkipped      Status = "skipped"
	StatusNotRun       Status = "not-run"
	StatusAdvisoryFail Status = "advisory-fail"
)

// Failure classifications (FR-009): distinguish "runner has no Docker"
// (infrastructure — fix the workflow) from "mcpproxy failed to use Docker"
// (product regression).
const (
	ClassificationInfrastructure = "infrastructure"
	ClassificationProduct        = "product"
)

// ReasonNotImplemented is the reason recorded for reserved manifest slots
// whose implementing stage has not landed yet.
const ReasonNotImplemented = "not-implemented-yet"

// ReasonMissingFragment is the reason recorded when an expected fragment is
// absent from the report directory.
const ReasonMissingFragment = "missing report fragment"

// Step is a named sub-assertion inside a check (e.g. matrix steps
// ready/tools/call/reconnect from FR-007).
type Step struct {
	Name       string `json:"name"`
	Status     Status `json:"status"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// Fragment is the unit one gate check writes to the report directory.
type Fragment struct {
	// Name must match a manifest entry name (e.g. "matrix/stdio").
	Name   string `json:"name"`
	Status Status `json:"status"`
	// Reason is required for anything other than pass (FR-004).
	Reason string `json:"reason,omitempty"`
	// Classification is set on failures: infrastructure|product (FR-009).
	Classification string         `json:"classification,omitempty"`
	DurationMS     int64          `json:"duration_ms"`
	Retries        int            `json:"retries"`
	StartedAt      time.Time      `json:"started_at,omitzero"`
	FinishedAt     time.Time      `json:"finished_at,omitzero"`
	Steps          []Step         `json:"steps,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
}

// ManifestEntry declares one expected gate entry.
type ManifestEntry struct {
	Name     string `json:"name"`
	Blocking bool   `json:"blocking"`
	// Reserved marks extension slots (T2-T4): a missing fragment is recorded
	// as not-run/"not-implemented-yet" instead of fail.
	Reserved bool `json:"reserved,omitempty"`
}

// Manifest entry names. Exported so the driver and CI wiring cannot drift
// from the merger's expectations.
const (
	EntryMatrixStdio  = "matrix/stdio"
	EntryMatrixHTTP   = "matrix/http"
	EntryMatrixSSE    = "matrix/sse"
	EntryMatrixDocker = "matrix/docker"
	EntryMatrixOAuth  = "matrix/oauth"

	EntryInvariantActivity   = "invariant/activity-request-id"
	EntryInvariantCounters   = "invariant/counters"
	EntryInvariantQuarantine = "invariant/quarantine-flow"
	EntryInvariantUpgrade    = "invariant/upgrade-in-place"

	EntrySuiteAPIE2E     = "suite/api-e2e"
	EntrySuiteUnitRace   = "suite/unit-race"
	EntrySuiteServerRace = "suite/server-race"
	EntrySuiteScanEval   = "suite/scan-eval"

	EntryReservedWebUISweep         = "reserved/web-ui-sweep"
	EntryReservedMacOSSmoke         = "reserved/macos-smoke"
	EntryReservedSurfaceConsistency = "reserved/surface-consistency"
)

// Manifest returns the hardcoded expected-entries manifest: 5 matrix cells +
// 4 invariants + 4 assembled suite jobs (FR-003) + 3 reserved slots.
func Manifest() []ManifestEntry {
	return []ManifestEntry{
		{Name: EntryMatrixStdio, Blocking: true},
		{Name: EntryMatrixHTTP, Blocking: true},
		{Name: EntryMatrixSSE, Blocking: true},
		{Name: EntryMatrixDocker, Blocking: true},
		{Name: EntryMatrixOAuth, Blocking: true},

		{Name: EntryInvariantActivity, Blocking: true},
		{Name: EntryInvariantCounters, Blocking: true},
		{Name: EntryInvariantQuarantine, Blocking: true},
		{Name: EntryInvariantUpgrade, Blocking: true},

		{Name: EntrySuiteAPIE2E, Blocking: true},
		{Name: EntrySuiteUnitRace, Blocking: true},
		{Name: EntrySuiteServerRace, Blocking: true},
		{Name: EntrySuiteScanEval, Blocking: true},

		{Name: EntryReservedWebUISweep, Blocking: false, Reserved: true},
		{Name: EntryReservedMacOSSmoke, Blocking: false, Reserved: true},
		{Name: EntryReservedSurfaceConsistency, Blocking: false, Reserved: true},
	}
}

// Entry is a manifest entry merged with its (possibly synthesized) fragment.
type Entry struct {
	Fragment
	Blocking bool `json:"blocking"`
	// Expected is false for fragments found in the report dir that no
	// manifest entry declares. They are still reported (no silent anything)
	// and a failing unexpected fragment fails the gate (fail-closed).
	Expected bool `json:"expected"`
}

// Report is the merged machine-readable gate report (gate-report.json).
type Report struct {
	SchemaVersion    int             `json:"schema_version"`
	GeneratedAt      time.Time       `json:"generated_at"`
	Verdict          string          `json:"verdict"` // pass|fail
	BlockingFailures []string        `json:"blocking_failures"`
	AdvisoryFailures []string        `json:"advisory_failures"`
	Counts           map[Status]int  `json:"counts"`
	Entries          []Entry         `json:"entries"`
	Manifest         []ManifestEntry `json:"manifest"`
}

// VerdictPass / VerdictFail are the only report verdicts (FR-001).
const (
	VerdictPass = "pass"
	VerdictFail = "fail"
)

// Passed reports whether the gate verdict allows publication.
func (r *Report) Passed() bool { return r.Verdict == VerdictPass }

// FragmentFileName maps an entry name to its fragment file name in the
// report directory ("matrix/stdio" → "matrix-stdio.json").
func FragmentFileName(name string) string {
	return strings.ReplaceAll(name, "/", "-") + ".json"
}
