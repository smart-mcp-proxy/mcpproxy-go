package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectExtraParamsIntoURL(t *testing.T) {
	wrapper := &OAuthTransportWrapper{
		extraParams: map[string]string{
			"resource": "https://example.com/mcp",
		},
	}

	baseURL := "https://auth.example.com/authorize?client_id=abc"
	modifiedURL, err := wrapper.InjectExtraParamsIntoURL(baseURL)

	require.NoError(t, err)
	assert.Contains(t, modifiedURL, "resource=https%3A%2F%2Fexample.com%2Fmcp")
}

func TestInjectExtraParamsIntoURL_EmptyParams(t *testing.T) {
	wrapper := &OAuthTransportWrapper{
		extraParams: map[string]string{},
	}

	baseURL := "https://auth.example.com/authorize?client_id=abc"
	modifiedURL, err := wrapper.InjectExtraParamsIntoURL(baseURL)

	require.NoError(t, err)
	assert.Equal(t, baseURL, modifiedURL)
}
