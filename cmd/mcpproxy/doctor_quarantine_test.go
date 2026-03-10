package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDisplayQuarantineStats_Empty(t *testing.T) {
	output := captureOutput(func() {
		displayQuarantineStats(nil)
	})
	if output != "" {
		t.Errorf("expected empty output for nil stats, got: %q", output)
	}

	output = captureOutput(func() {
		displayQuarantineStats([]quarantineServerStats{})
	})
	if output != "" {
		t.Errorf("expected empty output for empty stats, got: %q", output)
	}
}

func TestDisplayQuarantineStats_SingleServer(t *testing.T) {
	stats := []quarantineServerStats{
		{ServerName: "github-server", PendingCount: 5, ChangedCount: 0},
	}

	output := captureOutput(func() {
		displayQuarantineStats(stats)
	})

	if !strings.Contains(output, "Tools Pending Quarantine Approval") {
		t.Error("expected quarantine header in output")
	}
	if !strings.Contains(output, "github-server: 5 tools pending") {
		t.Errorf("expected server pending line, got: %s", output)
	}
	if !strings.Contains(output, "Total: 5 tools across 1 server") {
		t.Errorf("expected total line, got: %s", output)
	}
	if !strings.Contains(output, "mcpproxy upstream approve") {
		t.Error("expected CLI remediation hint")
	}
	if !strings.Contains(output, "mcpproxy upstream inspect") {
		t.Error("expected inspect remediation hint")
	}
}

func TestDisplayQuarantineStats_MultipleServers(t *testing.T) {
	stats := []quarantineServerStats{
		{ServerName: "cloudflare-observability", PendingCount: 9, ChangedCount: 0},
		{ServerName: "open-brain", PendingCount: 1, ChangedCount: 1},
	}

	output := captureOutput(func() {
		displayQuarantineStats(stats)
	})

	if !strings.Contains(output, "cloudflare-observability: 9 tools pending") {
		t.Errorf("expected cloudflare server line, got: %s", output)
	}
	if !strings.Contains(output, "open-brain: 2 tools pending (1 new, 1 changed)") {
		t.Errorf("expected open-brain server line with detail, got: %s", output)
	}
	if !strings.Contains(output, "Total: 11 tools across 2 servers") {
		t.Errorf("expected total line, got: %s", output)
	}
}

func TestDisplayQuarantineStats_ChangedOnly(t *testing.T) {
	stats := []quarantineServerStats{
		{ServerName: "test-server", PendingCount: 0, ChangedCount: 3},
	}

	output := captureOutput(func() {
		displayQuarantineStats(stats)
	})

	if !strings.Contains(output, "test-server: 3 tools pending (changed)") {
		t.Errorf("expected changed detail, got: %s", output)
	}
}

func TestDisplayQuarantineStats_SingleTool(t *testing.T) {
	stats := []quarantineServerStats{
		{ServerName: "my-server", PendingCount: 1, ChangedCount: 0},
	}

	output := captureOutput(func() {
		displayQuarantineStats(stats)
	})

	// Should use singular "tool" not "tools"
	if !strings.Contains(output, "1 tool pending") {
		t.Errorf("expected singular 'tool', got: %s", output)
	}
	if !strings.Contains(output, "1 tool across 1 server") {
		t.Errorf("expected singular total line, got: %s", output)
	}
}

func TestPluralSuffix(t *testing.T) {
	tests := []struct {
		count    int
		expected string
	}{
		{0, "s"},
		{1, ""},
		{2, "s"},
		{10, "s"},
	}

	for _, tt := range tests {
		got := pluralSuffix(tt.count)
		if got != tt.expected {
			t.Errorf("pluralSuffix(%d) = %q, want %q", tt.count, got, tt.expected)
		}
	}
}

// captureOutput captures stdout output from a function call.
func captureOutput(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}
