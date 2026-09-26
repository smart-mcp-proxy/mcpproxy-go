package connect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllSupportedClientsHaveReloadHintAndAliases is T028: every supported
// ClientDef must have a non-empty ReloadHint and at least one
// ClientInfoNames alias, so the presence/onboarding layers built on top of
// this table never silently fall back to a blank hint or an unmatchable
// client.
func TestAllSupportedClientsHaveReloadHintAndAliases(t *testing.T) {
	for _, c := range GetAllClients() {
		c := c
		t.Run(c.ID, func(t *testing.T) {
			if !c.Supported {
				t.Skip("not a connectable client")
			}
			if c.ReloadHint == "" {
				t.Errorf("client %s: ReloadHint must not be empty", c.ID)
			}
			if len(c.ClientInfoNames) == 0 {
				t.Errorf("client %s: ClientInfoNames must not be empty", c.ID)
			}
			for _, alias := range c.ClientInfoNames {
				if alias == "" {
					t.Errorf("client %s: ClientInfoNames must not contain an empty alias", c.ID)
				}
			}
		})
	}
}

// TestDisplayPath covers the home-prefix substitution (FR-037): a path under
// the home directory is shortened to "~", everything else passes through
// unchanged.
func TestDisplayPath(t *testing.T) {
	home := string(filepath.Separator) + filepath.Join("Users", "alice")

	tests := []struct {
		name string
		path string
		home string
		want string
	}{
		{
			name: "path under home is shortened",
			path: filepath.Join(home, ".cursor", "mcp.json"),
			home: home,
			want: filepath.Join("~", ".cursor", "mcp.json"),
		},
		{
			name: "path equal to home",
			path: home,
			home: home,
			want: "~",
		},
		{
			name: "path outside home is unchanged",
			path: filepath.Join(string(filepath.Separator), "etc", "mcpproxy", "config.json"),
			home: home,
			want: filepath.Join(string(filepath.Separator), "etc", "mcpproxy", "config.json"),
		},
		{
			name: "empty path stays empty",
			path: "",
			home: home,
			want: "",
		},
		{
			// homeDir "/" (root — e.g. HOME=/ in a minimal container, or a
			// misconfigured environment) must not turn into an empty prefix
			// after TrimRight, which would make every absolute path match.
			name: "root home dir does not swallow every absolute path",
			path: filepath.Join(string(filepath.Separator), "etc", "mcpproxy", "config.json"),
			home: string(filepath.Separator),
			want: filepath.Join(string(filepath.Separator), "etc", "mcpproxy", "config.json"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DisplayPath(tc.path, tc.home)
			if got != tc.want {
				t.Errorf("DisplayPath(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
			}
		})
	}
}

// TestDisplayPath_WindowsCaseInsensitiveHomeMatch guards a Windows-only trap:
// filesystem paths there are case-insensitive, and the home prefix
// (os.UserHomeDir -> %USERPROFILE%) and per-client config roots (%APPDATA%/
// %LOCALAPPDATA%) are independent env vars that can legitimately differ in
// casing (profile migration, roaming profiles). An exact byte comparison
// then fails to recognize a path that IS under home, leaving the full raw
// path displayed instead of the "~"-shortened form DisplayPath exists to
// produce. caseInsensitiveHomeMatch is overridden here (rather than gated on
// runtime.GOOS) so the behavior is exercised on every CI platform, not just
// Windows runners.
func TestDisplayPath_WindowsCaseInsensitiveHomeMatch(t *testing.T) {
	orig := caseInsensitiveHomeMatch
	caseInsensitiveHomeMatch = func() bool { return true }
	t.Cleanup(func() { caseInsensitiveHomeMatch = orig })

	sep := string(filepath.Separator)
	home := sep + filepath.Join("Users", "Alice")
	path := sep + filepath.Join("users", "alice", ".cursor", "mcp.json")
	want := "~" + sep + filepath.Join(".cursor", "mcp.json")
	if got := DisplayPath(path, home); got != want {
		t.Errorf("DisplayPath(%q, %q) = %q, want %q (case-insensitive home match)", path, home, got, want)
	}

	// A differently-cased path equal to home itself must still collapse to "~".
	differentlyCasedHome := sep + filepath.Join("USERS", "ALICE")
	if got := DisplayPath(differentlyCasedHome, home); got != "~" {
		t.Errorf("DisplayPath(%q, %q) = %q, want \"~\"", differentlyCasedHome, home, got)
	}
}

// TestDisplayPath_CaseSensitiveByDefault asserts that off Windows (the
// default caseInsensitiveHomeMatch), a differently-cased path is NOT treated
// as living under home -- the case-insensitive match above must not regress
// exact-match behavior on POSIX filesystems.
func TestDisplayPath_CaseSensitiveByDefault(t *testing.T) {
	home := string(filepath.Separator) + filepath.Join("Users", "alice")
	path := string(filepath.Separator) + filepath.Join("Users", "ALICE", ".cursor", "mcp.json")
	if got := DisplayPath(path, home); got != path {
		t.Errorf("DisplayPath(%q, %q) = %q, want unchanged %q (case-sensitive by default)", path, home, got, path)
	}
}

// TestDisplayPath_DefaultsToOSUserHomeDir asserts the empty-homeDir path
// resolves through os.UserHomeDir (which on Windows reads %USERPROFILE%),
// matching ConfigPath's own convention.
func TestDisplayPath_DefaultsToOSUserHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no resolvable home directory in this environment")
	}
	path := filepath.Join(home, ".cursor", "mcp.json")
	got := DisplayPath(path, "")
	want := filepath.Join("~", ".cursor", "mcp.json")
	if got != want {
		t.Errorf("DisplayPath(%q, \"\") = %q, want %q", path, got, want)
	}
}

