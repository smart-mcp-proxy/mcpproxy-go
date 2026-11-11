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
	require.Error(t, rejectPlatformAbsolutePath(`C:relative\key`))
}
