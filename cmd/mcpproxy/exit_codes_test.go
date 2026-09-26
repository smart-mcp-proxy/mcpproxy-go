package main

import (
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
)

// TestClassifyError_WrappedPortInUse pins the runServer → classifyError →
// exit-code-2 contract: a *server.PortInUseError wrapped with %w must still
// classify as a port conflict so the tray's state machine sees exit code 2.
func TestClassifyError_WrappedPortInUse(t *testing.T) {
	err := fmt.Errorf("server failed: %w", &server.PortInUseError{Address: "127.0.0.1:8080"})
	if got := classifyError(err); got != ExitCodePortConflict {
		t.Fatalf("classifyError(%v) = %d, want %d (ExitCodePortConflict)", err, got, ExitCodePortConflict)
	}
}

func TestClassifyError_Nil(t *testing.T) {
	if got := classifyError(nil); got != ExitCodeSuccess {
		t.Fatalf("classifyError(nil) = %d, want %d (ExitCodeSuccess)", got, ExitCodeSuccess)
	}
}

// TestClassifyError_InvalidStatusFlag_NotMisclassifiedAsConfigError pins a
// review finding: validateStatusFlag's error message enumerates the full
// status vocabulary as part of "must be one of: ..., needs_config, ...", and
// classifyError's string heuristics classify ANY error whose text contains
// both "invalid" and "config" as ExitCodeConfigError (4) — a coincidence of
// "needs_config" being a real, legitimately-listed status value, not an
// actual config-file problem. --status must exit the same way the equivalent
// --trust-mode validation error does (ExitCodeGeneralError, 1).
func TestClassifyError_InvalidStatusFlag_NotMisclassifiedAsConfigError(t *testing.T) {
	err := validateStatusFlag([]string{"bogus-status"})
	if err == nil {
		t.Fatal("expected validateStatusFlag to reject an unknown status")
	}
	if got := classifyError(err); got != ExitCodeGeneralError {
		t.Fatalf("classifyError(%v) = %d, want %d (ExitCodeGeneralError, matching --trust-mode)", err, got, ExitCodeGeneralError)
	}
}

// TestClassifyError_InvalidTrustModeFlag_IsGeneralError pins the existing,
// already-correct behavior this PR keeps consistent with --status above.
func TestClassifyError_InvalidTrustModeFlag_IsGeneralError(t *testing.T) {
	err := validateTrustModeFlag("bogus-mode")
	if err == nil {
		t.Fatal("expected validateTrustModeFlag to reject an unknown mode")
	}
	if got := classifyError(err); got != ExitCodeGeneralError {
		t.Fatalf("classifyError(%v) = %d, want %d (ExitCodeGeneralError)", err, got, ExitCodeGeneralError)
	}
}
