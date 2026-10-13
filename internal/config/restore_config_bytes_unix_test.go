//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 review round 1: the commit's compensation restores the captured
// mode exactly, whatever the process umask, and a captured 0000 is a real
// mode, not "unset".
func TestRestoreConfigFileBytes_ExactModeUnderUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	for _, mode := range []os.FileMode{0o640, 0o644, 0o600, 0o000} {
		path := filepath.Join(t.TempDir(), "mcp_config.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"new":true}`), 0o600))
		require.NoError(t, RestoreConfigFileBytes(path, []byte(`{"old":true}`), mode))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm(), "mode %o must be restored exactly", mode)
		require.NoError(t, os.Chmod(path, 0o600))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, `{"old":true}`, string(data))
	}
}
