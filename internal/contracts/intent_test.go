package contracts

import (
	"testing"

	"mcpproxy-go/internal/config"
)

func TestIntentDeclaration_Validate(t *testing.T) {
	tests := []struct {
		name        string
		intent      IntentDeclaration
		wantErr     bool
		wantErrCode string
	}{
		{
			name: "valid read intent",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			wantErr: false,
		},
		{
			name: "valid write intent",
			intent: IntentDeclaration{
				OperationType: OperationTypeWrite,
			},
			wantErr: false,
		},
		{
			name: "valid destructive intent",
			intent: IntentDeclaration{
				OperationType: OperationTypeDestructive,
			},
			wantErr: false,
		},
		{
			name: "valid intent with all fields",
			intent: IntentDeclaration{
				OperationType:   OperationTypeWrite,
				DataSensitivity: DataSensitivityPrivate,
				Reason:          "User requested update",
			},
			wantErr: false,
		},
		{
			name: "missing operation_type",
			intent: IntentDeclaration{
				OperationType: "",
			},
			wantErr:     true,
			wantErrCode: IntentErrorCodeMissingOperationType,
		},
		{
			name: "invalid operation_type",
			intent: IntentDeclaration{
				OperationType: "unknown",
			},
			wantErr:     true,
			wantErrCode: IntentErrorCodeInvalidOperationType,
		},
		{
			name: "case sensitive operation_type - uppercase fails",
			intent: IntentDeclaration{
				OperationType: "READ",
			},
			wantErr:     true,
			wantErrCode: IntentErrorCodeInvalidOperationType,
		},
		{
			name: "invalid data_sensitivity",
			intent: IntentDeclaration{
				OperationType:   OperationTypeRead,
				DataSensitivity: "secret",
			},
			wantErr:     true,
			wantErrCode: IntentErrorCodeInvalidSensitivity,
		},
		{
			name: "valid data_sensitivity - public",
			intent: IntentDeclaration{
				OperationType:   OperationTypeRead,
				DataSensitivity: DataSensitivityPublic,
			},
			wantErr: false,
		},
		{
			name: "valid data_sensitivity - internal",
			intent: IntentDeclaration{
				OperationType:   OperationTypeRead,
				DataSensitivity: DataSensitivityInternal,
			},
			wantErr: false,
		},
		{
			name: "valid data_sensitivity - unknown",
			intent: IntentDeclaration{
				OperationType:   OperationTypeRead,
				DataSensitivity: DataSensitivityUnknown,
			},
			wantErr: false,
		},
		{
			name: "reason at max length",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
				Reason:        string(make([]byte, MaxReasonLength)),
			},
			wantErr: false,
		},
		{
			name: "reason exceeds max length",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
				Reason:        string(make([]byte, MaxReasonLength+1)),
			},
			wantErr:     true,
			wantErrCode: IntentErrorCodeReasonTooLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.intent.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && err.Code != tt.wantErrCode {
				t.Errorf("Validate() error code = %v, wantErrCode %v", err.Code, tt.wantErrCode)
			}
		})
	}
}

