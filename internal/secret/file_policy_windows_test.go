//go:build windows

package secret

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectPlatformAbsolutePath_WindowsDriveAbsolute(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "key")
	require.NoError(t, rejectPlatformAbsolutePath(abs))
	assert.NoError(t, rejectPlatformAbsolutePath(`C:\ProgramData\mcpproxy\key`))
	assert.NoError(t, rejectPlatformAbsolutePath(`C:/ProgramData/mcpproxy/key`))
	require.Error(t, rejectPlatformAbsolutePath(`C:relative\key`))
}

func TestRejectPlatformAbsolutePath_ForbidsUNCAndDevice(t *testing.T) {
	unc := []string{
		`\\server\share\secret`,
		`//server/share/secret`,
		`\//server/share/secret`,
		`/\server\share\secret`,
	}
	for _, p := range unc {
		require.Error(t, rejectPlatformAbsolutePath(p), p)
	}

	dev := []string{
		`\\?\C:\secret`,
		`\\.\CON`,
		`//?/C:/secret`,
		`//./CON`,
		`\\?/C:\secret`,
		`/\?\C:\secret`,
	}
	for _, p := range dev {
		require.Error(t, rejectPlatformAbsolutePath(p), p)
	}
}
