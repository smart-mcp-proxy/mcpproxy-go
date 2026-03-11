package secret

import (
	"context"
	"errors"
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

func (m *MockProvider) Resolve(ctx context.Context, ref Ref) (string, error) {
	args := m.Called(ctx, ref)
	return args.String(0), args.Error(1)
}

func (m *MockProvider) Store(ctx context.Context, ref Ref, value string) error {
	args := m.Called(ctx, ref, value)
	return args.Error(0)
}

func (m *MockProvider) Delete(ctx context.Context, ref Ref) error {
	args := m.Called(ctx, ref)
	return args.Error(0)
}

func (m *MockProvider) List(ctx context.Context) ([]Ref, error) {
	args := m.Called(ctx)
	return args.Get(0).([]Ref), args.Error(1)
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

	ref := Ref{
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
		unknownRef := Ref{Type: "unknown", Name: "test"}

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
		mockProvider.On("Resolve", ctx, Ref{
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
		freshMockProvider.On("Resolve", ctx, Ref{
			Type:     "mock",
			Name:     "username",
			Original: "${mock:username}",
		}).Return("user123", nil)
		freshMockProvider.On("Resolve", ctx, Ref{
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

// --- Tests for ExpandStructSecretsCollectErrors ---

func TestExpandStructSecretsCollectErrors_HappyPath(t *testing.T) {
	type simpleConfig struct {
		WorkingDir string
		URL        string
	}
	s := &simpleConfig{
		WorkingDir: "${mock:work-dir}",
		URL:        "https://plain.example.com",
	}

	mockProvider := &MockProvider{}
	r := &Resolver{providers: make(map[string]Provider)}
	r.RegisterProvider("mock", mockProvider)

	ctx := context.Background()
	mockProvider.On("CanResolve", "mock").Return(true)
	mockProvider.On("IsAvailable").Return(true)
	mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "work-dir", Original: "${mock:work-dir}"}).Return("/home/user/project", nil)

	errs := r.ExpandStructSecretsCollectErrors(ctx, s)

	assert.Empty(t, errs)
	assert.Equal(t, "/home/user/project", s.WorkingDir)
	assert.Equal(t, "https://plain.example.com", s.URL) // plain values untouched
	mockProvider.AssertExpectations(t)
}

func TestExpandStructSecretsCollectErrors_PartialFailure(t *testing.T) {
	type twoFieldConfig struct {
		URL        string
		WorkingDir string
	}
	s := &twoFieldConfig{
		URL:        "${mock:my-url}",
		WorkingDir: "${mock:missing-dir}",
	}

	mockProvider := &MockProvider{}
	r := &Resolver{providers: make(map[string]Provider)}
	r.RegisterProvider("mock", mockProvider)

	ctx := context.Background()
	mockProvider.On("CanResolve", "mock").Return(true)
	mockProvider.On("IsAvailable").Return(true)
	mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "my-url", Original: "${mock:my-url}"}).Return("https://resolved.example.com", nil)
	mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "missing-dir", Original: "${mock:missing-dir}"}).Return("", errors.New("secret not found"))

	errs := r.ExpandStructSecretsCollectErrors(ctx, s)

	assert.Len(t, errs, 1)
	assert.Equal(t, "WorkingDir", errs[0].FieldPath)
	assert.Equal(t, "${mock:missing-dir}", errs[0].Reference)
	assert.Error(t, errs[0].Err)
	// Successful field is resolved; failed field retains original value
	assert.Equal(t, "https://resolved.example.com", s.URL)
	assert.Equal(t, "${mock:missing-dir}", s.WorkingDir)
	mockProvider.AssertExpectations(t)
}

func TestExpandStructSecretsCollectErrors_NilPointer(t *testing.T) {
	type nested struct {
		WorkingDir string
	}
	type configWithOptional struct {
		WorkingDir string
		Isolation  *nested
	}
	s := &configWithOptional{WorkingDir: "no-refs", Isolation: nil}

	r := &Resolver{providers: make(map[string]Provider)}
	ctx := context.Background()

	// Should not panic on nil nested pointer
	errs := r.ExpandStructSecretsCollectErrors(ctx, s)

	assert.Empty(t, errs)
	assert.Equal(t, "no-refs", s.WorkingDir)
}

func TestExpandStructSecretsCollectErrors_NestedStruct(t *testing.T) {
	type isolationConfig struct {
		WorkingDir string
	}
	type serverConfig struct {
		Isolation *isolationConfig
	}

	mockProvider := &MockProvider{}
	r := &Resolver{providers: make(map[string]Provider)}
	r.RegisterProvider("mock", mockProvider)
	ctx := context.Background()

	t.Run("success expands nested field", func(t *testing.T) {
		s := &serverConfig{Isolation: &isolationConfig{WorkingDir: "${mock:iso-dir}"}}
		mockProvider.On("CanResolve", "mock").Return(true)
		mockProvider.On("IsAvailable").Return(true)
		mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "iso-dir", Original: "${mock:iso-dir}"}).Return("/isolation/dir", nil)

		errs := r.ExpandStructSecretsCollectErrors(ctx, s)

		assert.Empty(t, errs)
		assert.Equal(t, "/isolation/dir", s.Isolation.WorkingDir)
		mockProvider.AssertExpectations(t)
	})

	t.Run("failure reports nested field path", func(t *testing.T) {
		mockFail := &MockProvider{}
		rFail := &Resolver{providers: make(map[string]Provider)}
		rFail.RegisterProvider("mock", mockFail)

		s := &serverConfig{Isolation: &isolationConfig{WorkingDir: "${mock:missing}"}}
		mockFail.On("CanResolve", "mock").Return(true)
		mockFail.On("IsAvailable").Return(true)
		mockFail.On("Resolve", ctx, Ref{Type: "mock", Name: "missing", Original: "${mock:missing}"}).Return("", errors.New("not found"))

		errs := rFail.ExpandStructSecretsCollectErrors(ctx, s)

		assert.Len(t, errs, 1)
		assert.Equal(t, "Isolation.WorkingDir", errs[0].FieldPath)
		assert.Equal(t, "${mock:missing}", errs[0].Reference)
		// Original value retained on failure
		assert.Equal(t, "${mock:missing}", s.Isolation.WorkingDir)
		mockFail.AssertExpectations(t)
	})
}

