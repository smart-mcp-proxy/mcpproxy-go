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
