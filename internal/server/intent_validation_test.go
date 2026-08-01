package server

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMCPProxyServer_extractIntent(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.DefaultConfig()

	proxy := &MCPProxyServer{
		logger: logger,
		config: cfg,
	}

	tests := []struct {
		name            string
		request         mcp.CallToolRequest
		wantNil         bool
		wantSensitivity string
		wantReason      string
		wantErr         bool
	}{
		{
			name: "flat intent with data_sensitivity only",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"intent_data_sensitivity": "private",
					},
				},
			},
			wantNil:         false,
			wantSensitivity: "private",
			wantReason:      "",
			wantErr:         false,
		},
		{
			name: "flat intent with reason only",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"intent_reason": "test reason",
					},
				},
			},
			wantNil:         false,
			wantSensitivity: "",
			wantReason:      "test reason",
			wantErr:         false,
		},
		{
			name: "flat intent with all fields",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"intent_data_sensitivity": "internal",
						"intent_reason":           "user requested update",
					},
				},
			},
			wantNil:         false,
			wantSensitivity: "internal",
			wantReason:      "user requested update",
			wantErr:         false,
		},
		{
			name: "no intent - nil arguments",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      "test",
					Arguments: nil,
				},
			},
			wantNil: true,
			wantErr: false,
		},
		{
			name: "no intent - empty arguments",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      "test",
					Arguments: map[string]interface{}{},
				},
			},
			wantNil: true,
			wantErr: false,
		},
		{
			name: "no intent - only other args present",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"name":      "github:list_repos",
						"args_json": "{}",
					},
				},
			},
			wantNil: true,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent, err := proxy.extractIntent(tt.request)

			if (err != nil) != tt.wantErr {
				t.Errorf("extractIntent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantNil {
				if intent != nil && !tt.wantErr {
					t.Errorf("extractIntent() = %v, want nil", intent)
				}
				return
			}

			if intent == nil {
				t.Errorf("extractIntent() = nil, want non-nil")
				return
			}

			if intent.DataSensitivity != tt.wantSensitivity {
				t.Errorf("extractIntent().DataSensitivity = %v, want %v", intent.DataSensitivity, tt.wantSensitivity)
			}
			if intent.Reason != tt.wantReason {
				t.Errorf("extractIntent().Reason = %v, want %v", intent.Reason, tt.wantReason)
			}
		})
	}
}

func TestMCPProxyServer_validateIntentForVariant(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.DefaultConfig()

	proxy := &MCPProxyServer{
		logger: logger,
		config: cfg,
	}

	tests := []struct {
		name        string
		intent      *contracts.IntentDeclaration
		toolVariant string
		wantErr     bool
		wantOpType  string // expected operation_type after inference
	}{
		{
			name:        "nil intent - creates default with inferred operation_type",
			intent:      nil,
			toolVariant: contracts.ToolVariantRead,
			wantErr:     false,
			wantOpType:  contracts.OperationTypeRead,
		},
		{
			name:        "empty intent - infers operation_type from read variant",
			intent:      &contracts.IntentDeclaration{},
			toolVariant: contracts.ToolVariantRead,
			wantErr:     false,
			wantOpType:  contracts.OperationTypeRead,
		},
		{
			name:        "empty intent - infers operation_type from write variant",
			intent:      &contracts.IntentDeclaration{},
			toolVariant: contracts.ToolVariantWrite,
			wantErr:     false,
			wantOpType:  contracts.OperationTypeWrite,
		},
		{
			name:        "empty intent - infers operation_type from destructive variant",
			intent:      &contracts.IntentDeclaration{},
			toolVariant: contracts.ToolVariantDestructive,
			wantErr:     false,
			wantOpType:  contracts.OperationTypeDestructive,
		},
		{
			name: "intent with optional fields - operation_type inferred",
			intent: &contracts.IntentDeclaration{
				DataSensitivity: "private",
				Reason:          "test reason",
			},
			toolVariant: contracts.ToolVariantWrite,
			wantErr:     false,
			wantOpType:  contracts.OperationTypeWrite,
		},
		{
			name: "intent with invalid data_sensitivity - error",
			intent: &contracts.IntentDeclaration{
				DataSensitivity: "invalid",
			},
			toolVariant: contracts.ToolVariantRead,
			wantErr:     true,
		},
		{
			name:        "unknown tool variant - error",
			intent:      &contracts.IntentDeclaration{},
			toolVariant: "unknown_variant",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent, errResult := proxy.validateIntentForVariant(tt.intent, tt.toolVariant)

			if (errResult != nil) != tt.wantErr {
				t.Errorf("validateIntentForVariant() error = %v, wantErr %v", errResult != nil, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if intent == nil {
					t.Errorf("validateIntentForVariant() returned nil intent, want non-nil")
					return
				}
				if intent.OperationType != tt.wantOpType {
					t.Errorf("validateIntentForVariant() operation_type = %v, want %v", intent.OperationType, tt.wantOpType)
				}
			}
		})
	}
}

