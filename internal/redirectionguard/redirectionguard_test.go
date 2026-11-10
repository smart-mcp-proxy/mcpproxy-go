package redirectionguard

import (
	"strings"
	"testing"
)

func TestWarningFor(t *testing.T) {
	if msg, ok := WarningFor(false); ok || msg != "" {
		t.Fatalf("WarningFor(false) = %q, %v; want no warning", msg, ok)
	}

	msg, ok := WarningFor(true)
	if !ok {
		t.Fatal("WarningFor(true) must produce a warning")
	}
	for _, want := range []string{
		"RedirectionGuard",
		"untrusted mount point. (os error 448)",
		"uvx",
		"Start menu",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning %q does not mention %q", msg, want)
		}
	}
}

func TestCheckReportsCanonicalWarning(t *testing.T) {
	// Off Windows the mitigation does not exist; on Windows CI the test
	// process normally does not run with it. Either way a reported warning
	// must be the canonical one.
	if msg, ok := Check(); ok {
		if want, _ := WarningFor(true); msg != want {
			t.Fatalf("Check() = %q, want canonical warning", msg)
		}
	}
}

func TestPolicyEnforced(t *testing.T) {
	// ret is GetProcessMitigationPolicy's BOOL; only bit 0 of Flags
	// (EnforceRedirectionTrust) means enforced, not bit 1 (AuditRedirectionTrust).
	cases := []struct {
		name  string
		ret   uintptr
		flags uint32
		want  bool
	}{
		{"call failed, zeroed flags", 0, 0, false},
		{"call failed, stale flags", 0, 1, false},
		{"succeeded, not enforced", 1, 0, false},
		{"succeeded, enforced", 1, 1, true},
		{"succeeded, audit only", 1, 2, false},
		{"succeeded, enforced and audit", 1, 3, true},
	}
	for _, tc := range cases {
		if got := policyEnforced(tc.ret, tc.flags); got != tc.want {
			t.Errorf("%s: policyEnforced(%d, %#x) = %v, want %v", tc.name, tc.ret, tc.flags, got, tc.want)
		}
	}
}
