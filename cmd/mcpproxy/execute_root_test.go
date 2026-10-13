package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

func newExecuteRootFixture(runE func() error) *cobra.Command {
	root := &cobra.Command{Use: "mcpproxy"}
	root.AddCommand(&cobra.Command{
		Use:  "connect",
		RunE: func(*cobra.Command, []string) error { return runE() },
	})
	return root
}

// F-09: a RunE error was printed by cobra AND by main(), so the whole block
// (including the "Fixes:" list) appeared twice.
func TestExecuteRoot_PrintsErrorOnce(t *testing.T) {
	guard := &runtime.BindingGuardError{
		Bindings: []runtime.BindingRef{{ClientID: "cursor", Profile: "work-readonly", Mode: "locked"}},
		Fixes: []runtime.GuardFix{
			{Kind: "require_mcp_auth"},
			{Kind: "set_anonymous_profile", Target: "work-readonly"},
		},
	}
	root := newExecuteRootFixture(func() error { return describeConnectFailure(guard, "cursor") })
	var cobraErr, stderr bytes.Buffer
	root.SetErr(&cobraErr)
	root.SetOut(&cobraErr)
	root.SetArgs([]string{"connect"})

	code := executeRoot(root, &stderr)

	all := cobraErr.String() + stderr.String()
	if got := strings.Count(all, "Fixes:"); got != 1 {
		t.Errorf("Fixes: printed %d times, want 1:\n%s", got, all)
	}
	if got := strings.Count(all, "Error:"); got != 1 {
		t.Errorf("Error: printed %d times, want 1:\n%s", got, all)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestExecuteRoot_UnknownCommandKeepsHelpHint(t *testing.T) {
	root := newExecuteRootFixture(func() error { return nil })
	var cobraErr, stderr bytes.Buffer
	root.SetErr(&cobraErr)
	root.SetOut(&cobraErr)
	root.SetArgs([]string{"nope"})

	code := executeRoot(root, &stderr)

	all := cobraErr.String() + stderr.String()
	if got := strings.Count(all, `unknown command "nope"`); got != 1 {
		t.Errorf("unknown command printed %d times, want 1:\n%s", got, all)
	}
	if !strings.Contains(all, "Run 'mcpproxy --help' for usage.") {
		t.Errorf("missing help hint:\n%s", all)
	}
	if code == ExitCodeSuccess {
		t.Error("exit code must be non-zero")
	}
}

func TestExecuteRoot_SuccessIsSilent(t *testing.T) {
	root := newExecuteRootFixture(func() error { return nil })
	var cobraErr, stderr bytes.Buffer
	root.SetErr(&cobraErr)
	root.SetArgs([]string{"connect"})
	if code := executeRoot(root, &stderr); code != ExitCodeSuccess {
		t.Errorf("exit code = %d", code)
	}
	if cobraErr.Len()+stderr.Len() != 0 {
		t.Errorf("unexpected output: %q %q", cobraErr.String(), stderr.String())
	}
}
