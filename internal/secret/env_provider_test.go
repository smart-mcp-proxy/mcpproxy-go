package secret

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvProvider_CanResolve(t *testing.T) {
	provider := NewEnvProvider()

	assert.True(t, provider.CanResolve("env"))
	assert.False(t, provider.CanResolve("keyring"))
	assert.False(t, provider.CanResolve("unknown"))
}

func TestEnvProvider_IsAvailable(t *testing.T) {
	provider := NewEnvProvider()
	assert.True(t, provider.IsAvailable())
}

func TestEnvProvider_Resolve(t *testing.T) {
	provider := NewEnvProvider()
	ctx := context.Background()

	t.Run("existing environment variable", func(t *testing.T) {
		// Set a test environment variable
		key := "TEST_SECRET_VAR"
		value := "test-secret-value"
		os.Setenv(key, value)
		defer os.Unsetenv(key)

		ref := SecretRef{
			Type: "env",
			Name: key,
		}

		result, err := provider.Resolve(ctx, ref)

		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("non-existing environment variable", func(t *testing.T) {
		ref := SecretRef{
			Type: "env",
			Name: "NON_EXISTING_VAR",
		}

		_, err := provider.Resolve(ctx, ref)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found or empty")
	})

	t.Run("empty environment variable", func(t *testing.T) {
		key := "EMPTY_VAR"
		os.Setenv(key, "")
		defer os.Unsetenv(key)

		ref := SecretRef{
			Type: "env",
			Name: key,
		}

		_, err := provider.Resolve(ctx, ref)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found or empty")
	})

	t.Run("wrong secret type", func(t *testing.T) {
		ref := SecretRef{
			Type: "keyring",
			Name: "test",
		}

		_, err := provider.Resolve(ctx, ref)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot resolve secret type")
	})
}

func TestEnvProvider_Store(t *testing.T) {
	provider := NewEnvProvider()
	ctx := context.Background()

	ref := SecretRef{
		Type: "env",
		Name: "test",
	}

	err := provider.Store(ctx, ref, "value")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support storing")
}

func TestEnvProvider_Delete(t *testing.T) {
	provider := NewEnvProvider()
	ctx := context.Background()

	ref := SecretRef{
		Type: "env",
		Name: "test",
	}

	err := provider.Delete(ctx, ref)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support deleting")
}

func TestEnvProvider_List(t *testing.T) {
	provider := NewEnvProvider()
	ctx := context.Background()

	// Set some test environment variables
	testVars := map[string]string{
		"TEST_API_KEY":    "sk-1234567890abcdef",
		"TEST_PASSWORD":   "secretpassword123",
		"TEST_REGULAR":    "localhost",
		"TEST_SHORT":      "abc",
	}

	for key, value := range testVars {
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}

	refs, err := provider.List(ctx)

	assert.NoError(t, err)
	assert.NotNil(t, refs)

	// Should contain our test secret-like variables
	foundSecrets := make(map[string]bool)
	for _, ref := range refs {
		assert.Equal(t, "env", ref.Type)
		if _, exists := testVars[ref.Name]; exists {
			foundSecrets[ref.Name] = true
		}
	}

	// Should detect API key and password as secrets
	assert.True(t, foundSecrets["TEST_API_KEY"] || foundSecrets["TEST_PASSWORD"],
		"Should detect at least one of the secret-like variables")
}