package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// scannerRunner executes one scanner against one corpus entry and returns its
// normalized findings. It is injected so unit tests can supply a deterministic
// mock while production wires a Docker-backed implementation (--scanners).
type scannerRunner func(ctx context.Context, p *scanner.ScannerPlugin, e corpusEntry) ([]scanner.ScanFinding, error)

// selectScanners resolves a comma-separated list of scanner ids against the
// registry and partitions them into runnable vs skipped under the current
// constraints. An unknown id is a hard config error (never a silent skip);
// the caller maps it to exit 4. Both slices are sorted by id for deterministic
// output (INV-5). Selection is pure — warnings are emitted by the caller via
// runnabilityReason so this stays trivially testable.
func selectScanners(reg *scanner.Registry, csv string, dockerEnabled bool, lookupEnv func(string) (string, bool)) (run, skipped []*scanner.ScannerPlugin, err error) {
	seen := make(map[string]bool)
	selected := make([]*scanner.ScannerPlugin, 0)
	for _, raw := range strings.Split(csv, ",") {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		p, gerr := reg.Get(id)
		if gerr != nil {
			return nil, nil, fmt.Errorf("unknown scanner %q: %w", id, gerr)
		}
		selected = append(selected, p)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })

	for _, p := range selected {
		if runnabilityReason(p, dockerEnabled, lookupEnv) != "" {
			skipped = append(skipped, p)
		} else {
			run = append(run, p)
		}
	}
	return run, skipped, nil
}

// runnabilityReason returns "" when the scanner can run under the given
// constraints, otherwise a human-readable reason it must be skipped. Used both
// to partition in selectScanners and to explain the skip to the operator, so
// the gating rules live in exactly one place (DRY). Order matters: Docker is
// the cheapest gate, then secrets, then network.
func runnabilityReason(p *scanner.ScannerPlugin, dockerEnabled bool, lookupEnv func(string) (string, bool)) string {
	if !dockerEnabled {
		return "Docker isolation disabled (set MCPPROXY_SCAN_EVAL_DOCKER=1 to enable)"
	}
	for _, req := range p.RequiredEnv {
		if _, ok := lookupEnv(req.Key); !ok {
			return fmt.Sprintf("missing required secret %s", req.Key)
		}
	}
	if p.NetworkReq {
		return "requires network access (unavailable in offline corpus evaluation)"
	}
	return ""
}

// severityRank orders severities for max-severity computation. info, the empty
// string, and unknown values all rank 0 so they neither flag nor set
// max_severity — the schema enum is {critical,high,medium,low} only, and info
// findings are kept solely as provenance in detections.
func severityRank(s string) int {
	switch s {
	case scanner.SeverityCritical:
		return 4
	case scanner.SeverityHigh:
		return 3
	case scanner.SeverityMedium:
		return 2
	case scanner.SeverityLow:
		return 1
	default:
		return 0
	}
}

// scanFindingsToVerdict projects a scanner's findings into one detectorVerdict.
// Every finding (including info) is recorded in detections for provenance, but
// only {critical,high,medium,low} contribute to flagged/max_severity, so the
// flagged ⇔ max_severity!="" invariant holds. Detections is always non-nil.
func scanFindingsToVerdict(id string, findings []scanner.ScanFinding) detectorVerdict {
	v := detectorVerdict{
		Detector:   id,
		Detections: make([]detectionView, 0, len(findings)),
	}
	for _, f := range findings {
		v.Detections = append(v.Detections, detectionView{
			Type:     f.RuleID,
			Category: f.Category,
			Severity: f.Severity,
		})
		if severityRank(f.Severity) > severityRank(v.MaxSeverity) {
			v.MaxSeverity = f.Severity
			v.Flagged = true
		}
	}
	return v
}

