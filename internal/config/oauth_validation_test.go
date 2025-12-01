package config

import (
	"strings"
	"testing"
)

func TestValidateOAuthExtraParams(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:    "nil params",
			params:  nil,
			wantErr: false,
		},
		{
			name:    "empty params",
			params:  map[string]string{},
			wantErr: false,
		},
		{
			name: "valid RFC 8707 resource parameter",
			params: map[string]string{
				"resource": "https://example.com/mcp",
			},
			wantErr: false,
		},
		{
			name: "valid multiple custom parameters",
			params: map[string]string{
				"resource": "https://example.com/mcp",
				"audience": "mcp-api",
				"tenant":   "tenant-123",
			},
			wantErr: false,
		},
		{
			name: "invalid - attempts to override client_id",
			params: map[string]string{
				"client_id": "malicious-client",
			},
			wantErr:     true,
			errContains: "client_id",
		},
		{
			name: "invalid - attempts to override state",
			params: map[string]string{
				"state": "malicious-state",
			},
			wantErr:     true,
			errContains: "state",
		},
		{
			name: "invalid - attempts to override redirect_uri",
			params: map[string]string{
				"redirect_uri": "https://evil.com/callback",
			},
			wantErr:     true,
			errContains: "redirect_uri",
		},
		{
			name: "invalid - attempts to override multiple reserved params",
			params: map[string]string{
				"client_id":      "malicious",
				"client_secret":  "secret",
				"response_type":  "code",
			},
			wantErr:     true,
			errContains: "reserved OAuth 2.0 parameters",
		},
		{
			name: "valid resource with invalid client_id",
			params: map[string]string{
				"resource":  "https://example.com/mcp",
				"client_id": "malicious",
			},
			wantErr:     true,
			errContains: "client_id",
		},
		{
			name: "case insensitive validation",
			params: map[string]string{
				"CLIENT_ID": "malicious",
			},
			wantErr:     true,
			errContains: "CLIENT_ID",
		},
		{
			name: "code_challenge parameter",
			params: map[string]string{
				"code_challenge": "malicious",
			},
			wantErr:     true,
			errContains: "code_challenge",
		},
		{
			name: "code_challenge_method parameter",
			params: map[string]string{
				"code_challenge_method": "plain",
			},
			wantErr:     true,
			errContains: "code_challenge_method",
		},
		{
			name: "grant_type parameter",
			params: map[string]string{
				"grant_type": "password",
			},
			wantErr:     true,
			errContains: "grant_type",
		},
		{
			name: "refresh_token parameter",
			params: map[string]string{
				"refresh_token": "malicious",
			},
			wantErr:     true,
			errContains: "refresh_token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOAuthExtraParams(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOAuthExtraParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateOAuthExtraParams() error = %v, should contain %q", err, tt.errContains)
				}
			}
		})
	}
}

func TestOAuthConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		config      *OAuthConfig
		wantErr     bool
		errContains string
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: false,
		},
		{
			name: "empty config",
			config: &OAuthConfig{},
			wantErr: false,
		},
		{
			name: "valid config with extra params",
			config: &OAuthConfig{
				ClientID:    "test-client",
				ExtraParams: map[string]string{
					"resource": "https://example.com/mcp",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid config with reserved param",
			config: &OAuthConfig{
				ClientID: "test-client",
				ExtraParams: map[string]string{
					"client_id": "malicious",
				},
			},
			wantErr:     true,
			errContains: "client_id",
		},
		{
			name: "valid config without extra params",
			config: &OAuthConfig{
				ClientID:    "test-client",
				Scopes:      []string{"read", "write"},
				PKCEEnabled: true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("OAuthConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("OAuthConfig.Validate() error = %v, should contain %q", err, tt.errContains)
				}
			}
		})
	}
}
