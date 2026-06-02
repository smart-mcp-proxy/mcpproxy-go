package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// defaultScannerTimeout is the conservative ceiling used when a plugin declares
// no timeout (or an unparseable one) so a misconfigured value never hangs a run.
const defaultScannerTimeout = 120 * time.Second

// scannerExec is the narrow slice of *scanner.DockerRunner the production runner
// needs. Defining it here lets unit tests inject a deterministic stub while
// production wires the real Docker-backed implementation (dependency inversion,
// constitution: testability + 3-layer upstream client).
type scannerExec interface {
	RunScanner(ctx context.Context, cfg scanner.ScannerRunConfig) (stdout, stderr string, exitCode int, err error)
	ReadReportFile(reportDir string) ([]byte, error)
}

// newDockerScannerRunner builds the production scannerRunner: per entry it
// materializes the corpus text as tools.json in a freshly mounted source dir,
// runs the scanner container offline (NetworkMode=none — selectScanners is the
// network gate, the runner is offline-by-default, Security-by-Default), and
// parses the SARIF report into findings tagged with the scanner id. A docker
// exec failure or an unreadable report is surfaced as an error so the caller
// records a non-flagging verdict plus a warning (an unavailable scanner must
// never manufacture a finding). Scanners signal hits via a non-zero exit code,
// so a non-zero exit accompanied by a parseable report is NOT a failure.
func newDockerScannerRunner(exec scannerExec, baseDir string, lookupEnv func(string) (string, bool)) scannerRunner {
	return func(ctx context.Context, p *scanner.ScannerPlugin, e corpusEntry) ([]scanner.ScanFinding, error) {
		workDir, err := os.MkdirTemp(baseDir, fmt.Sprintf("scan-%s-", p.ID))
		if err != nil {
			return nil, fmt.Errorf("scanner %s: create work dir: %w", p.ID, err)
		}
		defer os.RemoveAll(workDir)

		sourceDir := filepath.Join(workDir, "source")
		reportDir := filepath.Join(workDir, "report")
		for _, d := range []string{sourceDir, reportDir} {
			if mkErr := os.MkdirAll(d, 0o750); mkErr != nil {
				return nil, fmt.Errorf("scanner %s: prepare %s: %w", p.ID, d, mkErr)
			}
		}
		if wErr := writeToolsJSON(sourceDir, e); wErr != nil {
			return nil, fmt.Errorf("scanner %s: write tools.json: %w", p.ID, wErr)
		}

		cfg := scanner.ScannerRunConfig{
			ContainerName: scanner.GenerateContainerName(p.ID, e.ID),
			Image:         p.EffectiveImage(),
			Command:       p.Command,
			Env:           collectScannerEnv(p, lookupEnv),
			SourceDir:     sourceDir,
			ReportDir:     reportDir,
			NetworkMode:   "none",
			Timeout:       parseTimeout(p.Timeout),
		}

		_, stderrOut, exitCode, runErr := exec.RunScanner(ctx, cfg)
		if runErr != nil {
			return nil, fmt.Errorf("scanner %s exec failed: %w", p.ID, runErr)
		}

		data, reportErr := exec.ReadReportFile(reportDir)
		if reportErr != nil {
			return nil, fmt.Errorf("scanner %s (exit %d): no readable report: %w; stderr: %s",
				p.ID, exitCode, reportErr, stderrOut)
		}
		return findingsFromReport(p.ID, data), nil
	}
}

// collectScannerEnv assembles the container environment from declared keys only:
// configured defaults first, then any present required/optional lookups (an
// absent key is omitted, never blank). Ambient process secrets never leak into
// the scanner subprocess (Security-by-Default).
func collectScannerEnv(p *scanner.ScannerPlugin, lookupEnv func(string) (string, bool)) map[string]string {
	env := make(map[string]string, len(p.ConfiguredEnv))
	for k, v := range p.ConfiguredEnv {
		env[k] = v
	}
	for _, req := range p.RequiredEnv {
		if v, ok := lookupEnv(req.Key); ok {
			env[req.Key] = v
		}
	}
	for _, opt := range p.OptionalEnv {
		if v, ok := lookupEnv(opt.Key); ok {
			env[opt.Key] = v
		}
	}
	return env
}

// parseTimeout parses a plugin's declared timeout, falling back to the
// conservative default for empty, unparseable, or non-positive values so a
// misconfigured timeout never hangs (or instantly kills) a run.
func parseTimeout(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return defaultScannerTimeout
	}
	return d
}