// TestClientStatusCarriesDisplayPathAndReloadHint asserts ClientStatus
// (GET /connect, GET /connect/{client}) exposes both new fields for a
// supported client, using a fake HOME so the test is hermetic.
func TestClientStatusCarriesDisplayPathAndReloadHint(t *testing.T) {
	home := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)

	statuses := svc.GetAllStatus()
	found := false
	for _, st := range statuses {
		if st.ID != "cursor" {
			continue
		}
		found = true
		wantPath := DisplayPath(st.ConfigPath, home)
		if st.DisplayPath != wantPath {
			t.Errorf("DisplayPath = %q, want %q", st.DisplayPath, wantPath)
		}
		if st.DisplayPath == st.ConfigPath {
			t.Errorf("DisplayPath %q should differ from the full ConfigPath %q under a fake home", st.DisplayPath, st.ConfigPath)
		}
		if st.ReloadHint == "" {
			t.Errorf("ReloadHint must not be empty for a supported client")
		}
	}
	if !found {
		t.Fatal("expected a cursor client status")
	}

	single, err := svc.GetStatus("cursor")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if single.ReloadHint == "" {
		t.Error("GetStatus: ReloadHint must not be empty")
	}
	if single.DisplayPath == "" {
		t.Error("GetStatus: DisplayPath must not be empty")
	}
}

// TestConnectResultCarriesDisplayPathAndReloadHint asserts ConnectResult
// (POST /connect/{client}, disconnect too) exposes DisplayPath/ReloadHint on
// every branch: success, already-exists (no force) and disconnect.
func TestConnectResultCarriesDisplayPathAndReloadHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)

	res, err := svc.Connect("cursor", "", false)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	assertHintAndPath(t, res, home)
	if !res.Success {
		t.Fatalf("expected first connect to succeed, got action=%s message=%s", res.Action, res.Message)
	}

	// Second connect without force hits the already_exists branch — still
	// expected to carry the hint/path (FR-037 applies to every result, not
	// only the success path).
	res2, err := svc.Connect("cursor", "", false)
	if err != nil {
		t.Fatalf("Connect (again): %v", err)
	}
	assertHintAndPath(t, res2, home)
	if res2.Action != "already_exists" {
		t.Fatalf("expected already_exists, got action=%s", res2.Action)
	}

	dres, err := svc.Disconnect("cursor", "")
	if err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	assertHintAndPath(t, dres, home)
}

// TestDisconnectReloadHint_DescribesRemovalNotLoading is review round 3's
// finding: every ClientDef.ReloadHint is worded for a fresh connect (e.g.
// "Reload the Cursor window (or restart Cursor) to load MCPProxy"), and
// Disconnect's deferred fill used to copy that text verbatim onto a
// successful "removed" result — so a user who just disconnected Cursor read
// an instruction to reload it "to load MCPProxy" right after the entry was
// taken out. A successful disconnect's hint must describe applying the
// removal, not loading something that is no longer configured.
func TestDisconnectReloadHint_DescribesRemovalNotLoading(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)

	if _, err := svc.Connect("cursor", "", false); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	res, err := svc.Disconnect("cursor", "")
	if err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if res.Action != "removed" {
		t.Fatalf("expected action=removed, got %s", res.Action)
	}
	if res.ReloadHint == "" {
		t.Fatal("ReloadHint must not be empty")
	}
	if strings.Contains(res.ReloadHint, "to load MCPProxy") {
		t.Errorf("ReloadHint = %q, must not read as loading MCPProxy after it was just removed", res.ReloadHint)
	}
}

