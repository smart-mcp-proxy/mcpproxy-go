package detect

import "sort"

// defaultScannerID is the bundled in-process scanner the engine attributes its
// findings to, matching the existing tpa-descriptions analyzer it replaces.
const defaultScannerID = "tpa-descriptions"

// Options configures an Engine.
type Options struct {
	// Checks are run, in this order, against every tool. Order is part of the
	// determinism contract.
	Checks []Check
	// ScannerID is stamped onto every finding's Scanner field. Defaults to
	// "tpa-descriptions" when empty.
	ScannerID string
}

// Engine runs all registered checks over a registry snapshot and aggregates
// per-tool signals into findings. Pure aside from the recover() isolation that
// keeps a misbehaving check from aborting the scan.
type Engine struct {
	checks    []Check
	scannerID string
}

// NewEngine builds an Engine from Options.
func NewEngine(opts Options) *Engine {
	id := opts.ScannerID
	if id == "" {
		id = defaultScannerID
	}
	return &Engine{checks: opts.Checks, scannerID: id}
}

// Coverage records how complete a scan was: a check whose Inspect panicked or
// errored is recovered, counted here, and never aborts the scan — mirroring the
// existing scanners_failed degradation path.
type Coverage struct {
	ChecksRun      int
	ChecksFailed   int
	FailedCheckIDs []string
}

// Result is the output of a scan.
type Result struct {
	Findings []Finding
	Coverage Coverage
}

// Scan inspects every tool in the snapshot. The RegistryView is built once per
// scan (indexes + NormalizedText) if the caller passed an unindexed view, then
// shared with every check. A check that panics is isolated; the scan still
// returns its other findings. Output (findings and ordering) is deterministic
// for identical input.
func (e *Engine) Scan(reg RegistryView) Result {
	if reg.ToolsByName == nil {
		reg = NewRegistryView(reg.Tools)
	}

	failed := make(map[string]struct{})
	findings := make([]Finding, 0, len(reg.Tools))

	for i := range reg.Tools {
		tool := reg.Tools[i]
		var toolSignals []Signal
		for _, c := range e.checks {
			sigs, panicked := safeInspect(c, tool, reg)
			if panicked {
				failed[c.ID()] = struct{}{}
				continue
			}
			toolSignals = append(toolSignals, sigs...)
		}
		if f, ok := aggregate(tool, toolSignals, e.scannerID); ok {
			findings = append(findings, f)
		}
	}

	failedIDs := make([]string, 0, len(failed))
	for id := range failed {
		failedIDs = append(failedIDs, id)
	}
	sort.Strings(failedIDs)

	return Result{
		Findings: findings,
		Coverage: Coverage{
			ChecksRun:      len(e.checks) - len(failedIDs),
			ChecksFailed:   len(failedIDs),
			FailedCheckIDs: failedIDs,
		},
	}
}

// safeInspect runs one check under recover() so a panic is contained. A check
// that panics yields no signals and panicked=true.
func safeInspect(c Check, tool ToolView, reg RegistryView) (sigs []Signal, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			sigs = nil
			panicked = true
		}
	}()
	return c.Inspect(tool, reg), false
}
