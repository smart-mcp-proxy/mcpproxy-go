package secret

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSecretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestReadFile_TrailingNewline(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		{"none", "abc", "abc"},
		{"LF", "abc\n", "abc"},
		{"CRLF", "abc\r\n", "abc"},
		{"only one LF is removed", "abc\n\n", "abc\n"},
		{"only one CRLF is removed", "abc\r\n\r\n", "abc\r\n"},
		{"a lone CR is kept", "abc\r", "abc\r"},
		{"leading and inner whitespace are kept", "  a b\t\n", "  a b\t"},
		{"empty", "", ""},
		{"newline only", "\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadFile(writeSecretFile(t, tc.content))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestReadFile_SizeCap(t *testing.T) {
	atCap := strings.Repeat("a", MaxSecretFileBytes)
	got, err := ReadFile(writeSecretFile(t, atCap))
	require.NoError(t, err)
	assert.Len(t, got, MaxSecretFileBytes)

	_, err = ReadFile(writeSecretFile(t, atCap+"a"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")

	// A trailing newline does not count toward a file that is exactly at the
	// cap once stripped, but the raw file is one byte over: it is refused,
	// because the cap is on what is read.
	_, err = ReadFile(writeSecretFile(t, atCap+"\n"))
	require.Error(t, err)
}

func TestReadFile_Refusals(t *testing.T) {
	dir := t.TempDir()

	t.Run("empty path", func(t *testing.T) {
		_, err := ReadFile("")
		require.Error(t, err)
	})
	t.Run("relative path", func(t *testing.T) {
		_, err := ReadFile("secrets/key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be absolute")
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := ReadFile(filepath.Join(dir, "nope"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot read secret file")
	})
	t.Run("directory", func(t *testing.T) {
		_, err := ReadFile(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	})
	t.Run("NUL byte", func(t *testing.T) {
		_, err := ReadFile(writeSecretFile(t, "ab\x00cd"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NUL")
	})
	t.Run("unreadable file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("file permission bits are not enforced on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root can read any file")
		}
		path := writeSecretFile(t, "abc")
		require.NoError(t, os.Chmod(path, 0o000))
		_, err := ReadFile(path)
		require.Error(t, err)
	})
}

func TestReadFile_FollowsSymlinkToRegularFile(t *testing.T) {
	// Kubernetes secret volumes expose each key as a symlink into ..data/.
	target := writeSecretFile(t, "from-target\n")
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(target, link))

	got, err := ReadFile(link)
	require.NoError(t, err)
	assert.Equal(t, "from-target", got)
}

func TestReadFile_ErrorsNeverContainTheContent(t *testing.T) {
	const sentinel = "SENTINEL-kq81ZtYw"
	for name, content := range map[string]string{
		"nul":     sentinel + "\x00",
		"too big": sentinel + strings.Repeat("a", MaxSecretFileBytes),
	} {
		_, err := ReadFile(writeSecretFile(t, content))
		require.Error(t, err, name)
		assert.NotContains(t, err.Error(), sentinel, name)
	}
}
