//go:build windows

package secret

import (
	"os"
	"path/filepath"
	"syscall"
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

// Cross-review R3 (PR #1884): a config path longer than MAX_PATH must still
// be captured (CreateFile needs the \\?\ form for it).
func TestFileIdentity_WindowsLongPath(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for len(deep) < 300 {
		deep = filepath.Join(deep, "abcdefghijklmnopqrstuvwxyz")
	}
	require.NoError(t, os.MkdirAll(deep, 0o700))
	cfgPath := filepath.Join(deep, "mcp_config.json")
	require.Greater(t, len(cfgPath), 260)
	require.NoError(t, os.WriteFile(cfgPath, []byte("cfg"), 0o600))

	_, err := identityOfPathErr(cfgPath)
	require.NoError(t, err)
	require.NoError(t, NoteSupersededProxyFile(cfgPath))
}

// Cross-review R2 (PR #1884): another process may hold the config open with
// DELETE access. os.Open does not share DELETE and fails with a sharing
// violation; the identity capture must share every mode and still succeed.
func TestFileIdentity_WindowsCaptureWhileHeldWithDeleteAccess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte("cfg"), 0o600))

	p, err := syscall.UTF16PtrFromString(cfgPath)
	require.NoError(t, err)
	const deleteAccess = 0x00010000 // DELETE
	held, err := syscall.CreateFile(p, syscall.GENERIC_READ|deleteAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.CloseHandle(held) })

	if f, oerr := os.Open(cfgPath); oerr == nil {
		_ = f.Close()
		t.Log("os.Open succeeded here; the sharing violation it can hit is not reproduced on this runner")
	}

	_, err = identityOfPathErr(cfgPath)
	require.NoError(t, err, "identity capture shares DELETE")
	require.NoError(t, NoteSupersededProxyFile(cfgPath))
}
