package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestFormatReviewResponseTableHonorsFull(t *testing.T) {
	raw := []byte(`{"data":{"server":{"name":"github"},"tools":[{"name":"create_issue","tier":"write","approval_status":"pending","scan_verdict":"warnings","description":"Create an issue\nwith labels","input_schema":{"type":"object"},"output_schema":{"type":"string"},"diff":"@@ -1 +1 @@\n-old\n+new"}]}}`)

	compact := captureReviewOutput(t, func() error { return formatReviewResponse("table", raw, false) })
	require.Contains(t, compact, "TOOL")
	require.Contains(t, compact, "create_issue")
	require.Contains(t, compact, "Create an issue")
	require.NotContains(t, compact, "with labels")
	require.NotContains(t, compact, "from the server, not verified")

	full := captureReviewOutput(t, func() error { return formatReviewResponse("table", raw, true) })
	require.Contains(t, full, "with labels")
	require.Contains(t, full, "from the server, not verified")
	require.Contains(t, full, "Input schema")
	require.Contains(t, full, "Output schema")
	require.Contains(t, full, "@@ -1 +1 @@")
}

func TestFormatReviewResponseTableWritesActionResult(t *testing.T) {
	for _, test := range []struct {
		raw  []byte
		want string
	}{
		{[]byte(`{"data":{"status":"approved","server_name":"github"}}`), "Approved server github"},
		{[]byte(`{"data":{"message":"Blocked 2 tools for server github"}}`), "Blocked 2 tools for server github"},
	} {
		output := captureReviewOutput(t, func() error { return formatReviewResponse("table", test.raw, false) })
		require.NotContains(t, output, "Server: <nil>")
		require.NotContains(t, output, "No results found")
		require.Contains(t, output, test.want)
	}
}

func TestFormatReviewResponseTableQueueColumns(t *testing.T) {
	raw := []byte(`{"data":{"servers":[{"server":"github","kind":"server_review","quarantined":true,"pending":2,"changed":1,"tier_counts":{"write":2},"scan":{"verdict":"warnings"}}]}}`)

	output := captureReviewOutput(t, func() error { return formatReviewResponse("table", raw, false) })
	for _, want := range []string{"SERVER", "QUARANTINED", "PENDING", "CHANGED", "TIERS", "SCAN", "github"} {
		require.Contains(t, output, want)
	}
}

func TestReviewAliasHelpPointsToReviewWorkflow(t *testing.T) {
	for _, command := range []*cobra.Command{
		newToolsApproveCmd(),
		newToolsRejectCmd(),
		newSecurityApproveCmd(),
		newSecurityRejectCmd(),
		upstreamApproveCmd,
	} {
		require.Contains(t, command.Long, "mcpproxy review", command.Use)
	}
}

func captureReviewOutput(t *testing.T, fn func() error) string {
	t.Helper()
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = previous })
	require.NoError(t, fn())
	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return strings.TrimSpace(string(output))
}
