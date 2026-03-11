package core

import (
	"context"
	"fmt"
	"reflect"
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

// --- T007/T008: NewClientWithOptions struct-wide secret expansion ---

// makeResolverWithMock creates a Resolver with a "mock" provider for testing.
func makeResolverWithMock(mockP *MockSecretProvider) *secret.Resolver {
	r := secret.NewResolver()
	r.RegisterProvider("mock", mockP)
	return r
}

// makeTempDB creates a throwaway BoltDB for a test and schedules cleanup.
func makeTempDB(t *testing.T, logger *zap.Logger) *storage.BoltDB {
	t.Helper()
	db, err := storage.NewBoltDB(t.TempDir()+"/test.db", logger.Sugar())
	if err == nil {
		t.Cleanup(func() { db.Close() })
	}
	return db
}

func TestNewClientWithOptions_ExpandsWorkingDir(t *testing.T) {
	logger := zap.NewNop()
	db := makeTempDB(t, logger)
	ctx := context.Background()

	mockP := &MockSecretProvider{}
	resolver := makeResolverWithMock(mockP)

	serverConfig := &config.ServerConfig{
		Name:       "test-server",
		Protocol:   "stdio",
		Command:    "echo",
		WorkingDir: "${mock:work-dir}",
	}

	mockP.On("CanResolve", "mock").Return(true)
	mockP.On("IsAvailable").Return(true)
	mockP.On("Resolve", ctx, secret.Ref{
		Type:     "mock",
		Name:     "work-dir",
		Original: "${mock:work-dir}",
	}).Return("/resolved/work-dir", nil)

	client, err := NewClientWithOptions("test-expand-wd", serverConfig, logger, &config.LogConfig{}, &config.Config{}, db, false, resolver)

	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "/resolved/work-dir", client.config.WorkingDir)
	mockP.AssertExpectations(t)
}

func TestNewClientWithOptions_ExpandsIsolationWorkingDir(t *testing.T) {
	logger := zap.NewNop()
	db := makeTempDB(t, logger)
	ctx := context.Background()

	mockP := &MockSecretProvider{}
	resolver := makeResolverWithMock(mockP)

	serverConfig := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "stdio",
		Command:  "echo",
		Isolation: &config.IsolationConfig{
			WorkingDir: "${mock:iso-dir}",
		},
	}

	mockP.On("CanResolve", "mock").Return(true)
	mockP.On("IsAvailable").Return(true)
	mockP.On("Resolve", ctx, secret.Ref{
		Type:     "mock",
		Name:     "iso-dir",
		Original: "${mock:iso-dir}",
	}).Return("/resolved/iso-dir", nil)

	client, err := NewClientWithOptions("test-expand-iso-wd", serverConfig, logger, &config.LogConfig{}, &config.Config{}, db, false, resolver)

	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.config.Isolation)
	assert.Equal(t, "/resolved/iso-dir", client.config.Isolation.WorkingDir)
	mockP.AssertExpectations(t)
}

func TestNewClientWithOptions_ExpandsURL(t *testing.T) {
	logger := zap.NewNop()
	db := makeTempDB(t, logger)
	ctx := context.Background()

	mockP := &MockSecretProvider{}
	resolver := makeResolverWithMock(mockP)

	serverConfig := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "http",
		URL:      "https://${mock:api-host}/mcp",
	}

	mockP.On("CanResolve", "mock").Return(true)
	mockP.On("IsAvailable").Return(true)
	mockP.On("Resolve", ctx, secret.Ref{
		Type:     "mock",
		Name:     "api-host",
		Original: "${mock:api-host}",
	}).Return("api.example.com", nil)

	client, err := NewClientWithOptions("test-expand-url", serverConfig, logger, &config.LogConfig{}, &config.Config{}, db, false, resolver)

	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "https://api.example.com/mcp", client.config.URL)
	mockP.AssertExpectations(t)
}

func TestNewClientWithOptions_PreservesExistingEnvArgsHeaders(t *testing.T) {
	logger := zap.NewNop()
	db := makeTempDB(t, logger)
	ctx := context.Background()

	mockP := &MockSecretProvider{}
	resolver := makeResolverWithMock(mockP)

	serverConfig := &config.ServerConfig{
		Name:     "test-server",
		Protocol: "stdio",
		Command:  "echo",
		Env:      map[string]string{"MY_VAR": "${mock:env-val}"},
		Args:     []string{"${mock:arg-val}"},
		Headers:  map[string]string{"X-Key": "${mock:header-val}"},
	}

	mockP.On("CanResolve", "mock").Return(true)
	mockP.On("IsAvailable").Return(true)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "env-val", Original: "${mock:env-val}"}).Return("resolved-env", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "arg-val", Original: "${mock:arg-val}"}).Return("resolved-arg", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "header-val", Original: "${mock:header-val}"}).Return("resolved-header", nil)

	client, err := NewClientWithOptions("test-preserve-existing", serverConfig, logger, &config.LogConfig{}, &config.Config{}, db, false, resolver)

	assert.NoError(t, err)
	assert.NotNil(t, client)
	// FR-008: existing field expansion must not regress
	assert.Equal(t, "resolved-env", client.config.Env["MY_VAR"])
	assert.Equal(t, "resolved-arg", client.config.Args[0])
	assert.Equal(t, "resolved-header", client.config.Headers["X-Key"])
	mockP.AssertExpectations(t)
}

