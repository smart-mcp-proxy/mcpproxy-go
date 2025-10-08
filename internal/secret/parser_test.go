package secret

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSecretRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *Ref
		wantErr bool
	}{
		{
			name:  "valid keyring reference",
			input: "${keyring:my-api-key}",
			want: &Ref{
				Type:     "keyring",
				Name:     "my-api-key",
				Original: "${keyring:my-api-key}",
			},
			wantErr: false,
		},
		{
			name:  "valid env reference",
			input: "${env:API_KEY}",
			want: &Ref{
				Type:     "env",
				Name:     "API_KEY",
				Original: "${env:API_KEY}",
			},
			wantErr: false,
		},
		{
			name:  "valid reference with spaces",
			input: "${keyring: my key }",
			want: &Ref{
				Type:     "keyring",
				Name:     "my key",
				Original: "${keyring: my key }",
			},
			wantErr: false,
		},
		{
			name:    "invalid reference - no colon",
			input:   "${keyring-my-key}",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid reference - no closing brace",
			input:   "${keyring:my-key",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "plain text",
			input:   "just-plain-text",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSecretRef(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, got)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestIsSecretRef(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid keyring reference",
			input: "${keyring:my-key}",
			want:  true,
		},
		{
			name:  "valid env reference",
			input: "${env:MY_VAR}",
			want:  true,
		},
		{
			name:  "plain text",
			input: "plain text",
			want:  false,
		},
		{
			name:  "partial reference",
			input: "${keyring:",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSecretRef(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindSecretRefs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []*Ref
	}{
		{
			name:  "single reference",
			input: "token: ${keyring:github-token}",
			want: []*Ref{
				{
					Type:     "keyring",
					Name:     "github-token",
					Original: "${keyring:github-token}",
				},
			},
		},
		{
			name:  "multiple references",
			input: "api_key: ${env:API_KEY} and secret: ${keyring:secret}",
			want: []*Ref{
				{
					Type:     "env",
					Name:     "API_KEY",
					Original: "${env:API_KEY}",
				},
				{
					Type:     "keyring",
					Name:     "secret",
					Original: "${keyring:secret}",
				},
			},
		},
		{
			name:  "no references",
			input: "plain text with no secrets",
			want:  []*Ref{},
		},
		{
			name:  "empty string",
			input: "",
			want:  []*Ref{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindSecretRefs(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMaskSecretValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "short value",
			input: "abc",
			want:  "****",
		},
		{
			name:  "medium value",
			input: "abcdef",
			want:  "ab****",
		},
		{
			name:  "long value",
			input: "abcdefghijklmnop",
			want:  "abc****op",
		},
		{
			name:  "empty value",
			input: "",
			want:  "****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskSecretValue(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectPotentialSecret(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		fieldName string
		wantIs    bool
		minConf   float64
	}{
		{
			name:      "api key field with high entropy value",
			value:     "sk-1234567890abcdef1234567890abcdef",
			fieldName: "api_key",
			wantIs:    true,
			minConf:   0.6,
		},
		{
			name:      "password field",
			value:     "supersecretpassword123",
			fieldName: "password",
			wantIs:    true,
			minConf:   0.5,
		},
		{
			name:      "token field with base64-like value",
			value:     "dGVzdC10b2tlbi12YWx1ZQ==",
			fieldName: "auth_token",
			wantIs:    true,
			minConf:   0.7,
		},
		{
			name:      "regular config value",
			value:     "localhost",
			fieldName: "host",
			wantIs:    false,
			minConf:   0.0,
		},
		{
			name:      "empty value",
			value:     "",
			fieldName: "api_key",
			wantIs:    false,
			minConf:   0.0,
		},
		{
			name:      "short value",
			value:     "test",
			fieldName: "password",
			wantIs:    false,
			minConf:   0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isSecret, confidence := DetectPotentialSecret(tt.value, tt.fieldName)
			assert.Equal(t, tt.wantIs, isSecret)
			if tt.wantIs {
				assert.GreaterOrEqual(t, confidence, tt.minConf)
			}
		})
	}
}
