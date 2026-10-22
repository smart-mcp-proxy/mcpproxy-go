//go:build unix

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// readWithin runs readCandidateFile and fails the test if it does not return
// within d: a FIFO open must never park the caller.
func readWithin(t *testing.T, d time.Duration, path string, max int64) ([]byte, error) {
	t.Helper()
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		b, err := readCandidateFile(context.Background(), path, max)
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(d):
		t.Fatalf("readCandidateFile(%s) blocked for more than %s", path, d)
		return nil, nil
	}
}

func TestReadCandidateFile_FIFORefusedWithoutBlocking(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	makeTestFIFO(t, p)
	_, err := readWithin(t, time.Second, p, 1024)
	requireCandidateErr(t, err, RejectNotRegularFile)
}

func TestReadCandidateFile_SymlinkToFIFORefused(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	makeTestFIFO(t, fifo)
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(fifo, link))
	_, err := readWithin(t, time.Second, link, 1024)
	requireCandidateErr(t, err, RejectNotRegularFile)
}

func TestReadCandidateFile_SymlinkToRegularRefused(t *testing.T) {
	target := writeCandidate(t, "real", []byte("secret"))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(target, link))
	_, err := readCandidateFile(context.Background(), link, 1024)
	ce := requireCandidateErr(t, err, RejectNotRegularFile)
	require.False(t, ce.NotExist)
}

func TestReadCandidateFile_DanglingSymlinkRefused(t *testing.T) {
	link := filepath.Join(t.TempDir(), "dangling")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "gone"), link))
	_, err := readCandidateFile(context.Background(), link, 1024)
	requireCandidateErr(t, err, RejectNotRegularFile)
}

// SC-011a: the path is a regular file when resolved but a FIFO by the time it
// is opened. The descriptor-based check must refuse it, not block.
func TestReadCandidateFile_SwapToFIFOBetweenResolutionAndOpen(t *testing.T) {
	p := writeCandidate(t, "swap", []byte("regular"))
	candidateOpenHook = func(path string) {
		require.NoError(t, os.Remove(path))
		makeTestFIFO(t, path)
	}
	t.Cleanup(func() { candidateOpenHook = nil })

	_, err := readWithin(t, time.Second, p, 1024)
	requireCandidateErr(t, err, RejectNotRegularFile)
}

func TestReadCandidateFile_SwapToSymlinkBetweenResolutionAndOpen(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("other"), 0o644))
	p := filepath.Join(dir, "cand")
	require.NoError(t, os.WriteFile(p, []byte("regular"), 0o644))
	candidateOpenHook = func(path string) {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Symlink(target, path))
	}
	t.Cleanup(func() { candidateOpenHook = nil })

	_, err := readCandidateFile(context.Background(), p, 1024)
	requireCandidateErr(t, err, RejectNotRegularFile)
}
