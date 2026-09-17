//go:build windows

package codescripts

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// writeUTF16Content fills filePath (a buffer of filePathSize UTF-16 units, as
// GetFinalPathNameByHandle receives it) with n placeholder characters,
// simulating what a real call writes into the caller's buffer on success.
func writeUTF16Content(filePath *uint16, filePathSize, n uint32) {
	if n == 0 {
		return
	}
	out := unsafe.Slice(filePath, filePathSize)
	for i := uint32(0); i < n && i < filePathSize; i++ {
		out[i] = 'a' + uint16(i%26)
	}
}

// TestFinalPathOfHandle_GrowingPathDoesNotPanic pins the round-15 MUST-FIX:
// a single resize-and-retry is not enough when the path GetFinalPathNameByHandle
// resolves keeps growing between calls (e.g. another process extends the
// path of a file while this delete-shareable handle stays open). Before the
// fix, a second oversized report after the one retry fell straight into
// `buf[:n]` with a buffer still sized to the FIRST report, which panics with
// a slice-bounds-out-of-range whenever the second n exceeds that stale
// length. The fix loops the resize, bounded by finalPathNameMaxAttempts, and
// only slices once a call's n actually fits the buffer it was given.
func TestFinalPathOfHandle_GrowingPathDoesNotPanic(t *testing.T) {
	const initialBufLen = 1024

	t.Run("exact-fit — first call already fits, no retry", func(t *testing.T) {
		calls := 0
		orig := getFinalPathNameByHandle
		getFinalPathNameByHandle = func(_ windows.Handle, filePath *uint16, filePathSize uint32, _ uint32) (uint32, error) {
			calls++
			const n = initialBufLen - 1
			writeUTF16Content(filePath, filePathSize, n)
			return n, nil
		}
		t.Cleanup(func() { getFinalPathNameByHandle = orig })

		got, err := finalPathOfHandle(windows.Handle(0))
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
		assert.NotEmpty(t, got)
	})

	t.Run("one retry — second call's report fits the resized buffer", func(t *testing.T) {
		calls := 0
		orig := getFinalPathNameByHandle
		getFinalPathNameByHandle = func(_ windows.Handle, filePath *uint16, filePathSize uint32, _ uint32) (uint32, error) {
			calls++
			switch calls {
			case 1:
				// Too small: reports the required size (including the
				// terminator), writes nothing usable.
				return initialBufLen + 500, nil
			case 2:
				require.Equal(t, uint32(initialBufLen+500), filePathSize,
					"retry must size the buffer to the first report")
				n := filePathSize - 1
				writeUTF16Content(filePath, filePathSize, n)
				return n, nil
			default:
				t.Fatalf("unexpected call %d", calls)
				return 0, nil
			}
		}
		t.Cleanup(func() { getFinalPathNameByHandle = orig })

		got, err := finalPathOfHandle(windows.Handle(0))
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
		assert.NotEmpty(t, got)
	})

	t.Run("forced second growth — path keeps growing past the first retry, no panic, clean error", func(t *testing.T) {
		calls := 0
		orig := getFinalPathNameByHandle
		getFinalPathNameByHandle = func(_ windows.Handle, filePath *uint16, filePathSize uint32, _ uint32) (uint32, error) {
			calls++
			// Every call reports a size larger than the buffer it was just
			// given — the pathological "path keeps growing forever" case.
			// Before the fix this second growth (on what used to be the
			// unconditional final slice) panicked; now it must instead loop
			// up to the bound and then return an error.
			return filePathSize + 100, nil
		}
		t.Cleanup(func() { getFinalPathNameByHandle = orig })

		require.NotPanics(t, func() {
			got, err := finalPathOfHandle(windows.Handle(0))
			assert.Error(t, err, "must fail closed, not return an unproven/truncated path")
			assert.Empty(t, got)
		})
		assert.Equal(t, finalPathNameMaxAttempts, calls, "must stop retrying at the bound, not loop forever")
	})

	t.Run("growth settles within the bound — succeeds on a later attempt", func(t *testing.T) {
		calls := 0
		orig := getFinalPathNameByHandle
		getFinalPathNameByHandle = func(_ windows.Handle, filePath *uint16, filePathSize uint32, _ uint32) (uint32, error) {
			calls++
			if calls < finalPathNameMaxAttempts {
				// Keeps growing by more than the last resize, forcing
				// another loop iteration, right up to (but not exceeding)
				// the bound.
				return filePathSize + 10, nil
			}
			// Settles on the last permitted attempt.
			n := filePathSize - 1
			writeUTF16Content(filePath, filePathSize, n)
			return n, nil
		}
		t.Cleanup(func() { getFinalPathNameByHandle = orig })

		got, err := finalPathOfHandle(windows.Handle(0))
		require.NoError(t, err)
		assert.Equal(t, finalPathNameMaxAttempts, calls)
		assert.NotEmpty(t, got)
	})
}

// TestFinalPathOfHandle_RetriesAtBufferSizeBoundary pins the round-14 fix:
// GetFinalPathNameByHandle's returned size (n) INCLUDES the null terminator
// when the initial 1024-unit buffer was too small, so a path whose resolved
// length makes the first call report n == len(buf) (not just n > len(buf))
// must also retry — a buffer that fits exactly leaves no room for that
// terminator. Before the fix, that boundary case fell through the `n >
// len(buf)` check and returned a truncated/unspecified result instead of
// retrying at the reported size.
func TestFinalPathOfHandle_RetriesAtBufferSizeBoundary(t *testing.T) {
	const initialBufLen = 1024 // mirrors finalPathOfHandle's fixed initial buffer size

	cases := []struct {
		name        string
		firstN      uint32 // what the first GetFinalPathNameByHandle call reports
		expectCalls int
	}{
		{"one under the initial buffer size — succeeds on the first call", initialBufLen - 1, 1},
		{"exactly the initial buffer size — must retry (the fixed off-by-one)", initialBufLen, 2},
		{"one over the initial buffer size — already retried before the fix", initialBufLen + 1, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			orig := getFinalPathNameByHandle
			getFinalPathNameByHandle = func(_ windows.Handle, filePath *uint16, filePathSize uint32, _ uint32) (uint32, error) {
				calls++
				if calls == 1 {
					if tc.firstN < initialBufLen {
						// Succeeds on the first try: the buffer already held
						// the whole string, so the real call would have
						// written it and returned the string's length
						// (excluding the terminator).
						writeUTF16Content(filePath, filePathSize, tc.firstN)
						return tc.firstN, nil
					}
					// Too small: Win32 reports the required size, including
					// the terminator, and writes nothing usable.
					return tc.firstN, nil
				}
				// Retry: finalPathOfHandle must size the new buffer to
				// exactly what the first call reported.
				require.Equal(t, tc.firstN, filePathSize, "retry must size the buffer to the reported n")
				content := tc.firstN - 1 // the retry buffer has room for the terminator too
				writeUTF16Content(filePath, filePathSize, content)
				return content, nil
			}
			t.Cleanup(func() { getFinalPathNameByHandle = orig })

			got, err := finalPathOfHandle(windows.Handle(0))
			require.NoError(t, err)
			assert.Equal(t, tc.expectCalls, calls, "unexpected number of GetFinalPathNameByHandle calls")
			assert.NotEmpty(t, got, "must have read back the resolved path")
		})
	}
}
