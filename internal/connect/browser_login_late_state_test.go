//go:build !server

package connect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A key connect that saw no state file before its write looks again afterwards:
// a state file another process created in between is advanced too.
func TestKeyConnect_AdvancesAStateFileThatAppearedDuringTheWrite(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	cfg := ConfigPath("claude-code", home)

	created := false
	svc.setReadFile(func(p string) ([]byte, error) {
		// the config write's last check runs after the backup exists
		if baks, _ := filepath.Glob(cfg + ".bak.*"); p == cfg && len(baks) > 0 && !created {
			created = true
			writeState(t, cfg, undoState{Version: 1, Current: gen(1)})
		}
		return os.ReadFile(p)
	})
	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Force: true})
	if err != nil || !res.Success {
		t.Fatalf("%v %+v", err, res)
	}
	if !created {
		t.Fatal("fixture: the hook never ran")
	}
	raw, _ := os.ReadFile(cfg + undoStateSuffix)
	st, ok := parseUndoState(raw)
	if !ok || st.Pending || st.Current == gen(1) || !strings.Contains(string(raw), st.Current) {
		t.Fatalf("the late state is advanced to a fresh settled generation: %s", raw)
	}
}
