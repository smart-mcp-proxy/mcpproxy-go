package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"sync"
)

// candidateReadError is the classified failure of readCandidateFile. Reason is
// one of RejectNotRegularFile, RejectReadTimeout, RejectReadError or
// RejectCeilingExceeded (tpa_bundle_reasons.go); the loader branches on it and
// copies Detail into BundleInfo.LastRejection.Detail.
//
// NotExist is true ONLY when the open itself failed because the file is absent.
// The loader treats an absent sidecar as "unsigned" (tasks.md A7) but every
// other failure -- including a present-but-unreadable sidecar -- as a
// rejection, so the distinction must not be inferred from Reason alone.
type candidateReadError struct {
	Reason   string
	Detail   string
	NotExist bool
	Err      error
}

func (e *candidateReadError) Error() string {
	if e.Detail == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Detail
}

func (e *candidateReadError) Unwrap() error { return e.Err }

// Test-only seams. Production code leaves both nil.
var (
	// candidateOpenHook, when non-nil, is called with the path immediately
	// before the open syscall (after path resolution), so a test can swap the
	// path for a FIFO or symlink in the check-to-open window (SC-011a).
	candidateOpenHook func(path string)
	// candidateReaderWrap, when non-nil, wraps the bounded reader of the open
	// descriptor, so a test can inject a reader that blocks like a stalled
	// network filesystem.
	candidateReaderWrap func(io.Reader) io.Reader
)

// candidateParked records the paths that still have an abandoned reader
// goroutine parked in a blocked read. A path stays in the set until that
// goroutine's read returns, and while it is there readCandidateFile refuses to
// open the path again: on a stalled network filesystem every refresh would
// otherwise strand one more goroutine and file descriptor.
var candidateParked = struct {
	mu    sync.Mutex
	paths map[string]struct{}
}{paths: map[string]struct{}{}}

// candidateResult is what the reader goroutine hands back.
type candidateResult struct {
	data []byte
	err  error
}

// readCandidateFile reads a TPA bundle candidate (or its sidecar) of at most
// limit bytes from path, descriptor-first and time-bounded (FR-011, SC-011,
// research D5).
//
// Why descriptor-first: every property is decided on the descriptor the open
// returned, never on the path. A path check (Lstat) followed by an open is a
// window in which an attacker-controlled directory can swap a regular file for
// a FIFO, which would park the caller forever, or for a symlink to a file the
// process should not read. The platform open refuses a symlink at the final
// component atomically and does not block on a FIFO; the shared code then
// fstat's the descriptor, refuses anything that is not a regular file, checks
// the descriptor's size against limit, and reads at most limit+1 bytes from
// that same descriptor (a file that grew after the fstat is still bounded). A
// file of exactly limit bytes loads.
//
// Why a goroutine and not a read deadline: SetReadDeadline does not apply to
// regular files, and the stall this guards against is exactly a regular file
// on a hung network mount. The read therefore runs in a goroutine and the
// caller waits on ctx; when ctx expires first the caller returns
// RejectReadTimeout immediately and the goroutine, which owns the descriptor,
// closes it whenever its read finally returns. While such an abandoned reader
// is parked the path is refused fast (see candidateParked).
func readCandidateFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, &candidateReadError{Reason: RejectReadTimeout, Detail: "read budget expired before open: " + err.Error(), Err: err}
	}

	candidateParked.mu.Lock()
	if _, parked := candidateParked.paths[path]; parked {
		candidateParked.mu.Unlock()
		return nil, &candidateReadError{Reason: RejectReadTimeout, Detail: "previous read still blocked"}
	}
	candidateParked.mu.Unlock()

	// Capture the seams here so the goroutine never reads package state a test
	// may reset after this call has returned.
	hook, wrap := candidateOpenHook, candidateReaderWrap

	var (
		mu        sync.Mutex
		done      bool
		abandoned bool
	)
	resCh := make(chan candidateResult, 1)
	go func() {
		data, err := loadCandidate(path, limit, hook, wrap)
		mu.Lock()
		done = true
		wasAbandoned := abandoned
		mu.Unlock()
		if wasAbandoned {
			candidateParked.mu.Lock()
			delete(candidateParked.paths, path)
			candidateParked.mu.Unlock()
			return
		}
		resCh <- candidateResult{data: data, err: err}
	}()

	select {
	case r := <-resCh:
		return r.data, r.err
	case <-ctx.Done():
		mu.Lock()
		if done {
			// The goroutine finished just as the budget expired; its result is
			// in the channel (it does not send when abandoned, but it was not).
			mu.Unlock()
			r := <-resCh
			return r.data, r.err
		}
		abandoned = true
		candidateParked.mu.Lock()
		candidateParked.paths[path] = struct{}{}
		candidateParked.mu.Unlock()
		mu.Unlock()
		return nil, &candidateReadError{Reason: RejectReadTimeout, Detail: fmt.Sprintf("read budget expired: %v", ctx.Err()), Err: ctx.Err()}
	}
}

// loadCandidate is the body of the reader goroutine: open, classify the
// descriptor, size-check it, read it bounded. It owns and always closes the
// descriptor.
func loadCandidate(path string, limit int64, hook func(string), wrap func(io.Reader) io.Reader) ([]byte, error) {
	if hook != nil {
		hook(path)
	}
	f, size, err := openCandidateDescriptor(path)
	if err != nil {
		var ce *candidateReadError
		if errors.As(err, &ce) {
			return nil, ce
		}
		return nil, &candidateReadError{
			Reason:   RejectReadError,
			Detail:   "open: " + err.Error(),
			NotExist: errors.Is(err, fs.ErrNotExist),
			Err:      err,
		}
	}
	defer f.Close()

	if size > limit {
		return nil, &candidateReadError{Reason: RejectCeilingExceeded, Detail: fmt.Sprintf("limit %d bytes, observed %d bytes", limit, size)}
	}

	// Read at most limit+1 bytes from the SAME descriptor: one extra byte is
	// enough to tell "grew past the limit" from "exactly at the limit".
	readMax := limit
	if readMax < math.MaxInt64 {
		readMax++
	}
	r := io.LimitReader(f, readMax)
	if wrap != nil {
		r = wrap(r)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, &candidateReadError{Reason: RejectReadError, Detail: "read: " + err.Error(), Err: err}
	}
	if int64(len(data)) > limit {
		return nil, &candidateReadError{Reason: RejectCeilingExceeded, Detail: fmt.Sprintf("limit %d bytes, observed more than %d bytes while reading", limit, limit)}
	}
	return data, nil
}

// notRegularError builds the refusal for a candidate that is not a plain file.
func notRegularError(what string) *candidateReadError {
	return &candidateReadError{Reason: RejectNotRegularFile, Detail: what}
}

// Compile-time guard that the platform files all provide the same signature.
var _ func(string) (*os.File, int64, error) = openCandidateDescriptor
