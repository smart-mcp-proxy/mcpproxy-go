package secret

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolver_UpstreamEnvGuard(t *testing.T) {
	r := NewResolverWithOptions(ResolverOptions{
		Scope:      ScopeUpstream,
		FilePolicy: NewWildcardFilePolicy(ScopeUpstream),
	})
	for _, ref := range []string{
		"${env:MCPROXY_API_KEY}",
		"${env:MCPROXY_API_KEY_FILE}",
		"${env:MCPROXY_CRED_KEY}",
	} {
		_, err := r.ExpandSecretRefs(context.Background(), ref)
		require.ErrorIs(t, err, ErrReservedEnvVar, ref)
	}
}

func TestEnvProvider_ReservedGuard(t *testing.T) {
	p := NewEnvProvider(true)
	_, err := p.Resolve(context.Background(), Ref{Type: "env", Name: "MCPROXY_API_KEY"})
	require.ErrorIs(t, err, ErrReservedEnvVar)

	t.Setenv("MCPROXY_API_KEY", "sekrit")
	p2 := NewEnvProvider(false)
	val, err := p2.Resolve(context.Background(), Ref{Type: "env", Name: "MCPROXY_API_KEY"})
	require.NoError(t, err)
	assert.Equal(t, "sekrit", val)
}

func TestEnvProvider_CanResolve(t *testing.T) {
	provider := NewEnvProvider(false)

	assert.True(t, provider.CanResolve("env"))
	assert.False(t, provider.CanResolve("keyring"))
	assert.False(t, provider.CanResolve("unknown"))
}

func TestEnvProvider_IsAvailable(t *testing.T) {
	provider := NewEnvProvider(false)
	assert.True(t, provider.IsAvailable())
}

func TestEnvProvider_Resolve(t *testing.T) {
	provider := NewEnvProvider(false)
	ctx := context.Background()

	t.Run("existing environment variable", func(t *testing.T) {
		// Set a test environment variable
		key := "TEST_SECRET_VAR"
		value := "test-secret-value"
		os.Setenv(key, value)
		defer os.Unsetenv(key)

		ref := Ref{
			Type: "env",
			Name: key,
		}

		result, err := provider.Resolve(ctx, ref)

		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("non-existing environment variable", func(t *testing.T) {
		ref := Ref{
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

		ref := Ref{
			Type: "env",
			Name: key,
		}

		_, err := provider.Resolve(ctx, ref)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found or empty")
	})

	t.Run("wrong secret type", func(t *testing.T) {
		ref := Ref{
			Type: "keyring",
			Name: "test",
		}

		_, err := provider.Resolve(ctx, ref)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot resolve secret type")
	})
}

func TestEnvProvider_Store(t *testing.T) {
	provider := NewEnvProvider(false)
	ctx := context.Background()

	ref := Ref{
		Type: "env",
		Name: "test",
	}

	err := provider.Store(ctx, ref, "value")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support storing")
}

func TestEnvProvider_Delete(t *testing.T) {
	provider := NewEnvProvider(false)
	ctx := context.Background()

	ref := Ref{
		Type: "env",
		Name: "test",
	}

	err := provider.Delete(ctx, ref)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support deleting")
}

func TestEnvProvider_List(t *testing.T) {
	provider := NewEnvProvider(false)
	ctx := context.Background()

	// Set some test environment variables
	testVars := map[string]string{
		"TEST_API_KEY":  "sk-1234567890abcdef",
		"TEST_PASSWORD": "secretpassword123",
		"TEST_REGULAR":  "localhost",
		"TEST_SHORT":    "abc",
	}

	// Set environment variables and collect keys for cleanup
	var keysToCleanup []string
	for key, value := range testVars {
		os.Setenv(key, value)
		keysToCleanup = append(keysToCleanup, key)
	}

	// Clean up environment variables after test
	defer func() {
		for _, key := range keysToCleanup {
			os.Unsetenv(key)
		}
	}()

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
