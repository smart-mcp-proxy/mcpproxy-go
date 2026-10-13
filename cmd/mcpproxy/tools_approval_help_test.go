package main

import (
	"strings"
	"testing"
)

// Spec 109 FR-027 / T022: the CLI names the same approval labels as Web and macOS.
func TestToolsListApprovalHelpNamesLabels(t *testing.T) {
	initToolsFlags()
	flag := toolsListCmd.Flags().Lookup("approval")
	if flag == nil {
		t.Fatal("tools list has no --approval flag")
	}
	for _, want := range []string{
		"approved (Approved)",
		"pending (New, needs review)",
		"changed (Changed, needs review)",
	} {
		if !strings.Contains(flag.Usage, want) {
			t.Errorf("--approval usage %q does not contain %q", flag.Usage, want)
		}
	}
	if strings.Contains(strings.ToLower(flag.Usage), "awaiting") {
		t.Errorf("--approval usage %q uses the retired word 'awaiting'", flag.Usage)
	}
}