func TestIntentDeclaration_ValidateForToolVariant(t *testing.T) {
	tests := []struct {
		name        string
		intent      IntentDeclaration
		toolVariant string
		wantErr     bool
		wantErrCode string
	}{
		{
			name: "read intent with call_tool_read - matches",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantRead,
			wantErr:     false,
		},
		{
			name: "write intent with call_tool_write - matches",
			intent: IntentDeclaration{
				OperationType: OperationTypeWrite,
			},
			toolVariant: ToolVariantWrite,
			wantErr:     false,
		},
		{
			name: "destructive intent with call_tool_destructive - matches",
			intent: IntentDeclaration{
				OperationType: OperationTypeDestructive,
			},
			toolVariant: ToolVariantDestructive,
			wantErr:     false,
		},
		{
			name: "read intent with call_tool_write - mismatch",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantWrite,
			wantErr:     true,
			wantErrCode: IntentErrorCodeMismatch,
		},
		{
			name: "write intent with call_tool_read - mismatch",
			intent: IntentDeclaration{
				OperationType: OperationTypeWrite,
			},
			toolVariant: ToolVariantRead,
			wantErr:     true,
			wantErrCode: IntentErrorCodeMismatch,
		},
		{
			name: "read intent with call_tool_destructive - mismatch",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantDestructive,
			wantErr:     true,
			wantErrCode: IntentErrorCodeMismatch,
		},
		{
			name: "destructive intent with call_tool_read - mismatch",
			intent: IntentDeclaration{
				OperationType: OperationTypeDestructive,
			},
			toolVariant: ToolVariantRead,
			wantErr:     true,
			wantErrCode: IntentErrorCodeMismatch,
		},
		{
			name: "missing operation_type - validation fails first",
			intent: IntentDeclaration{
				OperationType: "",
			},
			toolVariant: ToolVariantRead,
			wantErr:     true,
			wantErrCode: IntentErrorCodeMissingOperationType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.intent.ValidateForToolVariant(tt.toolVariant)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateForToolVariant() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && err.Code != tt.wantErrCode {
				t.Errorf("ValidateForToolVariant() error code = %v, wantErrCode %v", err.Code, tt.wantErrCode)
			}
		})
	}
}

func TestIntentDeclaration_ValidateAgainstServerAnnotations(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		intent      IntentDeclaration
		toolVariant string
		serverTool  string
		annotations *config.ToolAnnotations
		strict      bool
		wantErr     bool
		wantErrCode string
	}{
		{
			name: "no annotations - always allowed",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantRead,
			serverTool:  "server:tool",
			annotations: nil,
			strict:      true,
			wantErr:     false,
		},
		{
			name: "call_tool_read on readOnlyHint=true - allowed",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantRead,
			serverTool:  "server:read_tool",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &trueVal,
			},
			strict:  true,
			wantErr: false,
		},
		{
			name: "call_tool_read on destructiveHint=true - rejected (strict)",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantRead,
			serverTool:  "github:delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			strict:      true,
			wantErr:     true,
			wantErrCode: IntentErrorCodeServerMismatch,
		},
		{
			name: "call_tool_read on destructiveHint=true - allowed (non-strict)",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantRead,
			serverTool:  "github:delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			strict:  false,
			wantErr: false,
		},
		{
			name: "call_tool_write on destructiveHint=true - rejected (strict)",
			intent: IntentDeclaration{
				OperationType: OperationTypeWrite,
			},
			toolVariant: ToolVariantWrite,
			serverTool:  "github:delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			strict:      true,
			wantErr:     true,
			wantErrCode: IntentErrorCodeServerMismatch,
		},
		{
			name: "call_tool_destructive on destructiveHint=true - always allowed",
			intent: IntentDeclaration{
				OperationType: OperationTypeDestructive,
			},
			toolVariant: ToolVariantDestructive,
			serverTool:  "github:delete_repo",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			strict:  true,
			wantErr: false,
		},
		{
			name: "call_tool_destructive - skips server validation entirely",
			intent: IntentDeclaration{
				OperationType: OperationTypeDestructive,
			},
			toolVariant: ToolVariantDestructive,
			serverTool:  "server:any_tool",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &trueVal,
			},
			strict:  true,
			wantErr: false,
		},
		{
			name: "call_tool_write on readOnlyHint=true - allowed (informational only)",
			intent: IntentDeclaration{
				OperationType: OperationTypeWrite,
			},
			toolVariant: ToolVariantWrite,
			serverTool:  "server:read_only_tool",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &trueVal,
			},
			strict:  true,
			wantErr: false, // Not an error, just informational
		},
		{
			name: "call_tool_read on destructiveHint=false - allowed",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			toolVariant: ToolVariantRead,
			serverTool:  "server:normal_tool",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &falseVal,
			},
			strict:  true,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.intent.ValidateAgainstServerAnnotations(tt.toolVariant, tt.serverTool, tt.annotations, tt.strict)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAgainstServerAnnotations() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && err.Code != tt.wantErrCode {
				t.Errorf("ValidateAgainstServerAnnotations() error code = %v, wantErrCode %v", err.Code, tt.wantErrCode)
			}
		})
	}
}

