package storage

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// truncationSuffix is what truncateResponse appends. Named here so the size
// assertions below state the cap contract rather than a magic string.
const truncationSuffix = "...[truncated]"

// TestTruncateResponseRuneBoundary: maxSize is a raw byte budget, and a cut at
// the raw offset splits a multi-byte character. "é" is two bytes, so a 100-rune
// string occupies bytes [0,200) with every rune starting at an even offset; a
// cut at 51 lands on the tail byte of the rune starting at 50.
func TestTruncateResponseRuneBoundary(t *testing.T) {
	const maxSize = 51
	input := strings.Repeat("é", 100)
	require.Greater(t, len(input), maxSize, "fixture must actually need truncation")

	result, truncated := truncateResponse(input, maxSize)
	require.True(t, truncated)

	body := strings.TrimSuffix(result, truncationSuffix)
	require.NotEqual(t, result, body, "the truncation marker must be present")

	assert.True(t, utf8.ValidString(result),
		"a byte-slice cut lands mid-rune and yields invalid UTF-8")
	assert.LessOrEqual(t, len(body), maxSize,
		"the retained text must stay inside the byte budget")
}

// TestTruncatedRecordSurvivesMarshalRoundTripWithinCap is the consequence the
// rune-boundary cut exists to prevent. MarshalBinary is json.Marshal, which
// does not reject invalid UTF-8 — it substitutes U+FFFD, three bytes per bad
// byte. So a record cut mid-rune comes back from storage LARGER than the cap
// that produced it, which is the opposite of what the cap is for.
func TestTruncatedRecordSurvivesMarshalRoundTripWithinCap(t *testing.T) {
	const maxSize = 51
	stored, truncated := truncateResponse(strings.Repeat("é", 100), maxSize)
	require.True(t, truncated)

	rec := &ActivityRecord{
		ID:                "01ABCDEFGHIJKLMNOPQRSTUVWX",
		Type:              ActivityTypeToolCall,
		Status:            ActivityStatusSuccess,
		Timestamp:         time.Now().UTC(),
		Response:          stored,
		ResponseTruncated: true,
	}

	encoded, err := rec.MarshalBinary()
	require.NoError(t, err)

	var round ActivityRecord
	require.NoError(t, round.UnmarshalBinary(encoded))

	assert.Equal(t, stored, round.Response,
		"the persisted text must survive the round trip byte-for-byte")
	assert.LessOrEqual(t, len(round.Response), maxSize+len(truncationSuffix),
		"a record must not exceed its own advertised cap after marshalling")
}
