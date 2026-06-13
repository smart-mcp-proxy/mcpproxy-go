package httpapi

import "testing"

// TestDecodePathParamScanJobID is the MCP-2123 regression guard for the
// scan-report-by-job-id route. Scan job IDs are "scan-<serverName>-<ts>", and
// official-registry server names contain '/', so the id reaches the handler
// percent-encoded (chi routes on RawPath). handleGetScanReportByJobID must
// percent-decode it before the exact-match job lookup, otherwise "View Full
// Report" 404s for slash-named servers even after the SPA route resolves.
func TestDecodePathParamScanJobID(t *testing.T) {
	encoded := "scan-com.pulsemcp%2Fgoogle-flights-1781284446323229000"
	want := "scan-com.pulsemcp/google-flights-1781284446323229000"
	if got := decodePathParam(encoded); got != want {
		t.Errorf("decodePathParam(%q) = %q, want %q", encoded, got, want)
	}

	// A non-encoded id must pass through unchanged.
	plain := "scan-simple-server-123"
	if got := decodePathParam(plain); got != plain {
		t.Errorf("decodePathParam(%q) = %q, want unchanged", plain, got)
	}
}
