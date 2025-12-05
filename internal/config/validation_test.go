package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateOAuthExtraParams_RejectsReservedParams(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]string
		expectErr bool
	}{
		{
			name:      "resource param allowed",
			params:    map[string]string{"resource": "https://example.com"},
			expectErr: false,
		},
		{
			name:      "client_id reserved",
			params:    map[string]string{"client_id": "foo"},
			expectErr: true,
		},
		{
			name:      "redirect_uri reserved",
			params:    map[string]string{"redirect_uri": "http://localhost"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOAuthExtraParams(tt.params)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
