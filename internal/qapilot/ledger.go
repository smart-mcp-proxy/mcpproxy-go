package qapilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ReportDirEnv names the directory the ledger appends check records to. When
// it is unset (local `go test`), nothing is written and the tests assert on
// their own. CI sets it and runs `qapilot report` over the directory.
const ReportDirEnv = "QAPILOT_REPORT_DIR"

// Check is one executed check as the report consumes it.
type Check struct {
	ID           string    `json:"id"`           // scenario or check id (S01, classify/..., authz/table)
	Requirements []string  `json:"requirements"` // QP-nn ids the check evidences
	Test         string    `json:"test"`         // Go test name
	Package      string    `json:"package"`
	Status       string    `json:"status"` // passed | failed | skipped
	Detail       string    `json:"detail,omitempty"`
	FinishedAt   time.Time `json:"finished_at"`
}

var ledgerMu sync.Mutex

// Track registers a check for the running test. When the test finishes the
// ledger records passed, failed or skipped from the test's own outcome, so a
// check cannot be reported as passed by a test that never reached its
// assertions, and a skip is recorded as a skip, never as a pass.
func Track(t *testing.T, id string, requirements ...string) {
	t.Helper()
	for _, r := range requirements {
		if _, ok := RequirementByID(r); !ok {
			t.Fatalf("qapilot: unknown requirement id %q in check %q", r, id)
		}
	}
	pkg := packageName(t)
	t.Cleanup(func() {
		status := "passed"
		switch {
		case t.Failed():
			status = "failed"
		case t.Skipped():
			status = "skipped"
		}
		record(Check{ID: id, Requirements: requirements, Test: t.Name(), Package: pkg, Status: status, FinishedAt: time.Now().UTC()})
	})
}

// record appends one check line to the package's JSONL file.
func record(c Check) {
	dir := os.Getenv(ReportDirEnv)
	if dir == "" {
		return
	}
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "qapilot ledger: %v\n", err)
		return
	}
	name := fmt.Sprintf("checks-%s-%d.jsonl", sanitize(c.Package), os.Getpid())
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qapilot ledger: %v\n", err)
		return
	}
	defer f.Close()
	line, _ := json.Marshal(c)
	_, _ = f.Write(append(line, '\n'))
}

func packageName(t *testing.T) string {
	// The test binary's working directory is the package directory.
	wd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(wd)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// ReadChecks loads every JSONL file in dir.
func ReadChecks(dir string) ([]Check, error) {
	files, err := filepath.Glob(filepath.Join(dir, "checks-*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Check
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var c Check
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			out = append(out, c)
		}
	}
	return out, nil
}
