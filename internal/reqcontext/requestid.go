package reqcontext

import (
	"regexp"

	"github.com/google/uuid"
)

const (
	// RequestIDHeader is the HTTP header name for request IDs
	RequestIDHeader = "X-Request-Id"

	// MaxRequestIDLength is the maximum allowed length for a request ID
	MaxRequestIDLength = 256
)

// requestIDPattern validates request ID format: alphanumeric, dashes, underscores
var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,256}$`)

// IsValidRequestID checks if a request ID matches the allowed pattern.
// Valid IDs contain only alphanumeric characters, dashes, and underscores,
// and are between 1 and 256 characters long.
func IsValidRequestID(id string) bool {
	if id == "" {
		return false
	}
	if len(id) > MaxRequestIDLength {
		return false
	}
	return requestIDPattern.MatchString(id)
}

// GenerateRequestID generates a new UUID v4 request ID
func GenerateRequestID() string {
	return uuid.New().String()
}

// GetOrGenerateRequestID returns the provided ID if valid, otherwise generates a new one.
// This is the main entry point for request ID handling in middleware.
func GetOrGenerateRequestID(providedID string) string {
	if IsValidRequestID(providedID) {
		return providedID
	}
	return GenerateRequestID()
}
