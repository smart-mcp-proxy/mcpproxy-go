package skills

import (
	"errors"
	"unicode/utf8"
)

var (
	// ErrChunkOffsetPastEnd: the offset is negative or beyond the end of the data.
	ErrChunkOffsetPastEnd = errors.New("offset is past the end of the file")
	// ErrChunkOffsetInsideRune: the offset points into the middle of a UTF-8 character.
	ErrChunkOffsetInsideRune = errors.New("offset is inside a UTF-8 character")
)

// Chunk cuts at most limit bytes of data starting at byte offset, ending on a
// UTF-8 character boundary, so the answer can be a few bytes shorter than
// limit. A limit smaller than one character still returns that whole
// character, so a reader always makes progress. next is the offset to ask for
// next (the end of the chunk) and more reports whether data remains after it.
//
// An offset equal to len(data) is valid and returns an empty chunk. An offset
// beyond the end or inside a character is refused with ErrChunkOffsetPastEnd
// or ErrChunkOffsetInsideRune. limit must be positive; callers validate it.
func Chunk(data []byte, offset, limit int64) (text []byte, next int64, more bool, err error) {
	size := int64(len(data))
	if offset < 0 || offset > size {
		return nil, 0, false, ErrChunkOffsetPastEnd
	}
	if offset < size && !utf8.RuneStart(data[offset]) {
		return nil, 0, false, ErrChunkOffsetInsideRune
	}
	end := offset + limit
	if end < offset || end >= size { // second clause: overflow guard
		end = size
	} else {
		// Shorten the chunk so it ends on a character boundary.
		for end > offset && !utf8.RuneStart(data[end]) {
			end--
		}
		if end == offset {
			end = offset + 1
			for end < size && !utf8.RuneStart(data[end]) {
				end++
			}
		}
	}
	return data[offset:end], end, end < size, nil
}
