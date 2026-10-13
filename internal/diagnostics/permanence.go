package diagnostics

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
)

// IsPermanent reports whether a failure carrying this code is deterministic and
// unrecoverable — the same attempt will fail identically until a human changes
// the configuration, the image, or the machine.
//
// It is the single permanence signal for the retry policy in
// internal/upstream/types (GH #1145). Declaring a NEW code permanent is a
// one-line `Retry: RetryPermanent,` in that code's existing register(...) call —
// there is deliberately no second list to keep in sync and no if-branch to add.
//
// Unregistered and unknown codes return false. That is the safe default and the
// reason MCPX_UNKNOWN_UNCLASSIFIED (which carries the zero RetryClass) stays
// retryable: failing to recognise an error is not evidence that retrying it is
// futile.
func IsPermanent(c Code) bool {
	e, ok := registry[c]
	return ok && e.Retry == RetryPermanent
}

// PermanentFailureReason renders the user-facing cause of a permanent park: the
// catalog message written to tell a user how to fix exactly this code. It falls
// back to the raw error, and then to a generic sentence, so a parked server is
// never surfaced without a reason (GH #1145).
func PermanentFailureReason(c Code, rawError string) string {
	if entry, ok := registry[c]; ok && entry.UserMessage != "" {
		return entry.UserMessage
	}
	if rawError != "" {
		return rawError
	}
	return "This server cannot start with its current configuration."
}

// ParkableCode classifies a connection failure and reports whether that
// classification is strong enough to STOP retrying it.
//
// The returned Code is exactly Classify's, so the message the user reads is
// unchanged. The boolean answers the narrower question the retry policy asks:
// may this be treated as PROVEN permanent and the server parked?
//
// Both conditions must hold:
//
//  1. the code is declared RetryPermanent in the catalog, and
//  2. the same code is reachable from a typed/structured signal ALONE — a code
//     a producer attached with WrapError, or a real *exec.Error carrying an
//     errno from mcpproxy's own spawn syscall.
//
// Condition (2) is the whole point. Classify's stdio and docker fallbacks are
// deliberately broad substring matches, and the string they match is not ours:
// core.enrichTransportClosedError folds the CHILD PROCESS'S captured stderr
// into the error ("... recent stderr:\n<child output>"). Without (2) a server
// that crashed transiently having printed "no such file or directory",
// "permission denied" or "command not found" is classified with a permanent
// code and parked forever on the strength of text it wrote about itself. Broad
// matching is right for a help message and wrong for a decision that stops
// automatic recovery, so the two are separated here instead of by narrowing the
// classifier and degrading every message with it.
//
// The coverage this costs is real and deliberate. A non-Docker stdio command is
// spawned through a login shell (core.wrapWithUserShell) precisely so it
// inherits the user's PATH, which means a genuinely missing binary reaches us
// as shell text, never as an *exec.Error, and is no longer parked — it retries
// on the normal ladder as it did before GH #1145. Restoring that coverage is a
// job for the spawn path (emitting a first-party coded error it can stand
// behind), not for the classifier trusting a child's stderr.
func ParkableCode(err error, hints ClassifierHints) (Code, bool) {
	if err == nil {
		return "", false
	}
	code := Classify(err, hints)
	if !IsPermanent(code) {
		return code, false
	}
	return code, classifyTyped(err, hints) == code
}

// classifyTyped re-derives a code from typed signals only, ignoring every
// free-text fallback. It deliberately mirrors the typed arms of Classify /
// classifyStdio in the same order, so a code it returns is one Classify would
// also have returned — ParkableCode compares the two and parks only on
// agreement.
//
// Returns "" when nothing structured is available, which is the common case and
// the safe one.
func classifyTyped(err error, hints ClassifierHints) Code {
	// A producer opted into explicit attribution (WrapError). This is mcpproxy
	// naming its own failure, which is as structured as it gets.
	var coded interface{ Code() Code }
	if errors.As(err, &coded) {
		if c := coded.Code(); c != "" {
			return c
		}
	}

	// A real errno from the exec we performed. Mirrors classifyStdio: the
	// docker-isolated arm resolves the docker BINARY itself first, then the
	// generic spawn errnos.
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		return ""
	}
	if hints.DockerIsolated && errors.Is(execErr.Err, syscall.ENOENT) &&
		strings.Contains(strings.ToLower(execErr.Name), "docker") {
		return DockerCLINotFound
	}
	switch {
	case errors.Is(execErr.Err, syscall.ENOENT):
		return STDIOSpawnENOENT
	case errors.Is(execErr.Err, syscall.EACCES):
		return STDIOSpawnEACCES
	case errors.Is(execErr.Err, syscall.ENOEXEC):
		return STDIOSpawnExecFormat
	}
	return ""
}