func TestExpandStructSecretsCollectErrors_SliceField(t *testing.T) {
	type configWithArgs struct {
		Args []string
	}
	s := &configWithArgs{Args: []string{"${mock:arg0}", "${mock:arg1}"}}

	mockProvider := &MockProvider{}
	r := &Resolver{providers: make(map[string]Provider)}
	r.RegisterProvider("mock", mockProvider)

	ctx := context.Background()
	mockProvider.On("CanResolve", "mock").Return(true)
	mockProvider.On("IsAvailable").Return(true)
	mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "arg0", Original: "${mock:arg0}"}).Return("resolved-arg0", nil)
	mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "arg1", Original: "${mock:arg1}"}).Return("resolved-arg1", nil)

	errs := r.ExpandStructSecretsCollectErrors(ctx, s)

	assert.Empty(t, errs)
	assert.Equal(t, []string{"resolved-arg0", "resolved-arg1"}, s.Args)
	mockProvider.AssertExpectations(t)

	// Verify failure reports correct path "Args[0]"
	mockFail := &MockProvider{}
	rFail := &Resolver{providers: make(map[string]Provider)}
	rFail.RegisterProvider("mock", mockFail)

	sFail := &configWithArgs{Args: []string{"${mock:missing}"}}
	mockFail.On("CanResolve", "mock").Return(true)
	mockFail.On("IsAvailable").Return(true)
	mockFail.On("Resolve", ctx, Ref{Type: "mock", Name: "missing", Original: "${mock:missing}"}).Return("", errors.New("not found"))

	errsFail := rFail.ExpandStructSecretsCollectErrors(ctx, sFail)
	assert.Len(t, errsFail, 1)
	assert.Equal(t, "Args[0]", errsFail[0].FieldPath)
	mockFail.AssertExpectations(t)
}

func TestExpandStructSecretsCollectErrors_MapField(t *testing.T) {
	type configWithEnv struct {
		Env map[string]string
	}
	s := &configWithEnv{Env: map[string]string{"MY_VAR": "${mock:my-secret}"}}

	mockProvider := &MockProvider{}
	r := &Resolver{providers: make(map[string]Provider)}
	r.RegisterProvider("mock", mockProvider)

	ctx := context.Background()
	mockProvider.On("CanResolve", "mock").Return(true)
	mockProvider.On("IsAvailable").Return(true)
	mockProvider.On("Resolve", ctx, Ref{Type: "mock", Name: "my-secret", Original: "${mock:my-secret}"}).Return("resolved-secret", nil)

	errs := r.ExpandStructSecretsCollectErrors(ctx, s)

	assert.Empty(t, errs)
	assert.Equal(t, "resolved-secret", s.Env["MY_VAR"])
	mockProvider.AssertExpectations(t)

	// Verify failure reports correct path "Env[MY_VAR]"
	mockFail := &MockProvider{}
	rFail := &Resolver{providers: make(map[string]Provider)}
	rFail.RegisterProvider("mock", mockFail)

	sFail := &configWithEnv{Env: map[string]string{"MY_VAR": "${mock:missing}"}}
	mockFail.On("CanResolve", "mock").Return(true)
	mockFail.On("IsAvailable").Return(true)
	mockFail.On("Resolve", ctx, Ref{Type: "mock", Name: "missing", Original: "${mock:missing}"}).Return("", errors.New("not found"))

	errsFail := rFail.ExpandStructSecretsCollectErrors(ctx, sFail)
	assert.Len(t, errsFail, 1)
	assert.Equal(t, "Env[MY_VAR]", errsFail[0].FieldPath)
	mockFail.AssertExpectations(t)
}

func TestExpandStructSecretsCollectErrors_NoRefs(t *testing.T) {
	type simpleConfig struct {
		WorkingDir string
		URL        string
		Args       []string
	}
	s := &simpleConfig{
		WorkingDir: "/absolute/path",
		URL:        "https://plain.example.com",
		Args:       []string{"--flag", "value"},
	}

	r := &Resolver{providers: make(map[string]Provider)}
	ctx := context.Background()

	errs := r.ExpandStructSecretsCollectErrors(ctx, s)

	assert.Empty(t, errs)
	assert.Equal(t, "/absolute/path", s.WorkingDir)
	assert.Equal(t, "https://plain.example.com", s.URL)
	assert.Equal(t, []string{"--flag", "value"}, s.Args)
}
