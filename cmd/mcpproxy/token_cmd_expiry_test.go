package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A token that never expires carries Go's zero time on the wire; the CLI shows
// "never", not a date in year 1. Real dates keep their layout, and a value that
// is not a timestamp is shown as is.
func TestFormatTokenExpiry(t *testing.T) {
	assert.Equal(t, "never", formatTokenExpiry("0001-01-01T00:00:00Z", "2006-01-02 15:04"))
	assert.Equal(t, "never", formatTokenExpiry("0001-01-01T00:00:00Z", ""))
	assert.Equal(t, "2026-12-01 10:30", formatTokenExpiry("2026-12-01T10:30:00Z", "2006-01-02 15:04"))
	assert.Equal(t, "2026-12-01T10:30:00Z", formatTokenExpiry("2026-12-01T10:30:00Z", ""))
	assert.Equal(t, "", formatTokenExpiry("", "2006-01-02"))
	assert.Equal(t, "soon", formatTokenExpiry("soon", "2006-01-02"))
}