// TestPreviewCarriesDisplayPath asserts GET /connect/{client}/preview exposes
// the same DisplayPath as ClientStatus/ConnectResult (FR-037), so the
// pre-connect preview pane and the post-connect result render the same
// client's config path identically instead of the preview alone showing the
// raw, unshortened ConfigPath.
func TestPreviewCarriesDisplayPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)

	preview, err := svc.Preview("cursor", "")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	want := DisplayPath(preview.ConfigPath, home)
	if preview.DisplayPath != want {
		t.Errorf("DisplayPath = %q, want %q", preview.DisplayPath, want)
	}
	if preview.DisplayPath == preview.ConfigPath {
		t.Errorf("DisplayPath %q should differ from the full ConfigPath %q under a fake home", preview.DisplayPath, preview.ConfigPath)
	}
}

// TestUndoCarriesDisplayPathAndReloadHint is review round 3's finding: unlike
// Connect and Disconnect, Undo's five ConnectResult literals (the two
// refusal branches — backup gone, config drifted — and the two outcome
// branches — file deleted, backup restored) never set DisplayPath or
// ReloadHint, contradicting ConnectResult.DisplayPath's own doc comment
// ("Populated for every result whose ConfigPath is known"). A caller driving
// a UI row off an undo result would see both fields empty on every branch.
func TestUndoCarriesDisplayPathAndReloadHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	svc := NewServiceWithHome("127.0.0.1:8080", "", home)

	// Branch: connect created the file (no prior backup) — undo deletes it.
	created, err := svc.Connect("cursor", "", false)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if created.BackupPath != "" {
		t.Fatalf("precondition: expected a fresh create with no backup, got backup=%q", created.BackupPath)
	}
	deleted, err := svc.Undo("cursor", "", "")
	if err != nil {
		t.Fatalf("Undo (delete branch): %v", err)
	}
	if deleted.Action != "deleted" {
		t.Fatalf("expected action=deleted, got %s message=%s", deleted.Action, deleted.Message)
	}
	assertHintAndPath(t, deleted, home)

	// Branch: undo refuses — the named backup does not exist ("not_found").
	// FR-037 promises DisplayPath/ReloadHint on every branch whose ConfigPath
	// is known, refusals included (mirroring Connect's already_exists/
	// precondition_failed branches, both covered above).
	notFound, err := svc.Undo("cursor", "", "mcp.json.bak.does-not-exist")
	if err != nil {
		t.Fatalf("Undo (not_found branch): %v", err)
	}
	if notFound.Action != "not_found" {
		t.Fatalf("expected action=not_found, got %s message=%s", notFound.Action, notFound.Message)
	}
	assertHintAndPath(t, notFound, home)

	// Recreate, then force-overwrite the same entry so THIS connect takes a
	// real backup (backupFile only backs up a file that already exists).
	if _, err := svc.Connect("cursor", "", false); err != nil {
		t.Fatalf("Connect (recreate): %v", err)
	}
	updated, err := svc.Connect("cursor", "", true)
	if err != nil {
		t.Fatalf("Connect (force update): %v", err)
	}
	if updated.BackupPath == "" {
		t.Fatal("precondition: expected the force-update to take a real backup")
	}

	// Branch: undo restores from that backup ("restored") — the happy path
	// of the byte-for-byte revert.
	restored, err := svc.Undo("cursor", "", filepath.Base(updated.BackupPath))
	if err != nil {
		t.Fatalf("Undo (restored branch): %v", err)
	}
	if restored.Action != "restored" {
		t.Fatalf("expected action=restored, got %s message=%s", restored.Action, restored.Message)
	}
	assertHintAndPath(t, restored, home)

	// Branch: undo refuses — the current file drifted since connect
	// ("conflict"). Force-update again for a fresh backup, then hand-edit the
	// live file so the drift check trips.
	updated2, err := svc.Connect("cursor", "", true)
	if err != nil {
		t.Fatalf("Connect (force update 2): %v", err)
	}
	cfgPath := svc.configPath("cursor")
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("drift the live file: %v", err)
	}
	conflict, err := svc.Undo("cursor", "", filepath.Base(updated2.BackupPath))
	if err != nil {
		t.Fatalf("Undo (conflict branch): %v", err)
	}
	if conflict.Action != "conflict" {
		t.Fatalf("expected action=conflict, got %s message=%s", conflict.Action, conflict.Message)
	}
	assertHintAndPath(t, conflict, home)
}

func assertHintAndPath(t *testing.T, res *ConnectResult, home string) {
	t.Helper()
	if res == nil {
		t.Fatal("result must not be nil")
	}
	if res.ReloadHint == "" {
		t.Error("ReloadHint must not be empty")
	}
	want := DisplayPath(res.ConfigPath, home)
	if res.DisplayPath != want {
		t.Errorf("DisplayPath = %q, want %q", res.DisplayPath, want)
	}
}
