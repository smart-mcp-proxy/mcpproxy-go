package patterns

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test AWS credential detection patterns
func TestAWSAccessKeyPattern(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantMatch     bool
		wantExample   bool
		description   string
	}{
		// Valid AWS access keys
		{
			name:        "standard AKIA prefix",
			input:       "AKIAIOSFODNN7EXAMPLE",
			wantMatch:   true,
			wantExample: true, // AWS example key
			description: "Standard AWS access key with AKIA prefix",
		},
		{
			name:        "ASIA temporary key",
			input:       "ASIABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "Temporary AWS credentials from STS",
		},
		{
			name:        "ABIA access key",
			input:       "ABIABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with ABIA prefix",
		},
		{
			name:        "ACCA access key",
			input:       "ACCABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with ACCA prefix",
		},
		{
			name:        "AGPA access key",
			input:       "AGPABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with AGPA prefix (group)",
		},
		{
			name:        "AIDA access key",
			input:       "AIDABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS IAM user unique ID",
		},
		{
			name:        "AIPA access key",
			input:       "AIPABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with AIPA prefix",
		},
		{
			name:        "ANPA access key",
			input:       "ANPABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with ANPA prefix",
		},
		{
			name:        "ANVA access key",
			input:       "ANVABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with ANVA prefix",
		},
		{
			name:        "APKA access key",
			input:       "APKABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with APKA prefix",
		},
		{
			name:        "AROA access key",
			input:       "AROABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS role unique ID",
		},
		{
			name:        "ASCA access key",
			input:       "ASCABCDEFGHIJKLMNOPQ",
			wantMatch:   true,
			wantExample: false,
			description: "AWS access key with ASCA prefix",
		},

		// Invalid keys
		{
			name:        "too short",
			input:       "AKIAIOSFODN",
			wantMatch:   false,
			wantExample: false,
			description: "AWS key too short to be valid",
		},
		{
			name:        "invalid prefix",
			input:       "ABCDIOSFODNN7EXAMPLE",
			wantMatch:   false,
			wantExample: false,
			description: "Invalid AWS key prefix",
		},
		{
			name:        "lowercase not valid",
			input:       "akiaiosfodnn7example",
			wantMatch:   false,
			wantExample: false,
			description: "AWS keys must be uppercase",
		},
		{
			name:        "mixed case not valid",
			input:       "AkiaIOSFODNN7EXAMPLE",
			wantMatch:   false,
			wantExample: false,
			description: "AWS keys must be all uppercase",
		},

		// Keys in context
		{
			name:        "key in JSON",
			input:       `{"aws_access_key_id": "AKIAIOSFODNN7EXAMPLE"}`,
			wantMatch:   true,
			wantExample: true,
			description: "AWS key embedded in JSON",
		},
		{
			name:        "key in environment variable",
			input:       "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			wantMatch:   true,
			wantExample: true,
			description: "AWS key in env var format",
		},
	}

	patterns := GetCloudPatterns()
	awsKeyPattern := findPatternByName(patterns, "aws_access_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if awsKeyPattern == nil {
				t.Skip("AWS access key pattern not implemented yet")
				return
			}
			matches := awsKeyPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
				if len(matches) > 0 && tt.wantExample {
					assert.True(t, awsKeyPattern.IsKnownExample(matches[0]), "expected to be known example")
				}
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

func TestAWSSecretKeyPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatch   bool
		wantExample bool
	}{
		{
			name:        "example secret key",
			input:       "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "secret key in context",
			input:       `aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			wantMatch:   true,
			wantExample: true,
		},
		{
			name:        "random 40 char base64-like",
			input:       "abcdefghij1234567890ABCDEFGHIJ1234567890",
			wantMatch:   true,
			wantExample: false,
		},
		{
			name:        "too short",
			input:       "wJalrXUtnFEMI/K7MDENG",
			wantMatch:   false,
			wantExample: false,
		},
	}

	patterns := GetCloudPatterns()
	secretKeyPattern := findPatternByName(patterns, "aws_secret_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if secretKeyPattern == nil {
				t.Skip("AWS secret key pattern not implemented yet")
				return
			}
			matches := secretKeyPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test GCP credential detection patterns
func TestGCPAPIKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "valid GCP API key",
			input:     "AIzaSyA-abcdefghijklmnopqrstuvwxyz12345",
			wantMatch: true,
		},
		{
			name:      "GCP API key in JSON",
			input:     `{"api_key": "AIzaSyA-abcdefghijklmnopqrstuvwxyz12345"}`,
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "AIzaSyA-abc",
			wantMatch: false,
		},
		{
			name:      "wrong prefix",
			input:     "BIzaSyA-abcdefghijklmnopqrstuvwxyz12345",
			wantMatch: false,
		},
	}

	patterns := GetCloudPatterns()
	gcpKeyPattern := findPatternByName(patterns, "gcp_api_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gcpKeyPattern == nil {
				t.Skip("GCP API key pattern not implemented yet")
				return
			}
			matches := gcpKeyPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

func TestGCPServiceAccountKey(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name: "valid service account JSON",
			input: `{
				"type": "service_account",
				"project_id": "my-project",
				"private_key_id": "abc123",
				"private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIEpA..."
			}`,
			wantMatch: true,
		},
		{
			name:      "service account type field",
			input:     `"type": "service_account"`,
			wantMatch: true,
		},
		{
			name:      "not a service account",
			input:     `{"type": "user_account"}`,
			wantMatch: false,
		},
	}

	patterns := GetCloudPatterns()
	saPattern := findPatternByName(patterns, "gcp_service_account")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if saPattern == nil {
				t.Skip("GCP service account pattern not implemented yet")
				return
			}
			matches := saPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Azure credential detection patterns
func TestAzureClientSecretPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Azure client secret format 1",
			input:     "7c9~abcdefghijklmnopqrstuvwxyz1234",
			wantMatch: true,
		},
		{
			name:      "Azure client secret format 2",
			input:     "abc.defghijklmnopqrstuvwxyz123456~",
			wantMatch: true,
		},
		{
			name:      "Azure client secret in config",
			input:     `AZURE_CLIENT_SECRET=7c9~abcdefghijklmnopqrstuvwxyz1234`,
			wantMatch: true,
		},
		{
			name:      "too short",
			input:     "7c9~abc",
			wantMatch: false,
		},
	}

	patterns := GetCloudPatterns()
	azureSecretPattern := findPatternByName(patterns, "azure_client_secret")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if azureSecretPattern == nil {
				t.Skip("Azure client secret pattern not implemented yet")
				return
			}
			matches := azureSecretPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

func TestAzureConnectionString(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "storage connection string",
			input:     "DefaultEndpointsProtocol=https;AccountName=myaccount;AccountKey=abc123/def456+ghijk==;EndpointSuffix=core.windows.net",
			wantMatch: true,
		},
		{
			name:      "partial connection string",
			input:     "AccountKey=abc123def456ghijk/MNOP+xyz==",
			wantMatch: true,
		},
		{
			name:      "not a connection string",
			input:     "some random text",
			wantMatch: false,
		},
	}

	patterns := GetCloudPatterns()
	connStringPattern := findPatternByName(patterns, "azure_connection_string")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if connStringPattern == nil {
				t.Skip("Azure connection string pattern not implemented yet")
				return
			}
			matches := connStringPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Helper function to find pattern by name
func findPatternByName(patterns []*Pattern, name string) *Pattern {
	for _, p := range patterns {
		if p.Name == name {
			return p
		}
	}
	return nil
}
