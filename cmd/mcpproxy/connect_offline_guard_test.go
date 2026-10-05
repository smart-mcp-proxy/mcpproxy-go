package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// seedBoundCursor connects cursor to `ro` locked while auth is on, then turns
// require_mcp_auth off, the state in which the offline guard applies.
func seedBoundCursor(t *testing.T) (home string, cfg *config.Config, cursorPath string) {
	t.Helper()
	home, cfg = connectTestEnv(t, true)
	cursorPath = seedCursor(t, home)
	out, err := runConnectArgs(t, "cursor", "--profile", "ro", "--lock")
	require.NoError(t, err, out)
	resetConnectFlagValues()

	cfg.RequireMCPAuth = false
	require.NoError(t, config.SaveConfig(cfg, configFile))
	return home, cfg, cursorPath
}

func resetConnectFlagValues() {
	connectProfile, connectLock, connectSwitchable, connectKeyless = "all", false, false, false
	connectList, connectAll, connectForce, connectServerName = false, false, false, ""
}

func cursorRecord(t *testing.T, cfg *config.Config) (pin, mode, pendingHash string) {
	t.Helper()
	sm, err := storage.NewManager(cfg.DataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer func() { _ = sm.Close() }()
	toks, err := sm.ListAgentTokens()
	require.NoError(t, err)
	for _, tk := range toks {
		if tk.Name == "client-cursor" {
			return tk.ProfilePin, tk.ProfileMode, tk.PendingHash
		}
	}
	t.Fatal("client-cursor record not found")
	return "", "", ""
}

// FR-008a offline: with auth off, a named binding that is NEW or CHANGED is
// refused, including a re-point of a client that is already bound.
func TestConnectProfileFlags_OfflineRefusesRepointOfABoundClient(t *testing.T) {
	_, cfg, path := seedBoundCursor(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	backupsBefore, _ := filepath.Glob(path + ".bak*")

	out, err := runConnectArgs(t, "cursor", "--force", "--profile", "work", "--lock")
	require.Error(t, err, out)
	require.Contains(t, err.Error(), "a client bound to profile work could escape it by omitting its credential while require_mcp_auth is off")
	require.Contains(t, err.Error(), "Fixes:")
	require.Equal(t, ExitCodeGeneralError, classifyError(err))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the config file is untouched")
	backupsAfter, _ := filepath.Glob(path + ".bak*")
	require.Equal(t, backupsBefore, backupsAfter, "no backup for a refused connect")

	pin, mode, pending := cursorRecord(t, cfg)
	require.Equal(t, "ro", pin)
	require.Equal(t, "locked", mode)
	require.Empty(t, pending, "no rotation was staged")
}

// A reconnect that keeps the recorded binding stays possible offline, so
// rotation does not need the daemon.
func TestConnectProfileFlags_OfflineReconnectKeepingBindingSucceeds(t *testing.T) {
	_, cfg, _ := seedBoundCursor(t)

	out, err := runConnectArgs(t, "cursor", "--force")
	require.NoError(t, err, out)

	pin, mode, pending := cursorRecord(t, cfg)
	require.Equal(t, "ro", pin)
	require.Equal(t, "locked", mode)
	require.Empty(t, pending, "the rotation finalized")
}

func TestDescribeConnectFailure_InFlightAndSuperseded(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&runtime.ConnectInProgressError{ClientID: "cursor"}, "a connect of cursor is already in progress; retry when it finishes"},
		{&runtime.CredentialSupersededError{ClientID: "cursor"}, "the credential written for cursor was replaced or revoked before it could be finalized; reconnect the client"},
	} {
		got := describeConnectFailure(fmt.Errorf("wrapped: %w", tc.err), "cursor")
		require.Equal(t, tc.want, got.Error())
		require.Equal(t, ExitCodeGeneralError, classifyError(got))
	}
}
