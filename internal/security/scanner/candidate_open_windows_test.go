//go:build windows

package scanner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Windows has no filesystem FIFOs, and creating a symlink needs a privilege
// CI runners do not always hold, so the FIFO and symlink behaviours are
// unix-only tests. The shared tests already cover directory, missing-file and
// ceiling behaviour on Windows; this pins that the CreateFile-based open path
// reads a regular file.
func TestReadCandidateFile_WindowsRegularFile(t *testing.T) {
	p := writeCandidate(t, "bundle.json", []byte("win"))
	got, err := readCandidateFile(context.Background(), p, 16)
	require.NoError(t, err)
	require.Equal(t, []byte("win"), got)
}
