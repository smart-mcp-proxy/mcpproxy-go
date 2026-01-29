package core

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// MockSecretProvider for testing secret resolution
type MockSecretProvider struct {
	mock.Mock
}

func (m *MockSecretProvider) CanResolve(secretType string) bool {
	args := m.Called(secretType)
	return args.Bool(0)
}

func (m *MockSecretProvider) Resolve(ctx context.Context, ref secret.Ref) (string, error) {
	args := m.Called(ctx, ref)
	return args.String(0), args.Error(1)
}

func (m *MockSecretProvider) Store(ctx context.Context, ref secret.Ref, value string) error {
	args := m.Called(ctx, ref, value)
	return args.Error(0)
}

func (m *MockSecretProvider) Delete(ctx context.Context, ref secret.Ref) error {
	args := m.Called(ctx, ref)
	return args.Error(0)
}

func (m *MockSecretProvider) List(ctx context.Context) ([]secret.Ref, error) {
	args := m.Called(ctx)
	return args.Get(0).([]secret.Ref), args.Error(1)
}

func (m *MockSecretProvider) IsAvailable() bool {
	args := m.Called()
	return args.Bool(0)
}

func TestClient_HeaderSecretResolution(t *testing.T) {
	logger := zap.NewNop()

	// Setup mock secret provider
	mockProvider := &MockSecretProvider{}
	resolver := secret.NewResolver()
	resolver.RegisterProvider("env", mockProvider)

	// Create a temporary database for testing
	tempDB, err := storage.NewBoltDB(t.TempDir()+"/test.db", logger.Sugar())
	if err == nil {
		defer tempDB.Close()
	}

	ctx := context.Background()

	t.Run("resolve secret in header", func(t *testing.T) {
		serverConfig := &config.ServerConfig{
			Name:     "test-server",
			Protocol: "stdio",
			Command:  "test-command",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:API_TOKEN}",
				"X-Custom":      "static-value",
			},
		}

		// Mock the secret resolution
		mockProvider.On("CanResolve", "env").Return(true)
		mockProvider.On("IsAvailable").Return(true)
		mockProvider.On("Resolve", ctx, secret.Ref{
			Type:     "env",
			Name:     "API_TOKEN",
			Original: "${env:API_TOKEN}",
		}).Return("secret-token-123", nil)

		// Create client with secret resolver
		client, err := NewClientWithOptions(
			"test-id",
			serverConfig,
			logger,
			&config.LogConfig{},
			&config.Config{},
			tempDB,
			false,
			resolver,
		)

		assert.NoError(t, err)
		assert.NotNil(t, client)

		// Verify headers are resolved
		assert.Equal(t, "Bearer secret-token-123", client.config.Headers["Authorization"])
		assert.Equal(t, "static-value", client.config.Headers["X-Custom"])

		mockProvider.AssertExpectations(t)
	})

	t.Run("resolve multiple secrets in headers", func(t *testing.T) {
		// Create a fresh mock provider for this test
		freshMockProvider := &MockSecretProvider{}
		freshResolver := secret.NewResolver()
		freshResolver.RegisterProvider("env", freshMockProvider)

		serverConfig := &config.ServerConfig{
			Name:     "test-server-2",
			Protocol: "stdio",
			Command:  "test-command",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:API_TOKEN}",
				"X-API-Key":     "${env:API_KEY}",
			},
		}

		// Mock the secret resolution
		freshMockProvider.On("CanResolve", "env").Return(true).Times(2)
		freshMockProvider.On("IsAvailable").Return(true).Times(2)
		freshMockProvider.On("Resolve", ctx, secret.Ref{
			Type:     "env",
			Name:     "API_TOKEN",
			Original: "${env:API_TOKEN}",
		}).Return("token-123", nil)
		freshMockProvider.On("Resolve", ctx, secret.Ref{
			Type:     "env",
			Name:     "API_KEY",
			Original: "${env:API_KEY}",
		}).Return("key-456", nil)

		// Create client with secret resolver
		client, err := NewClientWithOptions(
			"test-id-2",
			serverConfig,
			logger,
			&config.LogConfig{},
			&config.Config{},
			tempDB,
			false,
			freshResolver,
		)

		assert.NoError(t, err)
		assert.NotNil(t, client)

		// Verify all headers are resolved
		assert.Equal(t, "Bearer token-123", client.config.Headers["Authorization"])
		assert.Equal(t, "key-456", client.config.Headers["X-API-Key"])

		freshMockProvider.AssertExpectations(t)
	})

	t.Run("no headers to resolve", func(t *testing.T) {
		serverConfig := &config.ServerConfig{
			Name:     "test-server-3",
			Protocol: "stdio",
			Command:  "test-command",
			Headers:  nil,
		}

		// Create client without secret resolver (nil)
		client, err := NewClientWithOptions(
			"test-id-3",
			serverConfig,
			logger,
			&config.LogConfig{},
			&config.Config{},
			tempDB,
			false,
			nil,
		)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Nil(t, client.config.Headers)
	})

	t.Run("headers without secret references", func(t *testing.T) {
		serverConfig := &config.ServerConfig{
			Name:     "test-server-4",
			Protocol: "stdio",
			Command:  "test-command",
			Headers: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "static-value",
			},
		}

		// Create client with secret resolver (but no secrets to resolve)
		client, err := NewClientWithOptions(
			"test-id-4",
			serverConfig,
			logger,
			&config.LogConfig{},
			&config.Config{},
			tempDB,
			false,
			resolver,
		)

		assert.NoError(t, err)
		assert.NotNil(t, client)

		// Verify headers are unchanged
		assert.Equal(t, "application/json", client.config.Headers["Content-Type"])
		assert.Equal(t, "static-value", client.config.Headers["X-Custom"])
	})
}
