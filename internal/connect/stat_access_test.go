package connect

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// A config mcpproxy cannot even STAT is not an absent config. Every stat error
// used to collapse to "absent", so a permission-blocked config rendered the
// create promise ("this file does not exist; it will be created, and Undo
// removes it") with the Connect control available — and the denial was
// discovered only by clicking, which is exactly what running the write's guards
// in the preview exists to prevent (FR-003 / Spec 075 FR-004).

// denyingStat fails every stat with a permission error, the way an unreadable
// parent directory (or a macOS App-Data block) does.
func denyingStat(path string) (os.FileInfo, error) {
	return nil, &fs.PathError{Op: "stat", Path: path, Err: syscall.EPERM}
}

func TestPreview_UnstattableConfigIsDeniedNotAbsent(t *testing.T) {
	svc, home := testService(t)
	writeFileT(t, ConfigPath("claude-code", home), `{"mcpServers":{}}`)
	svc.setStat(denyingStat)

	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err == nil {
		t.Fatalf("a config that cannot be stat'ed must not preview as a create, got %+v", preview)
	}
	var accessErr *AccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("expected a typed *AccessError (403 + remediation), got %T: %v", err, err)
	}
	if accessErr.Remediation == "" {
		t.Fatal("a denial must carry actionable remediation")
	}
}

func TestGetStatus_UnstattableConfigIsDeniedNotAbsent(t *testing.T) {
	svc, home := testService(t)
	writeFileT(t, ConfigPath("claude-code", home), `{"mcpServers":{}}`)
	svc.setStat(denyingStat)

	status, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.AccessState != accessDenied {
		t.Fatalf("access_state = %q, want %q — an unstattable config is not a missing one",
			status.AccessState, accessDenied)
	}
	if status.Remediation == "" {
		t.Fatal("a denied status must carry remediation (Spec 075 FR-004)")
	}
}

// The OpenCode resolver picks whichever candidate exists, because writing next
// to an existing .jsonc is silently shadowed by it (#922). A candidate that
// cannot be stat'ed must therefore be treated as present, not skipped: skipping
// it targets the shadowed .json and the connect silently does nothing.
func TestOpenCodeCandidate_UnstattableJsoncIsNotSkipped(t *testing.T) {
	svc, home := testService(t)
	dir := filepath.Dir(ConfigPath("opencode", home))
	jsonc := filepath.Join(dir, "opencode.jsonc")
	plain := filepath.Join(dir, "opencode.json")
	writeFileT(t, jsonc, `{"mcp":{}}`)
	writeFileT(t, plain, `{"mcp":{}}`)

	svc.setStat(func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "opencode.jsonc" {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: syscall.EPERM}
		}
		return os.Stat(path)
	})

	if got := svc.configPath("opencode"); got != jsonc {
		t.Fatalf("configPath = %s, want the shadowing %s — a candidate we cannot stat must not be assumed absent",
			got, jsonc)
	}
}

// The same thing through real OS permissions rather than the seam.
func TestPreview_PermissionBlockedParentDirIsDeniedNotAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions do not deny stat on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}

	svc, home := testService(t)
	cfgPath := ConfigPath("claude-code", home)
	writeFileT(t, cfgPath, `{"mcpServers":{}}`)
	dir := filepath.Dir(cfgPath)
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, statErr := os.Stat(cfgPath); statErr == nil {
		t.Skip("this filesystem still permits the stat")
	}

	preview, err := svc.Preview("claude-code", "mcpproxy")
	if err == nil {
		t.Fatalf("expected a denial, got a preview: access_state=%q entry_exists=%v",
			preview.AccessState, preview.EntryExists)
	}
	var accessErr *AccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("expected a typed *AccessError, got %T: %v", err, err)
	}
}

// The stat-only aggregate must not name the wrong problem either: it stays
// content-read-free, but a stat it was not allowed to make is not evidence that
// the client has no config.
func TestGetAllStatus_UnstattableConfigIsNotSilentlyAbsent(t *testing.T) {
	svc, _ := testService(t)
	svc.setStat(denyingStat)

	for _, st := range svc.GetAllStatus() {
		if st.ID != "claude-code" {
			continue
		}
		if st.Exists {
			t.Fatal("a denied stat is not proof the file is there either")
		}
		if st.AccessState != accessDenied {
			t.Fatalf("access_state = %q, want %q so the row does not read \"No config found\"",
				st.AccessState, accessDenied)
		}
		return
	}
	t.Fatal("claude-code missing from GetAllStatus")
}
