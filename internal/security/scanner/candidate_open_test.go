package scanner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// requireCandidateErr asserts err is a *candidateReadError with the reason and
// returns it for further checks.
func requireCandidateErr(t *testing.T, err error, reason string) *candidateReadError {
	t.Helper()
	var ce *candidateReadError
	require.Error(t, err)
	require.True(t, errors.As(err, &ce), "want *candidateReadError, got %T: %v", err, err)
	require.Equal(t, reason, ce.Reason, "detail: %s", ce.Detail)
	return ce
}

func writeCandidate(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, data, 0o644))
	return p
}

func TestReadCandidateFile_RegularFile(t *testing.T) {
	p := writeCandidate(t, "bundle.json", []byte(`{"ok":true}`))
	got, err := readCandidateFile(context.Background(), p, 1024)
	require.NoError(t, err)
	require.Equal(t, []byte(`{"ok":true}`), got)
}

func TestReadCandidateFile_EmptyFile(t *testing.T) {
	p := writeCandidate(t, "empty", nil)
	got, err := readCandidateFile(context.Background(), p, 16)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestReadCandidateFile_CeilingBoundary(t *testing.T) {
	const max = 64
	at := writeCandidate(t, "at", bytes.Repeat([]byte{'a'}, max))
	got, err := readCandidateFile(context.Background(), at, max)
	require.NoError(t, err, "a file of exactly max bytes must load")
	require.Len(t, got, max)

	over := writeCandidate(t, "over", bytes.Repeat([]byte{'a'}, max+1))
	_, err = readCandidateFile(context.Background(), over, max)
	ce := requireCandidateErr(t, err, RejectCeilingExceeded)
	require.False(t, ce.NotExist)
	require.Contains(t, ce.Detail, "64")
	require.Contains(t, ce.Detail, "65")
}

func TestReadCandidateFile_GrowthPastCeilingDuringRead(t *testing.T) {
	// The descriptor's size passes the fstat check, but the reader then
	// delivers more than max bytes (the file grew). The read is bounded to
	// max+1 and refused as ceiling_exceeded.
	const max = 32
	p := writeCandidate(t, "small", []byte("tiny"))
	candidateReaderWrap = func(r io.Reader) io.Reader {
		return io.MultiReader(r, bytes.NewReader(bytes.Repeat([]byte{'z'}, 4*max)))
	}
	t.Cleanup(func() { candidateReaderWrap = nil })

	_, err := readCandidateFile(context.Background(), p, max)
	requireCandidateErr(t, err, RejectCeilingExceeded)
}

func TestReadCandidateFile_MissingFile(t *testing.T) {
	_, err := readCandidateFile(context.Background(), filepath.Join(t.TempDir(), "nope"), 16)
	ce := requireCandidateErr(t, err, RejectReadError)
	require.True(t, ce.NotExist, "ENOENT on open is the only NotExist case")
}

func TestReadCandidateFile_DirectoryRefused(t *testing.T) {
	_, err := readCandidateFile(context.Background(), t.TempDir(), 16)
	ce := requireCandidateErr(t, err, RejectNotRegularFile)
	require.False(t, ce.NotExist)
}

func TestReadCandidateFile_NotExistOnlyForENOENT(t *testing.T) {
	// A path whose parent is a regular file fails with ENOTDIR, which is a
	// read failure, not "absent": the loader must not treat it as unsigned.
	f := writeCandidate(t, "file", []byte("x"))
	_, err := readCandidateFile(context.Background(), filepath.Join(f, "child"), 16)
	ce := requireCandidateErr(t, err, RejectReadError)
	require.False(t, ce.NotExist)
}

func TestReadCandidateFile_CanceledContext(t *testing.T) {
	p := writeCandidate(t, "b", []byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readCandidateFile(ctx, p, 16)
	requireCandidateErr(t, err, RejectReadTimeout)
}

// blockingReader blocks every Read until release is closed, standing in for a
// read parked on a stalled network filesystem.
type blockingReader struct {
	entered chan struct{}
	release chan struct{}
	inner   io.Reader
}

func newBlockingReader(inner io.Reader) *blockingReader {
	return &blockingReader{entered: make(chan struct{}, 1), release: make(chan struct{}), inner: inner}
}

func (b *blockingReader) Read(p []byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return b.inner.Read(p)
}

func TestReadCandidateFile_ReadTimeoutAndParkedReaderCap(t *testing.T) {
	p := writeCandidate(t, "slow", []byte("payload"))

	brCh := make(chan *blockingReader, 1)
	candidateReaderWrap = func(r io.Reader) io.Reader {
		br := newBlockingReader(r)
		brCh <- br
		return br
	}
	t.Cleanup(func() { candidateReaderWrap = nil })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := readCandidateFile(ctx, p, 1024)
	elapsed := time.Since(start)
	requireCandidateErr(t, err, RejectReadTimeout)
	require.Less(t, elapsed, 2*time.Second, "the budget, not the stalled read, bounds the call")
	br := <-brCh
	<-br.entered // the first reader really is parked inside Read

	// Second call on the same path: refused immediately, no new open.
	var opens atomic.Int32
	candidateOpenHook = func(string) { opens.Add(1) }
	t.Cleanup(func() { candidateOpenHook = nil })
	start = time.Now()
	_, err = readCandidateFile(context.Background(), p, 1024)
	ce := requireCandidateErr(t, err, RejectReadTimeout)
	require.Less(t, time.Since(start), 500*time.Millisecond)
	require.Contains(t, ce.Detail, "previous read still blocked")
	require.Zero(t, opens.Load(), "a parked path must not be opened again")

	// A different path is unaffected by the parked one.
	other := writeCandidate(t, "other", []byte("fine"))
	candidateReaderWrap = nil
	got, err := readCandidateFile(context.Background(), other, 1024)
	require.NoError(t, err)
	require.Equal(t, []byte("fine"), got)

	// Releasing the parked reader frees the path.
	close(br.release)
	require.Eventually(t, func() bool {
		candidateParked.mu.Lock()
		defer candidateParked.mu.Unlock()
		return len(candidateParked.paths) == 0
	}, 2*time.Second, 5*time.Millisecond)
	got, err = readCandidateFile(context.Background(), p, 1024)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), got)
}
