package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskOAuthSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{
			name:   "long secret shows first 3 and last 4",
			secret: "abc123456789xyz",
			want:   "abc***9xyz",
		},
		{
			name:   "short secret (8 chars) fully masked",
			secret: "12345678",
			want:   "***",
		},
		{
			name:   "very short secret fully masked",
			secret: "abc",
			want:   "***",
		},
		{
			name:   "empty secret fully masked",
			secret: "",
			want:   "***",
		},
		{
			name:   "exactly 9 chars shows first 3 and last 4",
			secret: "123456789",
			want:   "123***6789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskOAuthSecret(tt.secret)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsResourceParam(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{
			name: "resource parameter (lowercase)",
			key:  "resource",
			want: true,
		},
		{
			name: "resource parameter (uppercase)",
			key:  "RESOURCE",
			want: true,
		},
		{
			name: "resource parameter (mixed case)",
			key:  "Resource",
			want: true,
		},
		{
			name: "resource_url parameter",
			key:  "resource_url",
			want: true,
		},
		{
			name: "audience parameter (exact match)",
			key:  "audience",
			want: true,
		},
		{
			name: "audience parameter (case insensitive)",
			key:  "AUDIENCE",
			want: true,
		},
		{
			name: "api_key is not a resource param",
			key:  "api_key",
			want: false,
		},
		{
			name: "token is not a resource param",
			key:  "token",
			want: false,
		},
		{
			name: "random param is not a resource param",
			key:  "custom_param",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isResourceParam(tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContainsSensitiveKeyword(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{
			name: "api_key contains 'key'",
			key:  "api_key",
			want: true,
		},
		{
			name: "client_secret contains 'secret'",
			key:  "client_secret",
			want: true,
		},
		{
			name: "access_token contains 'token'",
			key:  "access_token",
			want: true,
		},
		{
			name: "password contains 'password'",
			key:  "password",
			want: true,
		},
		{
			name: "credential contains 'credential'",
			key:  "credential",
			want: true,
		},
		{
			name: "KEY in uppercase",
			key:  "API_KEY",
			want: true,
		},
		{
			name: "resource does not contain sensitive keywords",
			key:  "resource",
			want: false,
		},
		{
			name: "audience does not contain sensitive keywords",
			key:  "audience",
			want: false,
		},
		{
			name: "tenant does not contain sensitive keywords",
			key:  "tenant",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsSensitiveKeyword(tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMaskExtraParams(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]string
		want   map[string]string
	}{
		{
			name:   "nil params returns nil",
			params: nil,
			want:   nil,
		},
		{
			name:   "empty params returns empty",
			params: map[string]string{},
			want:   map[string]string{},
		},
		{
			name: "resource URL shown in full",
			params: map[string]string{
				"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
			},
			want: map[string]string{
				"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
			},
		},
		{
			name: "audience shown in full",
			params: map[string]string{
				"audience": "mcp-api",
			},
			want: map[string]string{
				"audience": "mcp-api",
			},
		},
		{
			name: "api_key fully masked (sensitive keyword)",
			params: map[string]string{
				"api_key": "secret123456789",
			},
			want: map[string]string{
				"api_key": "***",
			},
		},
		{
			name: "custom param partially masked (default)",
			params: map[string]string{
				"tenant": "tenant-123456789",
			},
			want: map[string]string{
				"tenant": "ten***6789",
			},
		},
		{
			name: "mixed params with different masking rules",
			params: map[string]string{
				"resource":      "https://example.com/mcp",
				"audience":      "mcp-api",
				"api_key":       "secret-key-123",
				"tenant":        "tenant-456789",
				"client_secret": "very-secret",
			},
			want: map[string]string{
				"resource":      "https://example.com/mcp", // Resource URL - shown in full
				"audience":      "mcp-api",                 // Audience - shown in full
				"api_key":       "***",                     // Sensitive keyword - fully masked
				"tenant":        "ten***6789",              // Custom param - partially masked
				"client_secret": "***",                     // Sensitive keyword - fully masked
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskExtraParams(tt.params)
			assert.Equal(t, tt.want, got)
		})
	}
}
