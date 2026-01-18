package configimport

import "testing"

func TestValidServerName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		// Valid names
		{"github", false},
		{"my-server", false},
		{"my_server", false},
		{"server123", false},
		{"MixedCase", false},
		{"a", false},
		{"server-name_123", false},

		// Invalid names
		{"", true},                                                                // Empty
		{"server name", true},                                                     // Space
		{"server.name", true},                                                     // Dot
		{"server/name", true},                                                     // Slash
		{"server\\name", true},                                                    // Backslash
		{"server@name", true},                                                     // Special char
		{"server!name", true},                                                     // Special char
		{string(make([]byte, 65)), true},                                          // Too long (65 chars)
		{"server\tname", true},                                                    // Tab
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true}, // 65 chars
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidServerName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidServerName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}

	// Test exactly 64 chars (should be valid)
	validLongName := string(make([]byte, 64))
	for i := range validLongName {
		validLongName = validLongName[:i] + "a" + validLongName[i+1:]
	}
	longName := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 chars
	if err := ValidServerName(longName); err != nil {
		t.Errorf("ValidServerName(64 chars) should be valid, got error: %v", err)
	}
}

func TestSanitizeServerName(t *testing.T) {
	tests := []struct {
		input         string
		wantName      string
		wantSanitized bool
	}{
		// No sanitization needed
		{"github", "github", false},
		{"my-server", "my-server", false},
		{"my_server", "my_server", false},

		// Sanitization needed
		{"My Server", "My_Server", true},
		{"server.name", "server_name", true},
		{"server/name", "server_name", true},
		{"server\\name", "server_name", true},
		{"  server  ", "server", true},
		{"server@#$%name", "servername", true},
		{"server...name", "server_name", true}, // Multiple dots become one underscore
		{"", "", true},                         // Empty stays empty

		// Edge cases
		{"@#$%", "", true}, // All invalid chars
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotName, gotSanitized := SanitizeServerName(tt.input)
			if gotName != tt.wantName {
				t.Errorf("SanitizeServerName(%q) name = %q, want %q", tt.input, gotName, tt.wantName)
			}
			if gotSanitized != tt.wantSanitized {
				t.Errorf("SanitizeServerName(%q) sanitized = %v, want %v", tt.input, gotSanitized, tt.wantSanitized)
			}
		})
	}
}
