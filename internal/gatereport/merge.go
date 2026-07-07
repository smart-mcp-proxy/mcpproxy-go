package gatereport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WriteFragment writes a fragment file into the report directory, creating
// the directory if needed. The file name is derived from the fragment name.
func WriteFragment(dir string, frag *Fragment) error {
	if frag.Name == "" {
		return fmt.Errorf("fragment has no name")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	data, err := json.MarshalIndent(frag, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fragment %s: %w", frag.Name, err)
	}
	path := filepath.Join(dir, FragmentFileName(frag.Name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write fragment %s: %w", frag.Name, err)
	}
	return nil
}

// LoadFragments reads every *.json fragment from the report directory. Files
// that fail to parse are returned as synthetic failed fragments named after
// the file so a corrupted fragment can never silently vanish from the report.
func LoadFragments(dir string) ([]Fragment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // treated as "everything missing" by Merge
		}
		return nil, fmt.Errorf("read report dir: %w", err)
	}
	var frags []Fragment
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// The merged report itself may live in the same directory.
		if e.Name() == "gate-report.json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read fragment %s: %w", e.Name(), err)
		}
		var f Fragment
		if err := json.Unmarshal(data, &f); err != nil || f.Name == "" {
			frags = append(frags, Fragment{
				Name:   strings.TrimSuffix(e.Name(), ".json"),
				Status: StatusFail,
				Reason: fmt.Sprintf("unparseable report fragment %s: %v", e.Name(), err),
			})
			continue
		}
		frags = append(frags, f)
	}
	return frags, nil
}

// Merge combines fragments against the hardcoded manifest and computes the
// verdict. Rules:
//
//   - a manifest entry with no fragment: reserved slots become
//     not-run/"not-implemented-yet" (non-blocking); everything else becomes
//     fail/"missing report fragment" (FR-004 — a missing fragment is a fail).
//   - duplicate fragments for one name: fail (ambiguous evidence).
//   - a blocking entry passes the gate only with status pass or flaky
//     (FR-010); fail/skipped/not-run/advisory-fail on a blocking entry all
//     block (no silent skips).
//   - non-blocking entries never block, but their failures are listed in
//     AdvisoryFailures.
//   - unexpected fragments (no manifest entry) are reported; a non-green
//     unexpected fragment blocks (fail-closed).
func Merge(fragments []Fragment) *Report {
	manifest := Manifest()
	byName := make(map[string][]Fragment)
	for _, f := range fragments {
		byName[f.Name] = append(byName[f.Name], f)
	}

	report := &Report{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		Counts:        make(map[Status]int),
		Manifest:      manifest,
	}

	seen := make(map[string]bool)
	for _, m := range manifest {
		seen[m.Name] = true
		entry := Entry{Blocking: m.Blocking, Expected: true}
		switch frags := byName[m.Name]; {
		case len(frags) == 0 && m.Reserved:
			entry.Fragment = Fragment{Name: m.Name, Status: StatusNotRun, Reason: ReasonNotImplemented}
		case len(frags) == 0:
			entry.Fragment = Fragment{Name: m.Name, Status: StatusFail, Reason: ReasonMissingFragment}
		case len(frags) > 1:
			entry.Fragment = Fragment{
				Name:   m.Name,
				Status: StatusFail,
				Reason: fmt.Sprintf("duplicate report fragments (%d) for entry", len(frags)),
			}
		default:
			entry.Fragment = frags[0]
		}
		report.Entries = append(report.Entries, entry)
	}

	// Unexpected fragments: report them all, fail-closed on non-green.
	var extraNames []string
	for name := range byName {
		if !seen[name] {
			extraNames = append(extraNames, name)
		}
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		for _, f := range byName[name] {
			report.Entries = append(report.Entries, Entry{Fragment: f, Blocking: true, Expected: false})
		}
	}

	for i := range report.Entries {
		e := &report.Entries[i]
		report.Counts[e.Status]++
		green := e.Status == StatusPass || e.Status == StatusFlaky
		if green {
			continue
		}
		desc := fmt.Sprintf("%s: %s (%s)", e.Name, e.Status, e.Reason)
		if e.Blocking {
			report.BlockingFailures = append(report.BlockingFailures, desc)
		} else if e.Status != StatusNotRun && e.Status != StatusSkipped {
			report.AdvisoryFailures = append(report.AdvisoryFailures, desc)
		}
	}

	if len(report.BlockingFailures) == 0 {
		report.Verdict = VerdictPass
	} else {
		report.Verdict = VerdictFail
	}
	return report
}

// WriteReport writes the merged report as JSON to the given path.
func WriteReport(path string, r *Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gate report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write gate report: %w", err)
	}
	return nil
}