func TestDeriveCallWith(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		annotations *config.ToolAnnotations
		want        string
	}{
		{
			name:        "nil annotations - defaults to write",
			annotations: nil,
			want:        ToolVariantWrite,
		},
		{
			name:        "empty annotations - defaults to write",
			annotations: &config.ToolAnnotations{},
			want:        ToolVariantWrite,
		},
		{
			name: "destructiveHint=true - returns destructive",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
			},
			want: ToolVariantDestructive,
		},
		{
			name: "readOnlyHint=true - returns read",
			annotations: &config.ToolAnnotations{
				ReadOnlyHint: &trueVal,
			},
			want: ToolVariantRead,
		},
		{
			name: "both hints true - destructive takes priority",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &trueVal,
				ReadOnlyHint:    &trueVal,
			},
			want: ToolVariantDestructive,
		},
		{
			name: "destructiveHint=false, readOnlyHint=true - returns read",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &falseVal,
				ReadOnlyHint:    &trueVal,
			},
			want: ToolVariantRead,
		},
		{
			name: "both hints false - defaults to write",
			annotations: &config.ToolAnnotations{
				DestructiveHint: &falseVal,
				ReadOnlyHint:    &falseVal,
			},
			want: ToolVariantWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveCallWith(tt.annotations)
			if got != tt.want {
				t.Errorf("DeriveCallWith() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIntentDeclaration_ToMap(t *testing.T) {
	tests := []struct {
		name   string
		intent IntentDeclaration
		want   map[string]interface{}
	}{
		{
			name: "only operation_type",
			intent: IntentDeclaration{
				OperationType: OperationTypeRead,
			},
			want: map[string]interface{}{
				"operation_type": OperationTypeRead,
			},
		},
		{
			name: "all fields",
			intent: IntentDeclaration{
				OperationType:   OperationTypeWrite,
				DataSensitivity: DataSensitivityPrivate,
				Reason:          "Test reason",
			},
			want: map[string]interface{}{
				"operation_type":   OperationTypeWrite,
				"data_sensitivity": DataSensitivityPrivate,
				"reason":           "Test reason",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.intent.ToMap()
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("ToMap()[%s] = %v, want %v", k, got[k], v)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("ToMap() length = %d, want %d", len(got), len(tt.want))
			}
		})
	}
}

func TestIntentFromMap(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]interface{}
		want *IntentDeclaration
	}{
		{
			name: "nil map",
			m:    nil,
			want: nil,
		},
		{
			name: "only operation_type",
			m: map[string]interface{}{
				"operation_type": OperationTypeRead,
			},
			want: &IntentDeclaration{
				OperationType: OperationTypeRead,
			},
		},
		{
			name: "all fields",
			m: map[string]interface{}{
				"operation_type":   OperationTypeWrite,
				"data_sensitivity": DataSensitivityPrivate,
				"reason":           "Test reason",
			},
			want: &IntentDeclaration{
				OperationType:   OperationTypeWrite,
				DataSensitivity: DataSensitivityPrivate,
				Reason:          "Test reason",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IntentFromMap(tt.m)
			if tt.want == nil {
				if got != nil {
					t.Errorf("IntentFromMap() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Errorf("IntentFromMap() = nil, want %v", tt.want)
				return
			}
			if got.OperationType != tt.want.OperationType {
				t.Errorf("IntentFromMap().OperationType = %v, want %v", got.OperationType, tt.want.OperationType)
			}
			if got.DataSensitivity != tt.want.DataSensitivity {
				t.Errorf("IntentFromMap().DataSensitivity = %v, want %v", got.DataSensitivity, tt.want.DataSensitivity)
			}
			if got.Reason != tt.want.Reason {
				t.Errorf("IntentFromMap().Reason = %v, want %v", got.Reason, tt.want.Reason)
			}
		})
	}
}
