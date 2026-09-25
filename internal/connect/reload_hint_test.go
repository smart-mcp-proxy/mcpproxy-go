package connect

import (
	"os"
	"path/filepath"
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
