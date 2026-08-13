package diagnostics

import "testing"

// updateFailureCodes is the closed set of tray auto-update failure codes
// (spec 095). One per stage of the `appcast|download|install|other` enum the
// tray reports; the heartbeat surfaces them through the existing
// diagnostics.error_code_counts_24h map.
var updateFailureCodes = []Code{
	UpdateAppcastFailed,
	UpdateDownloadFailed,
	UpdateInstallFailed,
	UpdateOtherFailed,
}

// TestUpdateCodes_Values pins the wire-visible strings. These are transmitted
// telemetry enum values — renaming one silently breaks downstream queries.
func TestUpdateCodes_Values(t *testing.T) {
	want := map[Code]string{
		UpdateAppcastFailed:  "MCPX_UPDATE_APPCAST_FAILED",
		UpdateDownloadFailed: "MCPX_UPDATE_DOWNLOAD_FAILED",
		UpdateInstallFailed:  "MCPX_UPDATE_INSTALL_FAILED",
		UpdateOtherFailed:    "MCPX_UPDATE_OTHER_FAILED",
	}
	for code, str := range want {
		if string(code) != str {
			t.Errorf("code constant = %q, want %q", code, str)
		}
	}
}

// TestUpdateCodes_Registered — FR-014: the four codes must be catalog-registered,
// not merely MCPX_-prefixed. RecordErrorCode's own guard is prefix-only, and the
// anonymity scanner admits error_code_counts_24h keys by catalog membership.
func TestUpdateCodes_Registered(t *testing.T) {
	for _, c := range updateFailureCodes {
		if !Has(c) {
			t.Errorf("code %q is not registered in the catalog", c)
		}
		if _, ok := Get(c); !ok {
			t.Errorf("Get(%q) reported the code missing", c)
		}
	}
}

// TestUpdateCodes_InAll — All() drives the CLI/docs surfaces, so the new codes
// must be enumerable there too.
func TestUpdateCodes_InAll(t *testing.T) {
	seen := make(map[Code]bool, len(All()))
	for _, e := range All() {
		seen[e.Code] = true
	}
	for _, c := range updateFailureCodes {
		if !seen[c] {
			t.Errorf("code %q missing from All()", c)
		}
	}
}