// appendScannerVerdicts augments an existing detector report in place: each
// plugin id is appended to report.Detectors and every entry gains one verdict
// per plugin. A per-entry runner error is a safe non-flag (an unavailable
// scanner must never manufacture a finding) plus a one-line stderr warning.
// Entries are matched to corpus entries by id rather than slice position so a
// reordered or partial report stays correct.
func appendScannerVerdicts(report *verdictReport, c *corpus, plugins []*scanner.ScannerPlugin, runner scannerRunner, stderr io.Writer) {
	if len(plugins) == 0 {
		return
	}
	byID := make(map[string]corpusEntry, len(c.Entries))
	for _, e := range c.Entries {
		byID[e.ID] = e
	}
	for _, p := range plugins {
		report.Detectors = append(report.Detectors, p.ID)
	}
	for i := range report.Entries {
		entry := &report.Entries[i]
		ce, ok := byID[entry.ID]
		for _, p := range plugins {
			if !ok {
				entry.Verdicts = append(entry.Verdicts, scanFindingsToVerdict(p.ID, nil))
				continue
			}
			findings, rerr := runner(context.Background(), p, ce)
			if rerr != nil {
				fmt.Fprintf(stderr, "warning: scanner %s failed on entry %s: %v\n", p.ID, entry.ID, rerr)
				entry.Verdicts = append(entry.Verdicts, scanFindingsToVerdict(p.ID, nil))
				continue
			}
			entry.Verdicts = append(entry.Verdicts, scanFindingsToVerdict(p.ID, findings))
		}
	}
}

// applyScanners resolves the requested scanner ids against the registry, warns
// about any skipped under the current constraints, and — for the runnable set —
// appends their verdicts to the report in place. An unknown id is a hard config
// error the caller maps to exit 4 (never a silent skip); a skip is a warning,
// never an error, so the detector verdicts still emit. runner is injected for
// tests; when nil a Docker-backed runner is constructed (offline-by-default).
// The scratch base dir is created lazily so the docker-disabled path touches no
// filesystem and emits clean JSON.
func applyScanners(report *verdictReport, c *corpus, reg *scanner.Registry, scannerIDs string, dockerEnabled bool, lookupEnv func(string) (string, bool), runner scannerRunner, stderr io.Writer) error {
	run, skipped, err := selectScanners(reg, scannerIDs, dockerEnabled, lookupEnv)
	if err != nil {
		return err
	}
	for _, p := range skipped {
		fmt.Fprintf(stderr, "warning: skipping scanner %s: %s\n", p.ID, runnabilityReason(p, dockerEnabled, lookupEnv))
	}
	if len(run) == 0 {
		return nil
	}
	if runner == nil {
		baseDir, mkErr := os.MkdirTemp("", "scan-eval-")
		if mkErr != nil {
			return fmt.Errorf("scanner work dir: %w", mkErr)
		}
		defer os.RemoveAll(baseDir)
		runner = newDockerScannerRunner(scanner.NewDockerRunner(zap.NewNop()), baseDir, lookupEnv)
	}
	appendScannerVerdicts(report, c, run, runner, stderr)
	return nil
}

// findingsFromReport parses a scanner's report bytes into normalized findings,
// tagged with the scanner id. The runner is SARIF-only and safe-by-default: any
// report that is not valid SARIF (empty, non-SARIF JSON, or malformed) yields no
// findings rather than an error, so an unreadable report can never manufacture a
// verdict (security-by-default, constitution).
func findingsFromReport(id string, data []byte) []scanner.ScanFinding {
	if !scanner.IsSARIF(data) {
		return nil
	}
	report, err := scanner.ParseSARIF(data)
	if err != nil {
		return nil
	}
	return scanner.NormalizeFindings(report, id)
}

// writeToolsJSON materializes a corpus entry as a single-tool source tree in the
// {"tools":[{name,description}]} shape the bundled scanners read (mirrors
// scanner.Service.exportToolDefinitions). The entry id becomes the tool name and
// the corpus description becomes the tool description the scanners inspect.
func writeToolsJSON(dir string, e corpusEntry) error {
	doc := map[string]any{
		"tools": []map[string]string{
			{"name": e.ID, "description": e.Description},
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tools.json"), data, 0o600)
}
