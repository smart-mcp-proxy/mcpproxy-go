// Command scan-eval bridges the Spec 065 / D2 security corpus to mcpproxy's
// production sensitive-data detector and emits per-entry, per-detector verdict
// JSON for the Python SecurityScorer (B3). It is offline, deterministic test
// tooling — it adds no runtime or REST surface (Security-by-Default).
//
// Usage:
//
//	scan-eval --corpus datasets/security_corpus_v1.json [--out verdicts.json]
//
// The optional --scanners flag is a reserved extension point for the Docker
// bundled scanner registry; it is not yet implemented (deferred per Gate 2).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
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
	scanners := fs.String("scanners", "", "opt-in Docker bundled scanner ids (reserved extension point; not yet implemented)")

	if err := fs.Parse(args); err != nil {
		return exitConfigError
	}
	if *corpusPath == "" {
		fmt.Fprintln(stderr, "error: --corpus is required")
		return exitConfigError
	}
	if err := validateDetectors(*detectors); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitConfigError
	}
	if *scanners != "" {
		fmt.Fprintf(stderr, "warning: --scanners=%q is a reserved extension point and is not yet implemented; ignoring\n", *scanners)
	}

	c, err := loadCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitConfigError
	}

	report := evaluate(c, security.NewDetector(nil))

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
