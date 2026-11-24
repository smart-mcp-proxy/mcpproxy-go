package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cross-review R2 (PR #1884): when the identity of the config being replaced
// cannot be captured, the save warns (file name only) instead of going quiet.
func TestNoteSupersededConfig_WarnsOnCaptureFailure(t *testing.T) {
	type warning struct{ msg, key, value string }
	var got []warning
	SetLoadWarningRecorder(func(m, k, v string) { got = append(got, warning{m, k, v}) })
	t.Cleanup(func() { SetLoadWarningRecorder(nil) })

	dir := t.TempDir()
	noteSupersededConfig(filepath.Join(dir, "missing.json")) // first save: silent
	assert.Empty(t, got)

	blocked := filepath.Join(dir, "cfg-dir")
	require.NoError(t, os.Mkdir(blocked, 0o700)) // no file identity to capture
	noteSupersededConfig(blocked)
	require.Len(t, got, 1)
	assert.Equal(t, MsgSupersededConfigNotRecorded, got[0].msg)
	assert.Equal(t, "cfg-dir", got[0].value, "file name only, never the full path or contents")
}

// A standalone CLI save installs no recorder; the warning must still reach the
// operator on stderr.
func TestNoteSupersededConfig_PrintsToStderrWithoutRecorder(t *testing.T) {
	SetLoadWarningRecorder(nil)
	var buf bytes.Buffer
	prev := loadWarningStderr
	loadWarningStderr = &buf
	t.Cleanup(func() { loadWarningStderr = prev; DrainLoadWarnings() })

	blocked := filepath.Join(t.TempDir(), "cfg-dir")
	require.NoError(t, os.Mkdir(blocked, 0o700))
	noteSupersededConfig(blocked)
	assert.Contains(t, buf.String(), MsgSupersededConfigNotRecorded)
	assert.Contains(t, buf.String(), "cfg-dir")
	assert.NotContains(t, buf.String(), filepath.Dir(blocked), "file name only")
}
