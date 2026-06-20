package connect

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestClassifyAccess covers the four outcomes derived from the OS error class
// (Spec 075 FR-003/FR-011): classification comes from errors.Is, never from
// string-matching arbitrary error text.
func TestClassifyAccess(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want AccessOutcome
	}{
		{"nil is accessible", nil, accessAccessible},
		{"ErrNotExist is absent", fs.ErrNotExist, accessAbsent},
		{"PathError ENOENT is absent", &fs.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT}, accessAbsent},
		{"ErrPermission is denied", fs.ErrPermission, accessDenied},
		{"PathError EPERM is denied", &fs.PathError{Op: "open", Path: "/x", Err: syscall.EPERM}, accessDenied},
		{"PathError EACCES is denied", &fs.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}, accessDenied},
		{"wrapped EPERM is denied", fmt.Errorf("read /x: %w", &fs.PathError{Err: syscall.EPERM}), accessDenied},
		{"parse error is malformed", errors.New("invalid character 'x'"), accessMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAccess(tt.err); got != tt.want {
				t.Errorf("classifyAccess(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestAccessError_ErrorAndUnwrap verifies the typed error carries remediation,
// is errors.As-discoverable, and unwraps to the OS permission cause so the REST
// layer can map it (data-model.md AccessError).
func TestAccessError_ErrorAndUnwrap(t *testing.T) {
	cause := &fs.PathError{Op: "open", Path: "/cfg", Err: syscall.EPERM}
	ae := &AccessError{
		Client:      "Claude Code",
		Path:        "/cfg",
		Outcome:     accessDenied,
		Remediation: remediationText("Claude Code"),
		Err:         cause,
	}

	if !strings.Contains(ae.Error(), "tccutil reset SystemPolicyAppData") {
		t.Errorf("Error() should include remediation, got: %q", ae.Error())
	}
	if !errors.Is(ae, fs.ErrPermission) {
		t.Error("AccessError should unwrap to fs.ErrPermission")
	}
	var target *AccessError
	if !errors.As(fmt.Errorf("connect: %w", ae), &target) {
		t.Error("AccessError should be discoverable via errors.As through a wrap")
	}
}

// TestRemediationText asserts the canonical message names the cause, the App
// Data settings path, the tccutil reset command, and both bundle ids (FR-005).
func TestRemediationText(t *testing.T) {
	got := remediationText("Cursor")
	for _, want := range []string{
		"Cursor",
		"Privacy & Security",
		"App Data",
		"tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy",
		"com.smartmcpproxy.mcpproxy.dev",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("remediationText missing %q; got:\n%s", want, got)
		}
	}
}

// epermReader returns a content reader that always fails with a TCC-style
// permission denial, simulating a macOS App-Data block without a real OS denial.
func epermReader(path string) ([]byte, error) {
	return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EPERM}
}

// TestGetStatus_DeniedSurfacesRemediation: a permission-denied content read on
// an installed client resolves to access_state=denied with actionable
// remediation, and must NOT be reported as plain "not connected" (FR-004).
func TestGetStatus_DeniedSurfacesRemediation(t *testing.T) {
	svc, homeDir := testService(t)

	// The config file must exist on disk so os.Stat (metadata) reports installed;
	// the denial happens on the content read, mirroring macOS TCC App-Data.
	cfgPath := ConfigPath("claude-code", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.setReadFile(epermReader)

	st, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatalf("GetStatus should not hard-error on denial: %v", err)
	}
	if st.AccessState != accessDenied {
		t.Errorf("expected access_state=%q, got %q", accessDenied, st.AccessState)
	}
	if st.Connected {
		t.Error("denied access must not be reported as connected")
	}
	if !strings.Contains(st.Remediation, "tccutil reset SystemPolicyAppData") {
		t.Errorf("expected remediation with tccutil reset, got %q", st.Remediation)
	}
	if !strings.Contains(st.Remediation, "com.smartmcpproxy.mcpproxy") {
		t.Errorf("expected remediation to name the bundle id, got %q", st.Remediation)
	}
}

// TestConnectDenied_ReturnsAccessError: a permission denial on the connect path
// returns a typed *AccessError carrying remediation, distinct from the
// unknown-client and not-supported errors (FR-004).
func TestConnectDenied_ReturnsAccessError(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("claude-code", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.setReadFile(epermReader)

	_, err := svc.Connect("claude-code", "", false)
	if err == nil {
		t.Fatal("expected an error when the connect read is permission-denied")
	}
	var ae *AccessError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AccessError, got %T: %v", err, err)
	}
	if ae.Outcome != accessDenied {
		t.Errorf("expected Outcome=%q, got %q", accessDenied, ae.Outcome)
	}
	if !strings.Contains(ae.Remediation, "tccutil reset SystemPolicyAppData") {
		t.Errorf("expected remediation, got %q", ae.Remediation)
	}

	// A genuine unknown-client error must NOT be classified as a denial.
	if _, unknownErr := svc.Connect("does-not-exist", "", false); errors.As(unknownErr, &ae) {
		t.Error("unknown-client error must not be an *AccessError")
	}
}

// installClientConfig makes a supported client appear installed by writing a
// config file at its resolved path under the (test-isolated) home dir.
func installClientConfig(t *testing.T, homeDir, clientID, body string) {
	t.Helper()
	cfgPath := ConfigPath(clientID, homeDir)
	if cfgPath == "" {
		t.Fatalf("no config path for %s", clientID)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDetectAppDataDenial covers the doctor probe (Spec 075 US3, T020): an
// installed client whose content read is permission-denied reports a denial with
// remediation; a clean read does not; and no installed clients yields neither a
// denial nor any content read (no false positive).
func TestDetectAppDataDenial(t *testing.T) {
	t.Run("denied when an installed client config read is EPERM", func(t *testing.T) {
		svc, homeDir := testService(t)
		installClientConfig(t, homeDir, "claude-code", `{"mcpServers":{}}`)
		svc.setReadFile(epermReader)

		denied, remediation := svc.DetectAppDataDenial()
		if !denied {
			t.Fatal("expected denied=true for an EPERM content read on an installed client")
		}
		if !strings.Contains(remediation, "tccutil reset SystemPolicyAppData") {
			t.Errorf("remediation must carry the exact reset command, got %q", remediation)
		}
		if !strings.Contains(remediation, bundleIDProd) {
			t.Errorf("remediation must name the prod bundle id, got %q", remediation)
		}
	})

	t.Run("not denied when installed configs read cleanly", func(t *testing.T) {
		svc, homeDir := testService(t)
		installClientConfig(t, homeDir, "claude-code", `{"mcpServers":{}}`)
		svc.setReadFile(func(string) ([]byte, error) {
			return []byte(`{"mcpServers":{}}`), nil
		})

		denied, remediation := svc.DetectAppDataDenial()
		if denied || remediation != "" {
			t.Fatalf("clean read must not be a denial, got denied=%v remediation=%q", denied, remediation)
		}
	})

	t.Run("no false positive when no clients are installed", func(t *testing.T) {
		svc, _ := testService(t)
		// Fresh isolated home: nothing stat-exists, so a denial-returning reader
		// must never be consulted.
		svc.setReadFile(func(path string) ([]byte, error) {
			t.Errorf("reader must not be called when no client config exists; read %s", path)
			return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EPERM}
		})

		denied, remediation := svc.DetectAppDataDenial()
		if denied || remediation != "" {
			t.Fatalf("no installed clients must yield no denial, got denied=%v remediation=%q", denied, remediation)
		}
	})
}
