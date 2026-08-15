package main

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// Exit codes for mcpproxy to enable specific error handling by the tray launcher

const (
	// ExitCodeSuccess indicates normal program termination
	ExitCodeSuccess = 0

	// ExitCodeGeneralError indicates a generic error (default)
	ExitCodeGeneralError = 1

	// ExitCodePortConflict indicates the listen port is already in use
	ExitCodePortConflict = 2

	// ExitCodeDBLocked indicates the database is locked by another process
	ExitCodeDBLocked = 3

	// ExitCodeConfigError indicates configuration validation failed
	ExitCodeConfigError = 4

	// ExitCodePermissionError indicates insufficient permissions (file access, port binding)
	ExitCodePermissionError = 5

	// Spec 098 preflight verdict codes. They are a SEPARATE band from the codes
	// above on purpose: 0-5 describe whether mcpproxy could run, 10-12 describe
	// what a preflight found, so a cron wrapper can branch retry-vs-page-vs-fix
	// on the exit code alone without parsing JSON (SC-003). Their values are the
	// spec's, and preflight.ExitCode is the single mapping — these constants
	// exist so the CLI can name them, not to re-derive them.

	// ExitCodePreflightDegradedRetryable: every failure is retryable (the proxy
	// is mid-transition). The job should back off and retry.
	ExitCodePreflightDegradedRetryable = preflight.ExitDegradedRetryable

	// ExitCodePreflightBlocked: at least one tool needs an operator action
	// (approve, enable, log in, re-pin). Retrying will not help.
	ExitCodePreflightBlocked = preflight.ExitBlocked

	// ExitCodePreflightUnknownIDs: at least one requested id does not exist in
	// the caller's view — usually a typo or a removed server.
	ExitCodePreflightUnknownIDs = preflight.ExitUnknownIDs
)

// preflightVerdictError carries a non-ready preflight verdict out of the
// subcommand so the CENTRAL classifier assigns the exit code.
//
// The subcommand deliberately cannot call os.Exit with a code of its own: every
// exit code mcpproxy returns is decided in one place (classifyError), which is
// what keeps 10/11/12 from drifting into meaning something else in a second
// command later.
type preflightVerdictError struct {
	verdict string
	summary string
}

func (e *preflightVerdictError) Error() string {
	if e.summary == "" {
		return fmt.Sprintf("preflight verdict: %s", e.verdict)
	}
	return fmt.Sprintf("preflight verdict: %s (%s)", e.verdict, e.summary)
}

// ExitCode is the spec's worst-class-wins mapping, delegated to the evaluator
// package so the table lives once.
func (e *preflightVerdictError) ExitCode() int {
	return preflight.ExitCode(e.verdict)
}

// newPreflightVerdictError returns nil for a ready verdict — a successful
// preflight is not an error — and a typed error otherwise.
func newPreflightVerdictError(verdict, summary string) error {
	if verdict == preflight.VerdictReady || verdict == "" {
		return nil
	}
	return &preflightVerdictError{verdict: verdict, summary: summary}
}

// preflightGeneralError marks a preflight failure that is NOT a verdict — bad
// arguments, an unreachable daemon, a rendering failure.
//
// FR-009 gives those exit code 1, and the type is what guarantees it: without
// it they fall through to classifyError's string heuristics, where the daemon's
// own "failed to load configuration" or a remediation string containing
// "permission denied" would come back as exit 4 or 5. A cron wrapper reads
// those as mcpproxy-level failures, and 4/5 are also inside the band a
// preflight wrapper watches, so the misclassification is silent.
type preflightGeneralError struct {
	err error
}

func (e *preflightGeneralError) Error() string { return e.err.Error() }

func (e *preflightGeneralError) Unwrap() error { return e.err }

// ExitCode is always the general error code, whatever the message says.
func (e *preflightGeneralError) ExitCode() int { return ExitCodeGeneralError }

// newPreflightGeneralError wraps a non-verdict failure, passing nil through and
// leaving an already-typed verdict error alone (the verdict IS the answer).
func newPreflightGeneralError(err error) error {
	if err == nil {
		return nil
	}
	var verdictErr *preflightVerdictError
	if errors.As(err, &verdictErr) {
		return err
	}
	return &preflightGeneralError{err: err}
}
