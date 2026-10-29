//go:build unix

package secret

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A FIFO with no writer makes open(2) block forever. ReadFile must refuse it
// from the path's type before opening, so a mis-pointed MCPPROXY_API_KEY_FILE
// or ${file:} reference fails fast instead of hanging startup.
func TestReadFile_RefusesFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "secret.fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	type result struct {
		val string
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := ReadFile(fifo)
		done <- result{v, err}
	}()

	select {
	case r := <-done:
		require.Error(t, r.err)
		require.Contains(t, r.err.Error(), "not a regular file")
		require.Empty(t, r.val)
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFile blocked on a FIFO instead of refusing it")
	}
}
