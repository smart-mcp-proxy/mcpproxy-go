// Command scan-eval bridges the Spec 065 / D2 security corpus to mcpproxy's
// production sensitive-data detector and emits per-entry, per-detector verdict
// JSON for the Python SecurityScorer (B3). It is offline, deterministic test
// tooling — it adds no runtime or REST surface (Security-by-Default).
//
// Usage:
//
//	scan-eval --corpus datasets/security_corpus_v1.json [--out verdicts.json]
//
// The optional --scanners flag opts into Docker-isolated bundled security
// scanners (offline by default; set MCPPROXY_SCAN_EVAL_DOCKER=1 to enable
// container execution). Each requested scanner appends a per-entry verdict.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

const (
	exitOK          = 0 // success
	exitWriteError  = 1 // marshaling or output write failure
	exitConfigError = 4 // bad/missing corpus or flags (repo convention)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. It returns the process exit code and writes
// the verdict report to stdout (or --out) and diagnostics to stderr.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpusPath := fs.String("corpus", "", "path to the D2 security corpus JSON (required)")
	outPath := fs.String("out", "", "output path for verdict JSON (default: stdout)")
	detectors := fs.String("detectors", detectorSensitiveData, "comma-separated detectors to run (only 'sensitive-data' is supported)")
	scanners := fs.String("scanners", "", "comma-separated Docker bundled scanner ids to run (offline; set MCPPROXY_SCAN_EVAL_DOCKER=1 to enable)")
	gate := fs.Bool("gate", false, "run the Spec-076 detect.Engine over the corpus, print per-category recall/FP metrics, and exit non-zero on a recall/FP breach (CI regression gate)")
	minRecall := fs.Float64("min-recall", 0.90, "gate: minimum malicious recall over gated categories (FR-013)")
	maxFP := fs.Float64("max-fp", 0.05, "gate: maximum false-positive rate over benign + hard-negative samples (FR-013)")

	if err := fs.Parse(args); err != nil {
		return exitConfigError
	}
	if *corpusPath == "" {
		fmt.Fprintln(stderr, "error: --corpus is required")
		return exitConfigError
	}

	// Gate mode is a distinct pipeline: it runs the offline detect.Engine over
	// the labeled detect corpus and enforces recall/FP thresholds. It does not
	// emit the per-detector verdict report (that is the default --detectors path).
	if *gate {
		gc, err := loadGateCorpus(*corpusPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return exitConfigError
		}
		return runGate(gc, *minRecall, *maxFP, stdout, stderr)
	}

	if err := validateDetectors(*detectors); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitConfigError
	}
	c, err := loadCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitConfigError
	}

	report := evaluate(c, security.NewDetector(nil))

	// Optional Docker bundled scanners. An unknown id is a hard config error
	// (exit 4); a skip under current constraints is only a warning so detector
	// verdicts still emit. Docker is the gate — offline-by-default unless the
	// operator opts in via MCPPROXY_SCAN_EVAL_DOCKER.
	if *scanners != "" {
		dockerEnv := os.Getenv("MCPPROXY_SCAN_EVAL_DOCKER")
		dockerEnabled := dockerEnv == "1" || strings.EqualFold(dockerEnv, "true")
		reg := scanner.NewRegistry("", zap.NewNop())
		if err := applyScanners(report, c, reg, *scanners, dockerEnabled, os.LookupEnv, nil, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return exitConfigError
		}
	}

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "error: marshaling verdict report: %v\n", err)
		return exitWriteError
	}
	out = append(out, '\n')

	if *outPath == "" {
		if _, err := stdout.Write(out); err != nil {
			fmt.Fprintf(stderr, "error: writing to stdout: %v\n", err)
			return exitWriteError
		}
		return exitOK
	}
	if err := os.WriteFile(*outPath, out, 0o644); err != nil {
		fmt.Fprintf(stderr, "error: writing %q: %v\n", *outPath, err)
		return exitWriteError
	}
	return exitOK
}

// validateDetectors rejects any detector id other than the one bridged in this
// PR. Additional detectors are added here as their bridges land.
func validateDetectors(csv string) error {
	for _, d := range strings.Split(csv, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if d != detectorSensitiveData {
			return fmt.Errorf("unsupported detector %q (only %q is supported)", d, detectorSensitiveData)
		}
	}
	return nil
}
