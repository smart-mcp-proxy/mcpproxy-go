package secret

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The noted identity must keep naming the replaced file after a new file is
// renamed over its path (on every platform; on Windows a retained os.Stat
// FileInfo would resolve to the new file instead).
func TestNoteSupersededProxyFile_KeepsReplacedIdentity(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"api_key":"old"}`), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Link(cfgPath, link))

	NoteSupersededProxyFile(cfgPath)
	tmp := cfgPath + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(`{"api_key":"new"}`), 0o600))
	require.NoError(t, os.Rename(tmp, cfgPath))

	matches := func(path string) bool {
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()
		info, err := f.Stat()
		require.NoError(t, err)
		return matchesSupersededProxyFile(f, info)
	}
	require.True(t, matches(link), "the replaced inode is remembered")
	require.False(t, matches(cfgPath), "the new config is not the superseded one")
}

// A missing file is the first save (nothing to record, no error); any other
// failure to capture the identity is reported so the caller can warn.
func TestNoteSupersededProxyFile_ReportsCaptureFailure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, NoteSupersededProxyFile(filepath.Join(dir, "missing.json")))
	require.NoError(t, NoteSupersededProxyFile(""))

	err := NoteSupersededProxyFile(dir) // a directory: no file identity to record
	require.Error(t, err)
	require.NotContains(t, err.Error(), "api_key")
}
