package secret

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockProvider for testing
type MockProvider struct {
	mock.Mock
}

func (m *MockProvider) CanResolve(secretType string) bool {
	args := m.Called(secretType)
	return args.Bool(0)
}

func (m *MockProvider) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	args := m.Called(ctx, ref)
	return args.String(0), args.Error(1)
}

func (m *MockProvider) Store(ctx context.Context, ref SecretRef, value string) error {
	args := m.Called(ctx, ref, value)
	return args.Error(0)
}

func (m *MockProvider) Delete(ctx context.Context, ref SecretRef) error {
	args := m.Called(ctx, ref)
	return args.Error(0)
}

func (m *MockProvider) List(ctx context.Context) ([]SecretRef, error) {
	args := m.Called(ctx)
	return args.Get(0).([]SecretRef), args.Error(1)
}

func (m *MockProvider) IsAvailable() bool {
	args := m.Called()
	return args.Bool(0)
}

func TestResolver_RegisterProvider(t *testing.T) {
	resolver := &Resolver{
		providers: make(map[string]Provider),
	}

	mockProvider := &MockProvider{}
	resolver.RegisterProvider("mock", mockProvider)

	assert.Contains(t, resolver.providers, "mock")
	assert.Equal(t, mockProvider, resolver.providers["mock"])
}

func TestResolver_Resolve(t *testing.T) {
	resolver := &Resolver{
		providers: make(map[string]Provider),
	}

	mockProvider := &MockProvider{}
	resolver.RegisterProvider("mock", mockProvider)

	ref := SecretRef{
		Type: "mock",
		Name: "test-key",
	}

	ctx := context.Background()

	t.Run("successful resolution", func(t *testing.T) {
		mockProvider.On("CanResolve", "mock").Return(true)
		mockProvider.On("IsAvailable").Return(true)
		mockProvider.On("Resolve", ctx, ref).Return("secret-value", nil)

		result, err := resolver.Resolve(ctx, ref)

		assert.NoError(t, err)
		assert.Equal(t, "secret-value", result)
		mockProvider.AssertExpectations(t)
	})

	t.Run("provider not found", func(t *testing.T) {
		unknownRef := SecretRef{Type: "unknown", Name: "test"}

		_, err := resolver.Resolve(ctx, unknownRef)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no provider for secret type")
	})
}

func TestResolver_ExpandSecretRefs(t *testing.T) {
	resolver := &Resolver{
		providers: make(map[string]Provider),
	}

	mockProvider := &MockProvider{}
	resolver.RegisterProvider("mock", mockProvider)

	ctx := context.Background()

	t.Run("expand single reference", func(t *testing.T) {
		input := "token: ${mock:api-key}"

		mockProvider.On("CanResolve", "mock").Return(true)
		mockProvider.On("IsAvailable").Return(true)
		mockProvider.On("Resolve", ctx, SecretRef{
			Type:     "mock",
			Name:     "api-key",
			Original: "${mock:api-key}",
		}).Return("secret123", nil)

		result, err := resolver.ExpandSecretRefs(ctx, input)

		assert.NoError(t, err)
		assert.Equal(t, "token: secret123", result)
		mockProvider.AssertExpectations(t)
	})

	t.Run("no expansion needed", func(t *testing.T) {
		input := "plain text"

		result, err := resolver.ExpandSecretRefs(ctx, input)

		assert.NoError(t, err)
		assert.Equal(t, input, result)
	})

	t.Run("expand multiple references", func(t *testing.T) {
		// Create a fresh mock provider for this test to avoid conflicts
		freshMockProvider := &MockProvider{}
		freshResolver := &Resolver{
			providers: make(map[string]Provider),
		}
		freshResolver.RegisterProvider("mock", freshMockProvider)

		input := "user: ${mock:username} pass: ${mock:password}"

		freshMockProvider.On("CanResolve", "mock").Return(true).Times(2)
		freshMockProvider.On("IsAvailable").Return(true).Times(2)
		freshMockProvider.On("Resolve", ctx, SecretRef{
			Type:     "mock",
			Name:     "username",
			Original: "${mock:username}",
		}).Return("user123", nil)
		freshMockProvider.On("Resolve", ctx, SecretRef{
			Type:     "mock",
			Name:     "password",
			Original: "${mock:password}",
		}).Return("pass456", nil)

		result, err := freshResolver.ExpandSecretRefs(ctx, input)

		assert.NoError(t, err)
		assert.Equal(t, "user: user123 pass: pass456", result)
		freshMockProvider.AssertExpectations(t)
	})
}

func TestResolver_GetAvailableProviders(t *testing.T) {
	resolver := &Resolver{
		providers: make(map[string]Provider),
	}

	mockProvider1 := &MockProvider{}
	mockProvider2 := &MockProvider{}

	resolver.RegisterProvider("available", mockProvider1)
	resolver.RegisterProvider("unavailable", mockProvider2)

	mockProvider1.On("IsAvailable").Return(true)
	mockProvider2.On("IsAvailable").Return(false)

	available := resolver.GetAvailableProviders()

	assert.Len(t, available, 1)
	assert.Contains(t, available, "available")
	assert.NotContains(t, available, "unavailable")

	mockProvider1.AssertExpectations(t)
	mockProvider2.AssertExpectations(t)
}

func TestResolver_AnalyzeForMigration(t *testing.T) {
	resolver := &Resolver{
		providers: make(map[string]Provider),
	}

	// Test struct with potential secrets
	testConfig := struct {
		Host     string `json:"host"`
		APIKey   string `json:"api_key"`
		Password string `json:"password"`
		Debug    bool   `json:"debug"`
	}{
		Host:     "localhost",
		APIKey:   "sk-1234567890abcdef1234567890abcdef",
		Password: "supersecretpassword123",
		Debug:    true,
	}

	analysis := resolver.AnalyzeForMigration(testConfig)

	assert.NotNil(t, analysis)
	assert.Greater(t, analysis.TotalFound, 0)

	// Should detect API key and password as potential secrets
	foundAPIKey := false
	foundPassword := false

	for _, candidate := range analysis.Candidates {
		if candidate.Field == "APIKey" {
			foundAPIKey = true
			assert.Greater(t, candidate.Confidence, 0.5)
			assert.Contains(t, candidate.Suggested, "keyring:")
		}
		if candidate.Field == "Password" {
			foundPassword = true
			assert.Greater(t, candidate.Confidence, 0.5)
		}
	}

	assert.True(t, foundAPIKey, "Should detect API key as potential secret")
	assert.True(t, foundPassword, "Should detect password as potential secret")
}

func TestNewResolver(t *testing.T) {
	resolver := NewResolver()

	assert.NotNil(t, resolver)
	assert.NotNil(t, resolver.providers)

	// Should have default providers registered
	assert.Contains(t, resolver.providers, "env")
	assert.Contains(t, resolver.providers, "keyring")
}
