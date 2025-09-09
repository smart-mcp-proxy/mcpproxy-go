package core

import (
	"regexp"
	"strings"
	"testing"
)

func TestSanitizeServerNameForContainer(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple name",
			input:    "github-server",
			expected: "github-server",
		},
		{
			name:     "name with spaces",
			input:    "my server name",
			expected: "my-server-name",
		},
		{
			name:     "name with special characters",
			input:    "server@#$%^&*()",
			expected: "server",
		},
		{
			name:     "name starting with invalid character",
			input:    "-invalid-start",
			expected: "server-invalid-start",
		},
		{
			name:     "name with dots",
			input:    "server.name.test",
			expected: "server.name.test",
		},
		{
			name:     "name with underscores",
			input:    "server_name_test",
			expected: "server_name_test",
		},
		{
			name:     "empty name",
			input:    "",
			expected: "server",
		},
		{
			name:     "name with multiple consecutive special chars",
			input:    "server!!!@@@name",
			expected: "server-name",
		},
		{
			name:     "name ending with hyphens and dots",
			input:    "server-name-..",
			expected: "server-name",
		},
		{
			name:     "very long name",
			input:    strings.Repeat("a", 250),
			expected: strings.Repeat("a", 200),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeServerNameForContainer(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeServerNameForContainer(%q) = %q, want %q", tt.input, result, tt.expected)
			}

			// Verify the result is a valid Docker container name
			if result != "" {
				if !isValidDockerContainerNamePart(result) {
					t.Errorf("sanitizeServerNameForContainer(%q) = %q, which is not a valid Docker container name part", tt.input, result)
				}
			}
		})
	}
}

func TestGenerateRandomSuffix(t *testing.T) {
	// Test that the suffix has the correct length
	suffix := generateRandomSuffix()
	if len(suffix) != 4 {
		t.Errorf("generateRandomSuffix() returned length %d, want 4", len(suffix))
	}

	// Test that the suffix contains only alphanumeric characters
	validPattern := regexp.MustCompile(`^[a-z0-9]+$`)
	if !validPattern.MatchString(suffix) {
		t.Errorf("generateRandomSuffix() = %q, contains invalid characters", suffix)
	}

	// Test that multiple calls return different values (probabilistically)
	suffixes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		suffix := generateRandomSuffix()
		suffixes[suffix] = true
	}

	// We should have a reasonable number of unique suffixes
	if len(suffixes) < 50 {
		t.Errorf("generateRandomSuffix() generated only %d unique suffixes in 100 calls, expected more variety", len(suffixes))
	}
}

func TestGenerateContainerName(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
	}{
		{
			name:       "simple server name",
			serverName: "github-server",
		},
		{
			name:       "server name with spaces",
			serverName: "my test server",
		},
		{
			name:       "server name with special characters",
			serverName: "server@#$name",
		},
		{
			name:       "empty server name",
			serverName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerName := generateContainerName(tt.serverName)

			// Check that it starts with the expected prefix
			if !strings.HasPrefix(containerName, "mcpproxy-") {
				t.Errorf("generateContainerName(%q) = %q, should start with 'mcpproxy-'", tt.serverName, containerName)
			}

			// Check that it ends with a 4-character suffix
			parts := strings.Split(containerName, "-")
			if len(parts) < 3 {
				t.Errorf("generateContainerName(%q) = %q, should have at least 3 parts separated by hyphens", tt.serverName, containerName)
			} else {
				suffix := parts[len(parts)-1]
				if len(suffix) != 4 {
					t.Errorf("generateContainerName(%q) = %q, suffix should be 4 characters long, got %d", tt.serverName, containerName, len(suffix))
				}
			}

			// Check that the result is a valid Docker container name
			if !isValidDockerContainerName(containerName) {
				t.Errorf("generateContainerName(%q) = %q, which is not a valid Docker container name", tt.serverName, containerName)
			}

			// Test that multiple calls with the same server name produce different container names
			containerName2 := generateContainerName(tt.serverName)
			if containerName == containerName2 {
				t.Errorf("generateContainerName(%q) produced the same result twice: %q", tt.serverName, containerName)
			}
		})
	}
}

// isValidDockerContainerName checks if a string is a valid Docker container name
func isValidDockerContainerName(name string) bool {
	// Docker container names must:
	// - be 1-253 characters long
	// - contain only [a-zA-Z0-9][a-zA-Z0-9_.-]*
	// - start with alphanumeric character
	if name == "" || len(name) > 253 {
		return false
	}

	// Check first character is alphanumeric
	if !regexp.MustCompile(`^[a-zA-Z0-9]`).MatchString(name) {
		return false
	}

	// Check all characters are valid
	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	return validPattern.MatchString(name)
}

// isValidDockerContainerNamePart checks if a string is a valid part of a Docker container name
func isValidDockerContainerNamePart(name string) bool {
	if name == "" {
		return false
	}

	// Should only contain valid characters
	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	if !validPattern.MatchString(name) {
		return false
	}

	// Should start with alphanumeric character
	if !regexp.MustCompile(`^[a-zA-Z0-9]`).MatchString(name) {
		return false
	}

	// Should not end with hyphen or dot
	if strings.HasSuffix(name, "-") || strings.HasSuffix(name, ".") {
		return false
	}

	return true
}
