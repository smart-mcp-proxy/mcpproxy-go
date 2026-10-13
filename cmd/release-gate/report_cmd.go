package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// cmdReport merges the report-dir fragments against the hardcoded manifest,
// writes gate-report.json + the markdown summary, and returns ok=false
// unless every blocking entry is pass or flaky.
func cmdReport(args []string) (bool, error) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	reportDir := fs.String("report-dir", "", "directory holding report fragments (required)")
	out := fs.String("out", "", "path for gate-report.json (default: <report-dir>/gate-report.json)")
	summary := fs.String("summary", "", "path for the markdown summary (also appended to $GITHUB_STEP_SUMMARY when set)")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *reportDir == "" {
		return false, fmt.Errorf("--report-dir is required")
	}
	if *out == "" {
		*out = filepath.Join(*reportDir, "gate-report.json")
	}
	frags, err := gatereport.LoadFragments(*reportDir)
	if err != nil {
		return false, err
	}
	report := gatereport.Merge(frags)
	if err := gatereport.WriteReport(*out, report); err != nil {
		return false, err
	}
	md := gatereport.Markdown(report)
	if *summary != "" {
		if err := os.WriteFile(*summary, []byte(md), 0o644); err != nil {
			return false, err
		}
	}
	if ghSummary := os.Getenv("GITHUB_STEP_SUMMARY"); ghSummary != "" {
		f, err := os.OpenFile(ghSummary, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = f.WriteString(md)
			f.Close()
		}
	}
	fmt.Println(md)
	logf("report: verdict=%s (%s)", report.Verdict, *out)
	return report.Passed(), nil
}
