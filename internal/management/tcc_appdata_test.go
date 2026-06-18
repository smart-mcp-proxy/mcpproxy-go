package management

import (
	"strings"
	"testing"
)

// fakeAppDataProbe is a programmable appDataProbe for testing the warning
// translation without a real macOS denial.
type fakeAppDataProbe struct {
	denied      bool
	remediation string
}

func (f fakeAppDataProbe) DetectAppDataDenial() (bool, string) {
	return f.denied, f.remediation
}

// TestAppDataWarningFrom covers the doctor check translation (Spec 075 US3, T021):
// a probe that reports a denial yields a warning carrying the remediation; a
// probe that reports no denial yields nothing.
func TestAppDataWarningFrom(t *testing.T) {
	t.Run("denied yields a warning with remediation", func(t *testing.T) {
		remediation := "Fix: tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy"
		warning, ok := appDataWarningFrom(fakeAppDataProbe{denied: true, remediation: remediation})
		if !ok {
			t.Fatal("expected ok=true when a denial is reported")
		}
		if !strings.Contains(warning, remediation) {
			t.Errorf("warning must carry the remediation, got %q", warning)
		}
		if !strings.Contains(warning, "App Data") {
			t.Errorf("warning should name the App Data privacy gate, got %q", warning)
		}
	})

	t.Run("no denial yields nothing", func(t *testing.T) {
		warning, ok := appDataWarningFrom(fakeAppDataProbe{denied: false})
		if ok || warning != "" {
			t.Fatalf("expected no warning when not denied, got ok=%v warning=%q", ok, warning)
		}
	})
}
