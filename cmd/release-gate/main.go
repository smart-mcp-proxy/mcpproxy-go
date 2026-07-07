// Command release-gate is the Spec 081 release-qualification gate driver.
//
// Subcommands:
//
//	matrix      boot the candidate mcpproxy against five local fixture
//	            upstreams (stdio/http/sse/docker/oauth) and run the FR-007
//	            ready/list/call/reconnect checks per cell (US1).
//	invariants  attach to the live instance the matrix left running and
//	            assert FR-011 (activity request-id completeness), FR-012
//	            (counter movement), FR-013 (quarantine + approval e2e) and
//	            FR-014 (upgrade-in-place from the latest stable release).
//	run-suite   run an existing suite entry point (FR-003) and record its
//	            outcome as a report fragment.
//	report      merge all fragments against the hardcoded manifest into
//	            gate-report.json + a markdown summary; exits non-zero unless
//	            every blocking entry is pass or flaky.
//
// Every check writes a JSON fragment (internal/gatereport schema) into a
// shared --report-dir; a missing fragment for a blocking manifest entry is a
// FAIL (FR-004 — no silent skips).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var err error
	var ok bool
	switch os.Args[1] {
	case "matrix":
		ok, err = cmdMatrix(ctx, os.Args[2:])
	case "invariants":
		ok, err = cmdInvariants(ctx, os.Args[2:])
	case "run-suite":
		ok, err = cmdRunSuite(ctx, os.Args[2:])
	case "report":
		ok, err = cmdReport(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-gate %s: %v\n", os.Args[1], err)
		os.Exit(2)
	}
	if !ok {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `release-gate — Spec 081 release qualification gate driver

usage:
  release-gate matrix     --binary ./mcpproxy --fixture ./mcpfixture --oauth-server ./oauthserver \
                          --report-dir DIR [--state-file FILE] [--cells stdio,http,sse,docker,oauth] \
                          [--work-dir DIR] [--cell-timeout 300s]
  release-gate invariants --state-file FILE --report-dir DIR [--prev-binary PATH] [--skip-upgrade] \
                          [--upgrade-repo owner/repo] [--keep-core]
  release-gate run-suite  --name suite/api-e2e --report-dir DIR -- CMD [ARGS...]
  release-gate report     --report-dir DIR [--out gate-report.json] [--summary summary.md]
`)
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[release-gate %s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func cmdMatrix(ctx context.Context, args []string) (bool, error) {
	fs := flag.NewFlagSet("matrix", flag.ExitOnError)
	binary := fs.String("binary", "", "path to the candidate mcpproxy binary (required)")
	fixture := fs.String("fixture", "", "path to the mcpfixture binary (required)")
	oauthBin := fs.String("oauth-server", "", "path to the tests/oauthserver standalone binary (required for the oauth cell)")
	reportDir := fs.String("report-dir", "", "directory for report fragments (required)")
	stateFile := fs.String("state-file", "", "write run state here and leave the core running for `invariants`")
	workDir := fs.String("work-dir", "", "scratch directory base (default: system temp)")
	cells := fs.String("cells", strings.Join(allCells, ","), "comma-separated matrix cells to run")
	cellTimeout := fs.Duration("cell-timeout", 300*time.Second, "per-attempt timeout for one matrix cell")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *binary == "" || *fixture == "" || *reportDir == "" {
		return false, fmt.Errorf("--binary, --fixture and --report-dir are required")
	}
	var cellList []string
	for _, c := range strings.Split(*cells, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !containsStr(allCells, c) {
			return false, fmt.Errorf("unknown cell %q (valid: %s)", c, strings.Join(allCells, ","))
		}
		cellList = append(cellList, c)
	}
	return runMatrix(ctx, matrixOpts{
		Binary:      mustAbs(*binary),
		Fixture:     mustAbs(*fixture),
		OAuthBinary: absOrEmpty(*oauthBin),
		ReportDir:   *reportDir,
		StateFile:   *stateFile,
		WorkDir:     *workDir,
		Cells:       cellList,
		CellTimeout: *cellTimeout,
	})
}

func cmdRunSuite(ctx context.Context, args []string) (bool, error) {
	fs := flag.NewFlagSet("run-suite", flag.ExitOnError)
	name := fs.String("name", "", "manifest entry name (e.g. suite/api-e2e)")
	reportDir := fs.String("report-dir", "", "directory for report fragments (required)")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *name == "" || *reportDir == "" {
		return false, fmt.Errorf("--name and --report-dir are required")
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		return false, fmt.Errorf("no command given after flags (use: run-suite --name N --report-dir D -- CMD ARGS)")
	}
	return runSuite(ctx, *name, *reportDir, cmdArgs)
}

func mustAbs(p string) string {
	if abs, err := absPath(p); err == nil {
		return abs
	}
	return p
}

func absOrEmpty(p string) string {
	if p == "" {
		return ""
	}
	return mustAbs(p)
}
