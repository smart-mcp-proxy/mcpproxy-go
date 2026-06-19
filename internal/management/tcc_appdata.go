package management

// appDataProbe reports whether a persisted macOS App-Data (TCC) denial is
// currently blocking reads of MCP client configurations. *connect.Service
// satisfies it via DetectAppDataDenial.
type appDataProbe interface {
	DetectAppDataDenial() (denied bool, remediation string)
}

// appDataWarningPrefix introduces the doctor runtime warning; the actionable
// tccutil remediation is appended after it.
const appDataWarningPrefix = "macOS blocked mcpproxy from reading MCP client configurations (Privacy & Security ▸ App Data)."

// appDataWarningFrom turns a probe result into a doctor runtime-warning string.
// It returns ("", false) when there is no denial, so the caller adds nothing to
// the diagnostics. It is pure and OS-independent so it can be unit-tested on
// every platform without a real macOS denial (Spec 075 US3, T021).
func appDataWarningFrom(p appDataProbe) (warning string, ok bool) {
	denied, remediation := p.DetectAppDataDenial()
	if !denied {
		return "", false
	}
	return appDataWarningPrefix + "\n" + remediation, true
}
