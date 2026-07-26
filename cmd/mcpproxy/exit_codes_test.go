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
