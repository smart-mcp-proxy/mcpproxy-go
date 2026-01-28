package appctx

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ContractTests verify that interface method signatures remain stable
// These tests will fail if interface methods are changed, preventing accidental breaks

func TestUpstreamManagerContract(t *testing.T) {
	// Verify UpstreamManager interface contract
	interfaceType := reflect.TypeOf((*UpstreamManager)(nil)).Elem()

	// Expected method signatures for UpstreamManager
	expectedMethods := map[string]methodSignature{
		"AddServerConfig": {
			name: "AddServerConfig",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*config.ServerConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"AddServer": {
			name: "AddServer",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*config.ServerConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"RemoveServer": {
			name: "RemoveServer",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{},
		},
		"GetClient": {
			name: "GetClient",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*managed.Client)(nil)), reflect.TypeOf(true)},
		},
		"GetAllClients": {
			name: "GetAllClients",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(map[string]*managed.Client{})},
		},
		"GetAllServerNames": {
			name: "GetAllServerNames",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf([]string{})},
		},
		"ListServers": {
			name: "ListServers",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(map[string]*config.ServerConfig{})},
		},
		"ConnectAll": {
			name: "ConnectAll",
			in:   []reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem()},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"DisconnectAll": {
			name: "DisconnectAll",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"RetryConnection": {
			name: "RetryConnection",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"DiscoverTools": {
			name: "DiscoverTools",
			in:   []reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem()},
			out:  []reflect.Type{reflect.TypeOf([]*config.ToolMetadata{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"CallTool": {
			name: "CallTool",
			in:   []reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reflect.TypeOf(""), reflect.TypeOf(map[string]interface{}{})},
			out:  []reflect.Type{reflect.TypeOf((*interface{})(nil)).Elem(), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetStats": {
			name: "GetStats",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(map[string]interface{}{})},
		},
		"GetTotalToolCount": {
			name: "GetTotalToolCount",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(0)},
		},
		"HasDockerContainers": {
			name: "HasDockerContainers",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(true)},
		},
		"SetLogConfig": {
			name: "SetLogConfig",
			in:   []reflect.Type{reflect.TypeOf((*config.LogConfig)(nil))},
			out:  []reflect.Type{},
		},
		"StartManualOAuth": {
			name: "StartManualOAuth",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(true)},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"AddNotificationHandler": {
			name: "AddNotificationHandler",
			in:   []reflect.Type{reflect.TypeOf((*NotificationHandler)(nil)).Elem()},
			out:  []reflect.Type{},
		},
		"InvalidateAllToolCountCaches": {
			name: "InvalidateAllToolCountCaches",
			in:   []reflect.Type{},
			out:  []reflect.Type{},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

func TestIndexManagerContract(t *testing.T) {
	// Verify IndexManager interface contract
	interfaceType := reflect.TypeOf((*IndexManager)(nil)).Elem()

	expectedMethods := map[string]methodSignature{
		"IndexTool": {
			name: "IndexTool",
			in:   []reflect.Type{reflect.TypeOf((*config.ToolMetadata)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"BatchIndexTools": {
			name: "BatchIndexTools",
			in:   []reflect.Type{reflect.TypeOf([]*config.ToolMetadata{})},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"SearchTools": {
			name: "SearchTools",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)},
			out:  []reflect.Type{reflect.TypeOf([]*config.SearchResult{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Search": {
			name: "Search",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)},
			out:  []reflect.Type{reflect.TypeOf([]*config.SearchResult{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"DeleteTool": {
			name: "DeleteTool",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"DeleteServerTools": {
			name: "DeleteServerTools",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"RebuildIndex": {
			name: "RebuildIndex",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetDocumentCount": {
			name: "GetDocumentCount",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(uint64(0)), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetStats": {
			name: "GetStats",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(map[string]interface{}{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Close": {
			name: "Close",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetToolsByServer": {
			name: "GetToolsByServer",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf([]*config.ToolMetadata{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetAllIndexedServerNames": {
			name: "GetAllIndexedServerNames",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf([]string{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

func TestStorageManagerContract(t *testing.T) {
	// Verify StorageManager interface contract
	interfaceType := reflect.TypeOf((*StorageManager)(nil)).Elem()

	expectedMethods := map[string]methodSignature{
		"SaveUpstreamServer": {
			name: "SaveUpstreamServer",
			in:   []reflect.Type{reflect.TypeOf((*config.ServerConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetUpstreamServer": {
			name: "GetUpstreamServer",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*config.ServerConfig)(nil)), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"ListUpstreamServers": {
			name: "ListUpstreamServers",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf([]*config.ServerConfig{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"ListQuarantinedUpstreamServers": {
			name: "ListQuarantinedUpstreamServers",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf([]*config.ServerConfig{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"DeleteUpstreamServer": {
			name: "DeleteUpstreamServer",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"EnableUpstreamServer": {
			name: "EnableUpstreamServer",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(true)},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"QuarantineUpstreamServer": {
			name: "QuarantineUpstreamServer",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(true)},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"IncrementToolUsage": {
			name: "IncrementToolUsage",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetToolUsage": {
			name: "GetToolUsage",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*storage.ToolStatRecord)(nil)), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetToolStatistics": {
			name: "GetToolStatistics",
			in:   []reflect.Type{reflect.TypeOf(0)},
			out:  []reflect.Type{reflect.TypeOf((*config.ToolStats)(nil)), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"SaveToolHash": {
			name: "SaveToolHash",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetToolHash": {
			name: "GetToolHash",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"HasToolChanged": {
			name: "HasToolChanged",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf(true), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"DeleteToolHash": {
			name: "DeleteToolHash",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Backup": {
			name: "Backup",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetSchemaVersion": {
			name: "GetSchemaVersion",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(uint64(0)), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetStats": {
			name: "GetStats",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(map[string]interface{}{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"ListUpstreams": {
			name: "ListUpstreams",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf([]*config.ServerConfig{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"AddUpstream": {
			name: "AddUpstream",
			in:   []reflect.Type{reflect.TypeOf((*config.ServerConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"RemoveUpstream": {
			name: "RemoveUpstream",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"UpdateUpstream": {
			name: "UpdateUpstream",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*config.ServerConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetToolStats": {
			name: "GetToolStats",
			in:   []reflect.Type{reflect.TypeOf(0)},
			out:  []reflect.Type{reflect.TypeOf([]map[string]interface{}{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"ListQuarantinedTools": {
			name: "ListQuarantinedTools",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf([]map[string]interface{}{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Close": {
			name: "Close",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

func TestOAuthTokenManagerContract(t *testing.T) {
	// Verify OAuthTokenManager interface contract
	interfaceType := reflect.TypeOf((*OAuthTokenManager)(nil)).Elem()

	expectedMethods := map[string]methodSignature{
		"GetOrCreateTokenStore": {
			name: "GetOrCreateTokenStore",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*client.TokenStore)(nil)).Elem()},
		},
		"HasTokenStore": {
			name: "HasTokenStore",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf(true)},
		},
		"SetOAuthCompletionCallback": {
			name: "SetOAuthCompletionCallback",
			in:   []reflect.Type{reflect.TypeOf(func(string) {})},
			out:  []reflect.Type{},
		},
		"NotifyOAuthCompletion": {
			name: "NotifyOAuthCompletion",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{},
		},
		"GetToken": {
			name: "GetToken",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*interface{})(nil)).Elem(), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"SaveToken": {
			name: "SaveToken",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*interface{})(nil)).Elem()},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"ClearToken": {
			name: "ClearToken",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

func TestDockerIsolationManagerContract(t *testing.T) {
	// Verify DockerIsolationManager interface contract
	interfaceType := reflect.TypeOf((*DockerIsolationManager)(nil)).Elem()

	expectedMethods := map[string]methodSignature{
		"ShouldIsolate": {
			name: "ShouldIsolate",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf([]string{})},
			out:  []reflect.Type{reflect.TypeOf(true)},
		},
		"IsDockerAvailable": {
			name: "IsDockerAvailable",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(true)},
		},
		"GetDockerIsolationWarning": {
			name: "GetDockerIsolationWarning",
			in:   []reflect.Type{reflect.TypeOf((*config.ServerConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf("")},
		},
		"StartIsolatedCommand": {
			name: "StartIsolatedCommand",
			in:   []reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reflect.TypeOf(""), reflect.TypeOf([]string{}), reflect.TypeOf(map[string]string{}), reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*interface{})(nil)).Elem(), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"StopContainer": {
			name: "StopContainer",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"CleanupContainer": {
			name: "CleanupContainer",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"SetResourceLimits": {
			name: "SetResourceLimits",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetContainerStats": {
			name: "GetContainerStats",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf(map[string]interface{}{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetDefaultImage": {
			name: "GetDefaultImage",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf("")},
		},
		"SetDefaultImages": {
			name: "SetDefaultImages",
			in:   []reflect.Type{reflect.TypeOf(map[string]string{})},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

func TestLogManagerContract(t *testing.T) {
	// Verify LogManager interface contract
	interfaceType := reflect.TypeOf((*LogManager)(nil)).Elem()

	expectedMethods := map[string]methodSignature{
		"GetServerLogger": {
			name: "GetServerLogger",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*zap.Logger)(nil))},
		},
		"GetMainLogger": {
			name: "GetMainLogger",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*zap.Logger)(nil))},
		},
		"CreateLogger": {
			name: "CreateLogger",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*config.LogConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*zap.Logger)(nil))},
		},
		"RotateLogs": {
			name: "RotateLogs",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetLogFiles": {
			name: "GetLogFiles",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf([]string{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetLogContent": {
			name: "GetLogContent",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)},
			out:  []reflect.Type{reflect.TypeOf([]string{}), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"SetLogLevel": {
			name: "SetLogLevel",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetLogLevel": {
			name: "GetLogLevel",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf("")},
		},
		"UpdateLogConfig": {
			name: "UpdateLogConfig",
			in:   []reflect.Type{reflect.TypeOf((*config.LogConfig)(nil))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Sync": {
			name: "Sync",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Close": {
			name: "Close",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

func TestCacheManagerContract(t *testing.T) {
	// Verify CacheManager interface contract
	interfaceType := reflect.TypeOf((*CacheManager)(nil)).Elem()

	expectedMethods := map[string]methodSignature{
		"Get": {
			name: "Get",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*interface{})(nil)).Elem(), reflect.TypeOf(true)},
		},
		"Set": {
			name: "Set",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf((*interface{})(nil)).Elem(), reflect.TypeOf(time.Duration(0))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Delete": {
			name: "Delete",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Clear": {
			name: "Clear",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetStats": {
			name: "GetStats",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(map[string]interface{}{})},
		},
		"GetHitRate": {
			name: "GetHitRate",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf(float64(0))},
		},
		"SetTTL": {
			name: "SetTTL",
			in:   []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(time.Duration(0))},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"GetTTL": {
			name: "GetTTL",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf(time.Duration(0)), reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Expire": {
			name: "Expire",
			in:   []reflect.Type{reflect.TypeOf("")},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
		"Close": {
			name: "Close",
			in:   []reflect.Type{},
			out:  []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		},
	}

	verifyInterfaceContract(t, interfaceType, expectedMethods)
}

// Helper types and functions for contract verification

type methodSignature struct {
	name string
	in   []reflect.Type
	out  []reflect.Type
}

func verifyInterfaceContract(t *testing.T, interfaceType reflect.Type, expectedMethods map[string]methodSignature) {
	t.Helper()

	// Check that interface has expected number of methods
	assert.Equal(t, len(expectedMethods), interfaceType.NumMethod(),
		"Interface %s should have %d methods", interfaceType.Name(), len(expectedMethods))

	// Check each method exists and has correct signature
	for i := 0; i < interfaceType.NumMethod(); i++ {
		method := interfaceType.Method(i)
		methodName := method.Name

		expectedSig, exists := expectedMethods[methodName]
		if !assert.True(t, exists, "Method %s not found in expected methods", methodName) {
			continue
		}

		// Check input parameters
		expectedInCount := len(expectedSig.in)
		actualInCount := method.Type.NumIn()
		assert.Equal(t, expectedInCount, actualInCount,
			"Method %s should have %d input parameters, got %d", methodName, expectedInCount, actualInCount)

		for j := 0; j < expectedInCount && j < actualInCount; j++ {
			expectedType := expectedSig.in[j]
			actualType := method.Type.In(j)
			assert.Equal(t, expectedType, actualType,
				"Method %s parameter %d should be %s, got %s", methodName, j, expectedType, actualType)
		}

		// Check output parameters
		expectedOutCount := len(expectedSig.out)
		actualOutCount := method.Type.NumOut()
		assert.Equal(t, expectedOutCount, actualOutCount,
			"Method %s should have %d output parameters, got %d", methodName, expectedOutCount, actualOutCount)

		for j := 0; j < expectedOutCount && j < actualOutCount; j++ {
			expectedType := expectedSig.out[j]
			actualType := method.Type.Out(j)
			assert.Equal(t, expectedType, actualType,
				"Method %s return %d should be %s, got %s", methodName, j, expectedType, actualType)
		}
	}

	// Verify no unexpected methods exist
	for i := 0; i < interfaceType.NumMethod(); i++ {
		method := interfaceType.Method(i)
		_, exists := expectedMethods[method.Name]
		assert.True(t, exists, "Unexpected method %s found in interface %s", method.Name, interfaceType.Name())
	}
}
