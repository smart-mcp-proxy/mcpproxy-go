package reqcontext

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIsValidRequestID(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		// Valid cases
		{"UUID format", "a1b2c3d4-e5f6-7890-abcd-ef1234567890", true},
		{"Simple alphanumeric", "abc123", true},
		{"With dashes", "request-123-abc", true},
		{"With underscores", "request_123_abc", true},
		{"Mixed case", "Request-ID-123", true},
		{"Single character", "x", true},
		{"Max length (256)", strings.Repeat("a", 256), true},

		// Invalid cases
		{"Empty string", "", false},
		{"Too long (257)", strings.Repeat("a", 257), false},
		{"Contains space", "request 123", false},
		{"Contains special chars", "request@123", false},
		{"Contains angle brackets", "<script>", false},
		{"Contains slash", "path/to/resource", false},
		{"Contains dot", "file.txt", false},
		{"Contains colon", "time:12:30", false},
		{"Unicode characters", "reqest-\u00e9", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidRequestID(tt.id)
			if got != tt.valid {
				t.Errorf("IsValidRequestID(%q) = %v, want %v", tt.id, got, tt.valid)
			}
		})
	}
}

func TestGenerateRequestID(t *testing.T) {
	id := GenerateRequestID()

	// Should be a valid UUID
	_, err := uuid.Parse(id)
	if err != nil {
		t.Errorf("GenerateRequestID() returned invalid UUID: %v", err)
	}

	// Should be valid according to our validation
	if !IsValidRequestID(id) {
		t.Errorf("GenerateRequestID() returned ID that fails validation: %s", id)
	}

	// Should generate unique IDs
	id2 := GenerateRequestID()
	if id == id2 {
		t.Error("GenerateRequestID() returned same ID twice")
	}
}

func TestGetOrGenerateRequestID(t *testing.T) {
	tests := []struct {
		name       string
		providedID string
		wantSame   bool // true if should return providedID, false if should generate new
	}{
		{"Valid ID returned as-is", "my-request-123", true},
		{"Valid UUID returned as-is", "a1b2c3d4-e5f6-7890-abcd-ef1234567890", true},
		{"Empty generates new", "", false},
		{"Invalid generates new", "invalid spaces", false},
		{"Too long generates new", strings.Repeat("a", 300), false},
		{"Special chars generates new", "<script>alert(1)</script>", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetOrGenerateRequestID(tt.providedID)

			if tt.wantSame {
				if got != tt.providedID {
					t.Errorf("GetOrGenerateRequestID(%q) = %q, want %q", tt.providedID, got, tt.providedID)
				}
			} else {
				// Should have generated a new valid ID
				if !IsValidRequestID(got) {
					t.Errorf("GetOrGenerateRequestID(%q) returned invalid ID: %s", tt.providedID, got)
				}
				// Should be different from input (unless input was empty)
				if got == tt.providedID && tt.providedID != "" {
					t.Errorf("GetOrGenerateRequestID(%q) should have generated new ID", tt.providedID)
				}
			}
		})
	}
}

func BenchmarkIsValidRequestID(b *testing.B) {
	id := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	for i := 0; i < b.N; i++ {
		IsValidRequestID(id)
	}
}

func BenchmarkGenerateRequestID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateRequestID()
	}
}
