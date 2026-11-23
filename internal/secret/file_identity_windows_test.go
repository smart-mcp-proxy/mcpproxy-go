//go:build windows

package secret

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Cross-review R1 (PR #1884): os.Stat on Windows loads the file ID lazily by
// path, so a FileInfo kept across a rename names the new file. The eager
// identity read from a handle must still name the old one.
func TestFileIdentity_WindowsEagerAcrossRename(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte("old"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Link(cfgPath, link))

	before, ok := identityOfPath(cfgPath)
	require.True(t, ok)
	lazy, err := os.Stat(cfgPath)
	require.NoError(t, err)

	tmp := cfgPath + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte("new"), 0o600))
	require.NoError(t, os.Rename(tmp, cfgPath))

	after, ok := identityOfPath(cfgPath)
	require.True(t, ok)
	linked, ok := identityOfPath(link)
	require.True(t, ok)

	require.NotEqual(t, before, after, "the rename gave the config a new file index")
	require.Equal(t, before, linked, "the eager identity still names the replaced file")

	newInfo, err := os.Stat(cfgPath)
	require.NoError(t, err)
	if os.SameFile(lazy, newInfo) {
		t.Log("os.Stat FileInfo resolved lazily to the new file, as the eager identity avoids")
	}
}
