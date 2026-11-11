package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestLoadErrorExitCode(t *testing.T) {
	wrapped := fmt.Errorf("load: %w", config.NewStartupError(config.ExitCodeOAuthTokenKeyError, "bad key"))
	if got := loadErrorExitCode(wrapped); got != config.ExitCodeOAuthTokenKeyError {
		t.Fatalf("wrapped StartupError: exit %d, want %d", got, config.ExitCodeOAuthTokenKeyError)
	}
	if got := loadErrorExitCode(errors.New("plain")); got != 1 {
		t.Fatalf("plain error: exit %d, want 1", got)
	}
}
