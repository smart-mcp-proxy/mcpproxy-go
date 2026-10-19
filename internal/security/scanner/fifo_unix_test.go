//go:build !windows

package scanner

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func makeTestFIFO(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, syscall.Mkfifo(path, 0o644))
}