func TestNewClientWithOptions_DoesNotMutateOriginal(t *testing.T) {
	logger := zap.NewNop()
	db := makeTempDB(t, logger)
	ctx := context.Background()

	mockP := &MockSecretProvider{}
	resolver := makeResolverWithMock(mockP)

	serverConfig := &config.ServerConfig{
		Name:       "test-server",
		Protocol:   "stdio",
		Command:    "echo",
		WorkingDir: "${mock:work-dir}",
		URL:        "${mock:url}",
		Env:        map[string]string{"MY_VAR": "${mock:env-val}"},
	}

	mockP.On("CanResolve", "mock").Return(true)
	mockP.On("IsAvailable").Return(true)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "work-dir", Original: "${mock:work-dir}"}).Return("/resolved/dir", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "url", Original: "${mock:url}"}).Return("https://resolved.example.com", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "env-val", Original: "${mock:env-val}"}).Return("resolved-env", nil)

	_, err := NewClientWithOptions("test-no-mutate", serverConfig, logger, &config.LogConfig{}, &config.Config{}, db, false, resolver)

	assert.NoError(t, err)
	// FR-004: original ServerConfig must not be mutated after call
	assert.Equal(t, "${mock:work-dir}", serverConfig.WorkingDir)
	assert.Equal(t, "${mock:url}", serverConfig.URL)
	assert.Equal(t, "${mock:env-val}", serverConfig.Env["MY_VAR"])
}

// TestNewClientWithOptions_ReflectionRegressionTest (T008) walks all string fields of the
// resolved client config via reflection and asserts that none still match IsSecretRef().
// This catches any future ServerConfig string field that isn't covered by expansion (SC-004).
func TestNewClientWithOptions_ReflectionRegressionTest(t *testing.T) {
	logger := zap.NewNop()
	db := makeTempDB(t, logger)
	ctx := context.Background()

	mockP := &MockSecretProvider{}
	resolver := makeResolverWithMock(mockP)

	serverConfig := &config.ServerConfig{
		Name:       "test-server",
		Protocol:   "stdio",
		Command:    "echo",
		WorkingDir: "${mock:work-dir}",
		URL:        "${mock:url}",
		Env:        map[string]string{"MY_VAR": "${mock:env-val}"},
		Args:       []string{"${mock:arg-val}"},
		Headers:    map[string]string{"X-Key": "${mock:header-val}"},
		Isolation: &config.IsolationConfig{
			WorkingDir: "${mock:iso-work-dir}",
			Image:      "${mock:iso-image}",
		},
	}

	mockP.On("CanResolve", "mock").Return(true)
	mockP.On("IsAvailable").Return(true)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "work-dir", Original: "${mock:work-dir}"}).Return("/resolved/work-dir", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "url", Original: "${mock:url}"}).Return("https://resolved.example.com/mcp", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "env-val", Original: "${mock:env-val}"}).Return("resolved-env", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "arg-val", Original: "${mock:arg-val}"}).Return("resolved-arg", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "header-val", Original: "${mock:header-val}"}).Return("resolved-header", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "iso-work-dir", Original: "${mock:iso-work-dir}"}).Return("/resolved/iso-work-dir", nil)
	mockP.On("Resolve", ctx, secret.Ref{Type: "mock", Name: "iso-image", Original: "${mock:iso-image}"}).Return("myimage:latest", nil)

	client, err := NewClientWithOptions("test-reflection", serverConfig, logger, &config.LogConfig{}, &config.Config{}, db, false, resolver)

	assert.NoError(t, err)
	assert.NotNil(t, client)

	// Walk all string fields of the resolved config and collect any remaining refs.
	var unresolvedRefs []string
	collectSecretRefs(reflect.ValueOf(client.config), "", &unresolvedRefs)
	assert.Empty(t, unresolvedRefs, "all secret refs should be resolved; still unresolved: %v", unresolvedRefs)
}

// collectSecretRefs recursively walks v and appends "path=value" for any string
// matching secret.IsSecretRef to found. Used by the reflection regression test.
func collectSecretRefs(v reflect.Value, path string, found *[]string) {
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		collectSecretRefs(v.Elem(), path, found)
		return
	}
	switch v.Kind() {
	case reflect.String:
		if secret.IsSecretRef(v.String()) {
			*found = append(*found, fmt.Sprintf("%s=%s", path, v.String()))
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			fieldName := t.Field(i).Name
			newPath := fieldName
			if path != "" {
				newPath = path + "." + fieldName
			}
			collectSecretRefs(v.Field(i), newPath, found)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectSecretRefs(v.Index(i), fmt.Sprintf("%s[%d]", path, i), found)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			collectSecretRefs(v.MapIndex(key), fmt.Sprintf("%s[%v]", path, key.Interface()), found)
		}
	}
}
