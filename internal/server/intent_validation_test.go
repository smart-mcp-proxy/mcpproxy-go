package server

import (
	"testing"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"

	"github.com/mark3labs/mcp-go/mcp"
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
		name      string
		request   mcp.CallToolRequest
		wantNil   bool
		wantOpTyp string
		wantErr   bool
	}{
		{
			name: "valid intent object",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"intent": map[string]interface{}{
							"operation_type": "read",
						},
					},
				},
			},
			wantNil:   false,
			wantOpTyp: "read",
			wantErr:   false,
		},
		{
			name: "intent with all fields",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"intent": map[string]interface{}{
							"operation_type":   "write",
							"data_sensitivity": "private",
							"reason":           "test reason",
						},
					},
				},
			},
			wantNil:   false,
			wantOpTyp: "write",
			wantErr:   false,
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
			name: "intent not an object - error",
			request: mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "test",
					Arguments: map[string]interface{}{
						"intent": "not an object",
					},
				},
			},
			wantNil: true,
			wantErr: true,
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

			if intent.OperationType != tt.wantOpTyp {
				t.Errorf("extractIntent().OperationType = %v, want %v", intent.OperationType, tt.wantOpTyp)
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
	}{
		{
			name:        "nil intent - error",
			intent:      nil,
			toolVariant: contracts.ToolVariantRead,
			wantErr:     true,
		},
		{
			name: "matching read intent",
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeRead,
			},
			toolVariant: contracts.ToolVariantRead,
			wantErr:     false,
		},
		{
			name: "matching write intent",
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeWrite,
			},
			toolVariant: contracts.ToolVariantWrite,
			wantErr:     false,
		},
		{
			name: "matching destructive intent",
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeDestructive,
			},
			toolVariant: contracts.ToolVariantDestructive,
			wantErr:     false,
		},
		{
			name: "mismatched intent - read declared, write variant",
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeRead,
			},
			toolVariant: contracts.ToolVariantWrite,
			wantErr:     true,
		},
		{
			name: "mismatched intent - write declared, read variant",
			intent: &contracts.IntentDeclaration{
				OperationType: contracts.OperationTypeWrite,
			},
			toolVariant: contracts.ToolVariantRead,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := proxy.validateIntentForVariant(tt.intent, tt.toolVariant)

			if (result != nil) != tt.wantErr {
				t.Errorf("validateIntentForVariant() error = %v, wantErr %v", result != nil, tt.wantErr)
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
