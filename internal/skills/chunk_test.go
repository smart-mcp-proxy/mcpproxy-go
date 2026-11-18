package skills

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunk(t *testing.T) {
	const e = "é"   // 2 bytes
	const cjk = "世" // 3 bytes
	tests := []struct {
		name    string
		data    string
		offset  int64
		limit   int64
		want    string
		next    int64
		more    bool
		wantErr error
	}{
		{"empty file", "", 0, 10, "", 0, false, nil},
		{"whole file", "hello", 0, 100, "hello", 5, false, nil},
		{"exact end", "hello", 0, 5, "hello", 5, false, nil},
		{"ascii window", "hello world", 0, 5, "hello", 5, true, nil},
		{"second window", "hello world", 5, 100, " world", 11, false, nil},
		{"limit cuts a character, shortened", "a" + e + "b", 0, 2, "a", 1, true, nil},
		{"multi-byte at the end boundary", "a" + cjk, 0, 3, "a", 1, true, nil},
		{"multi-byte fits exactly", "a" + cjk + "z", 0, 4, "a" + cjk, 4, true, nil},
		{"limit smaller than one character still progresses", cjk + "z", 0, 1, cjk, 3, true, nil},
		{"limit one inside two-byte start", e + e, 0, 1, e, 2, true, nil},
		{"offset past the end", "abc", 4, 10, "", 0, false, ErrChunkOffsetPastEnd},
		{"offset at the end", "abc", 3, 10, "", 3, false, nil},
		{"offset in the middle of a character", cjk, 1, 10, "", 0, false, ErrChunkOffsetInsideRune},
		{"offset on a character start after multi-byte", cjk + "z", 3, 10, "z", 4, false, nil},
		{"negative offset", "abc", -1, 10, "", 0, false, ErrChunkOffsetPastEnd},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, next, more, err := Chunk([]byte(tc.data), tc.offset, tc.limit)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, string(text))
			require.Equal(t, tc.next, next)
			require.Equal(t, tc.more, more)
		})
	}
}

func TestChunkWalksAFileWithoutSplittingCharacters(t *testing.T) {
	data := []byte("aé世😀b" + "é世😀")
	var got []byte
	off := int64(0)
	for i := 0; i < 100; i++ {
		text, next, more, err := Chunk(data, off, 1)
		require.NoError(t, err)
		require.NotEmpty(t, text)
		require.True(t, validUTF8(text))
		got = append(got, text...)
		off = next
		if !more {
			break
		}
	}
	require.Equal(t, string(data), string(got))
}

func validUTF8(b []byte) bool {
	for _, r := range string(b) {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
