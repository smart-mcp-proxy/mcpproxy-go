package connect

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestGetAllStatus_NoContentReads asserts that computing the overall Connect
// status determines installed-state via metadata only and never reads any
// client config file's contents (FR-001, SC-001). The injected reader fails
// the test if invoked.
func TestGetAllStatus_NoContentReads(t *testing.T) {
	svc, homeDir := testService(t)

	// Make a couple of clients appear "installed" by creating their config files.
	installed := []string{"claude-code", "cursor"}
	for _, id := range installed {
		cfgPath := ConfigPath(id, homeDir)
		if cfgPath == "" {
			t.Fatalf("no config path for %s", id)
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Any content read during overall status is a contract violation.
	svc.setReadFile(func(path string) ([]byte, error) {
		t.Errorf("GetAllStatus must not read config contents, but read %s", path)
		return nil, fmt.Errorf("unexpected content read of %s", path)
	})

	statuses := svc.GetAllStatus()

	seen := map[string]bool{}
	for _, st := range statuses {
		seen[st.ID] = true
		if st.ID == "claude-code" || st.ID == "cursor" {
			if !st.Exists {
				t.Errorf("%s: expected Exists=true via metadata", st.ID)
			}
			// Overall status must not claim connected without a content read.
			if st.Connected {
				t.Errorf("%s: Connected must be false in overall (content-read-free) status", st.ID)
			}
			if st.AccessState != accessUnknown {
				t.Errorf("%s: expected access_state=%q, got %q", st.ID, accessUnknown, st.AccessState)
			}
		}
	}
	for _, id := range installed {
		if !seen[id] {
			t.Errorf("expected %s in overall status", id)
		}
	}
}

// TestGetStatus_ReadsSingleClientOnDemand asserts that GetStatus(id) performs
// exactly one content read for the requested client and resolves Connected +
// AccessState (FR-002, SC-001 independent test).
func TestGetStatus_ReadsSingleClientOnDemand(t *testing.T) {
	svc, _ := testService(t)

	// Register mcpproxy in claude-code so its config is "connected".
	if _, err := svc.Connect("claude-code", "", false); err != nil {
		t.Fatal(err)
	}

	// Count reads only from this point on (Connect's own reads are excluded).
	var reads int
	var readPaths []string
	realRead := os.ReadFile
	svc.setReadFile(func(path string) ([]byte, error) {
		reads++
		readPaths = append(readPaths, path)
		return realRead(path)
	})

	st, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if reads != 1 {
		t.Errorf("expected exactly 1 content read, got %d (%v)", reads, readPaths)
	}
	if !st.Exists {
		t.Error("expected Exists=true")
	}
	if !st.Connected {
		t.Error("expected Connected=true after connect")
	}
	if st.AccessState != accessAccessible {
		t.Errorf("expected access_state=%q, got %q", accessAccessible, st.AccessState)
	}
}

// TestGetStatus_AbsentClient resolves to absent without a content read failure.
func TestGetStatus_AbsentClient(t *testing.T) {
	svc, _ := testService(t)

	st, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.Exists {
		t.Error("expected Exists=false for a client with no config")
	}
	if st.Connected {
		t.Error("expected Connected=false for absent client")
	}
	if st.AccessState != accessAbsent {
		t.Errorf("expected access_state=%q, got %q", accessAbsent, st.AccessState)
	}
}

// TestGetStatus_Malformed reports malformed when the config cannot be parsed.
func TestGetStatus_Malformed(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("claude-code", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{ this is not valid json `), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := svc.GetStatus("claude-code")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !st.Exists {
		t.Error("expected Exists=true for present-but-malformed config")
	}
	if st.Connected {
		t.Error("expected Connected=false for malformed config")
	}
	if st.AccessState != accessMalformed {
		t.Errorf("expected access_state=%q, got %q", accessMalformed, st.AccessState)
	}
}

// TestGetStatus_UnknownClient errors for an unknown client id.
func TestGetStatus_UnknownClient(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.GetStatus("does-not-exist"); err == nil {
		t.Error("expected error for unknown client")
	}
}