func TestMCPProxyServer_validateIntentAgainstServer(t *testing.T) {
	logger := zap.NewNop()

	trueVal := true

	tests := []struct {
		name        string
		strict      bool
		intent      *contracts.IntentDeclaration
		toolVariant string
		serverName  string
		toolName    string
		annotations *config.ToolAnnotations
		wantErr     bool
	}{
		{
			name:   "no annotations - allowed",
			strict: true,
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeRead,
			},
			toolVariant: contracts.ToolVariantRead,
			serverName:  "github",
			toolName:    "get_user",
			annotations: nil,
			wantErr:     false,
		},
		{
			name:   "read on destructive tool - strict rejects",
			strict: true,
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeRead,
			},
			toolVariant: contracts.ToolVariantRead,
			serverName:  "github",
			toolName:    "delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			wantErr: true,
		},
		{
			name:   "read on destructive tool - non-strict allows",
			strict: false,
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeRead,
			},
			toolVariant: contracts.ToolVariantRead,
			serverName:  "github",
			toolName:    "delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			wantErr: false,
		},
		{
			name:   "destructive on destructive tool - always allowed",
			strict: true,
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeDestructive,
			},
			toolVariant: contracts.ToolVariantDestructive,
			serverName:  "github",
			toolName:    "delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.IntentDeclaration = &config.IntentDeclarationConfig{
				StrictServerValidation: tt.strict,
			}

			proxy := &MCPProxyServer{
				logger: logger,
				config: cfg,
			}

			result := proxy.validateIntentAgainstServer(
				tt.intent,
				tt.toolVariant,
				tt.serverName,
				tt.toolName,
				tt.annotations,
			)

			if (result != nil) != tt.wantErr {
				t.Errorf("validateIntentAgainstServer() error = %v, wantErr %v", result != nil, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// Spec 090 (T016): pre-intent-validation blocks carry a request id
// =============================================================================

// This is the earliest policy gate in handleCallToolVariant — it fires before
// the point where the handler used to mint its request id, which is exactly why
// it was the site most likely to emit an anonymous block. The id has to be
// minted above this gate, not at the old spot further down.
func TestCallToolVariant_IntentValidationBlock_CarriesRequestID(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true},
	})
	probe := watchPolicyDecisions(t, rt)

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "call_tool_read",
			Arguments: map[string]interface{}{
				"name": "github:create_issue",
				// Not one of public/internal/private/unknown, so
				// validateIntentForVariant rejects the call.
				"intent_data_sensitivity": "definitely-not-a-sensitivity",
			},
		},
	}

	result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError, "an invalid intent must be rejected")

	payload := probe.awaitOne(t)
	assert.Equal(t, "blocked", payload["decision"])
	assert.Equal(t, "github", payload["server_name"])
	assert.Equal(t, "create_issue", payload["tool_name"])
	requestIDOf(t, payload)
}

// A gate further down the same handler — past the point where the id used to be
// minted — must reuse that one id rather than mint a second, or two gates in one
// dispatch would look like two unrelated requests.
func TestCallToolVariant_CallabilityBlock_CarriesRequestID(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
		{Name: "github", Enabled: true, Quarantined: true},
	})
	probe := watchPolicyDecisions(t, rt)

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "call_tool_read",
			Arguments: map[string]interface{}{
				"name": "github:create_issue",
			},
		},
	}

	result, err := proxy.handleCallToolVariant(context.Background(), request, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError, "a quarantined server's tool is not callable")

	payload := probe.awaitOne(t)
	assert.Equal(t, "blocked", payload["decision"])
	assert.Equal(t, "github", payload["server_name"])
	assert.Contains(t, payload["reason"], "TOOL_BLOCKED")
	requestIDOf(t, payload)
}
