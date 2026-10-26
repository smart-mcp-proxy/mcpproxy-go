//go:build unix

package scanner

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SC-011 at the pipeline level: a FIFO planted at the bundle path is refused
// immediately, last-known-good keeps serving, and the next legitimate drop
// activates. (The open layer is covered by candidate_open_unix_test.go.)
func TestRuntime_FIFOAtTheBundlePath(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	prev := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
	chk := servingCheck()

	require.NoError(t, os.Remove(f.path))
	require.NoError(t, syscall.Mkfifo(f.path, 0o600))

	start := time.Now()
	info := f.run()
	assert.Less(t, time.Since(start), 3*time.Second, "a never-written FIFO is refused, not waited on")
	f.requireRejected(info, RejectNotRegularFile, prev.FullDigest, chk)

	require.NoError(t, os.Remove(f.path))
	info = f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "next"}))
	assert.Equal(t, "6", info.Sequence, "the next legitimate drop activates")
	require.NotNil(t, info.LastRejection, "and the refusal stays visible")
	assert.Equal(t, RejectNotRegularFile, info.LastRejection.Reason)
}

// The sidecar path gets the same treatment: a FIFO there must not be mistaken
// for "no sidecar" and so must not fall through to the unsigned path.
func TestRuntime_FIFOAtTheSidecarPathIsNotAnUnsignedDrop(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	c := unsignedCandidate(t, bundleOpts{Seq: 5})
	f.put(c)
	require.NoError(t, syscall.Mkfifo(f.path+".sig", 0o600))

	info := f.run()

	assert.Equal(t, embeddedDigest(), info.FullDigest)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectNotRegularFile, info.LastRejection.Reason)
}
