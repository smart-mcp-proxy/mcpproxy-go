package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A token that never expires carries Go's zero time on the wire; the CLI shows
// "never", not a date in year 1. Real dates keep their layout, and a value that
// is not a timestamp is shown as is.
func TestFormatTokenExpiry(t *testing.T) {
	assert.Equal(t, "never", formatTokenExpiry("0001-01-01T00:00:00Z", "2006-01-02 15:04", "agent"))
	assert.Equal(t, "never", formatTokenExpiry("0001-01-01T00:00:00Z", "", "agent"))
	assert.Equal(t, "2026-12-01 10:30", formatTokenExpiry("2026-12-01T10:30:00Z", "2006-01-02 15:04", "agent"))
	assert.Equal(t, "2026-12-01T10:30:00Z", formatTokenExpiry("2026-12-01T10:30:00Z", "", "agent"))
	assert.Equal(t, "", formatTokenExpiry("", "2006-01-02", "agent"))
	assert.Equal(t, "soon", formatTokenExpiry("soon", "2006-01-02", "agent"))
	// A client credential must carry an expiry: a zero one is invalid, not "never".
	assert.Equal(t, "invalid (no expiry)", formatTokenExpiry("0001-01-01T00:00:00Z", "", "client"))
	assert.Equal(t, "never", formatTokenExpiry("0001-01-01T00:00:00Z", "", ""))
}
