package contracts

import (
	"fmt"
	"strings"

	"mcpproxy-go/internal/config"
)

// Operation type constants for intent declaration
const (
	OperationTypeRead        = "read"
	OperationTypeWrite       = "write"
	OperationTypeDestructive = "destructive"
)

// ValidOperationTypes contains all valid operation_type values
var ValidOperationTypes = []string{
	OperationTypeRead,
	OperationTypeWrite,
	OperationTypeDestructive,
}

// Data sensitivity constants for intent declaration
const (
	DataSensitivityPublic   = "public"
	DataSensitivityInternal = "internal"
	DataSensitivityPrivate  = "private"
	DataSensitivityUnknown  = "unknown"
)

// ValidDataSensitivities contains all valid data_sensitivity values
var ValidDataSensitivities = []string{
	DataSensitivityPublic,
	DataSensitivityInternal,
	DataSensitivityPrivate,
	DataSensitivityUnknown,
}

// Tool variant constants for intent-based tool calling
const (
	ToolVariantRead        = "call_tool_read"
	ToolVariantWrite       = "call_tool_write"
	ToolVariantDestructive = "call_tool_destructive"
)

// ToolVariantToOperationType maps tool variants to their expected operation types
var ToolVariantToOperationType = map[string]string{
	ToolVariantRead:        OperationTypeRead,
	ToolVariantWrite:       OperationTypeWrite,
	ToolVariantDestructive: OperationTypeDestructive,
}

// MaxReasonLength is the maximum allowed length for the reason field
const MaxReasonLength = 1000

// IntentDeclaration represents the agent's declared intent for a tool call.
// This enables the two-key security model where intent must be declared both
// in tool selection (call_tool_read/write/destructive) and in this parameter.
type IntentDeclaration struct {
	// OperationType is REQUIRED and must match the tool variant used.
	// Valid values: "read", "write", "destructive"
	OperationType string `json:"operation_type"`

	// DataSensitivity is optional classification of data being accessed/modified.
	// Valid values: "public", "internal", "private", "unknown"
	// Default: "unknown" if not provided
	DataSensitivity string `json:"data_sensitivity,omitempty"`

	// Reason is optional human-readable explanation for the operation.
	// Max length: 1000 characters
	Reason string `json:"reason,omitempty"`
}

// IntentValidationError represents intent validation failures
type IntentValidationError struct {
	Code    string                 `json:"code"`    // Error code for programmatic handling
	Message string                 `json:"message"` // Human-readable error message
	Details map[string]interface{} `json:"details"` // Additional context
}

// Error codes for intent validation
const (
	IntentErrorCodeMissing             = "MISSING_INTENT"
	IntentErrorCodeMissingOperationType = "MISSING_OPERATION_TYPE"
	IntentErrorCodeInvalidOperationType = "INVALID_OPERATION_TYPE"
	IntentErrorCodeMismatch            = "INTENT_MISMATCH"
	IntentErrorCodeServerMismatch      = "SERVER_MISMATCH"
	IntentErrorCodeInvalidSensitivity  = "INVALID_SENSITIVITY"
	IntentErrorCodeReasonTooLong       = "REASON_TOO_LONG"
)

// Error implements the error interface
func (e *IntentValidationError) Error() string {
	return e.Message
}

// NewIntentValidationError creates a new IntentValidationError
func NewIntentValidationError(code, message string, details map[string]interface{}) *IntentValidationError {
	return &IntentValidationError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// Validate validates the IntentDeclaration fields
func (i *IntentDeclaration) Validate() *IntentValidationError {
	// Check operation_type is present
	if i.OperationType == "" {
		return NewIntentValidationError(
			IntentErrorCodeMissingOperationType,
			"intent.operation_type is required",
			nil,
		)
	}

	// Check operation_type is valid
	if !isValidOperationType(i.OperationType) {
		return NewIntentValidationError(
			IntentErrorCodeInvalidOperationType,
			fmt.Sprintf("Invalid intent.operation_type '%s': must be read, write, or destructive", i.OperationType),
			map[string]interface{}{
				"provided":       i.OperationType,
				"valid_values":   ValidOperationTypes,
			},
		)
	}

	// Check data_sensitivity if provided
	if i.DataSensitivity != "" && !isValidDataSensitivity(i.DataSensitivity) {
		return NewIntentValidationError(
			IntentErrorCodeInvalidSensitivity,
			fmt.Sprintf("Invalid intent.data_sensitivity '%s': must be public, internal, private, or unknown", i.DataSensitivity),
			map[string]interface{}{
				"provided":       i.DataSensitivity,
				"valid_values":   ValidDataSensitivities,
			},
		)
	}

	// Check reason length
	if len(i.Reason) > MaxReasonLength {
		return NewIntentValidationError(
			IntentErrorCodeReasonTooLong,
			fmt.Sprintf("intent.reason exceeds maximum length of %d characters", MaxReasonLength),
			map[string]interface{}{
				"provided_length": len(i.Reason),
				"max_length":      MaxReasonLength,
			},
		)
	}

	return nil
}

// ValidateForToolVariant validates that the intent matches the tool variant
func (i *IntentDeclaration) ValidateForToolVariant(toolVariant string) *IntentValidationError {
	// First validate the intent itself
	if err := i.Validate(); err != nil {
		return err
	}

	// Get expected operation type for this tool variant
	expectedOpType, ok := ToolVariantToOperationType[toolVariant]
	if !ok {
		return NewIntentValidationError(
			IntentErrorCodeMismatch,
			fmt.Sprintf("Unknown tool variant: %s", toolVariant),
			map[string]interface{}{
				"tool_variant": toolVariant,
			},
		)
	}

	// Check two-key match: intent.operation_type must match tool variant
	if i.OperationType != expectedOpType {
		return NewIntentValidationError(
			IntentErrorCodeMismatch,
			fmt.Sprintf("Intent mismatch: tool is %s but intent declares %s", toolVariant, i.OperationType),
			map[string]interface{}{
				"tool_variant":       toolVariant,
				"expected_operation": expectedOpType,
				"declared_operation": i.OperationType,
			},
		)
	}

	return nil
}

// ValidateAgainstServerAnnotations validates intent against server-provided annotations
func (i *IntentDeclaration) ValidateAgainstServerAnnotations(toolVariant, serverTool string, annotations *config.ToolAnnotations, strict bool) *IntentValidationError {
	// call_tool_destructive is the most permissive - skip server validation
	if toolVariant == ToolVariantDestructive {
		return nil
	}

	// No annotations means no server-side hints to validate against
	if annotations == nil {
		return nil
	}

	// Check for destructive tool being called with non-destructive variant
	if annotations.DestructiveHint != nil && *annotations.DestructiveHint {
		if toolVariant == ToolVariantRead || toolVariant == ToolVariantWrite {
			if strict {
				return NewIntentValidationError(
					IntentErrorCodeServerMismatch,
					fmt.Sprintf("Tool '%s' is marked destructive by server, use call_tool_destructive", serverTool),
					map[string]interface{}{
						"server_tool":      serverTool,
						"destructive_hint": true,
						"tool_variant":     toolVariant,
						"recommended":      ToolVariantDestructive,
					},
				)
			}
			// Non-strict mode: return nil but caller should log warning
		}
	}

	// Note: A write variant calling a read-only tool is allowed (informational mismatch)
	// The caller (server/mcp.go:validateIntentAgainstServer) may log a warning for this case

	return nil
}

// DeriveCallWith derives the recommended tool variant from server annotations
func DeriveCallWith(annotations *config.ToolAnnotations) string {
	if annotations != nil {
		if annotations.DestructiveHint != nil && *annotations.DestructiveHint {
			return ToolVariantDestructive
		}
		if annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint {
			return ToolVariantRead
		}
	}
	return ToolVariantWrite // Safe default
}

// isValidOperationType checks if the operation type is valid
func isValidOperationType(opType string) bool {
	for _, valid := range ValidOperationTypes {
		if strings.EqualFold(opType, valid) {
			return opType == valid // Case-sensitive match required
		}
	}
	return false
}

// isValidDataSensitivity checks if the data sensitivity is valid
func isValidDataSensitivity(sensitivity string) bool {
	for _, valid := range ValidDataSensitivities {
		if strings.EqualFold(sensitivity, valid) {
			return sensitivity == valid // Case-sensitive match required
		}
	}
	return false
}

// ToMap converts IntentDeclaration to a map for storage in metadata
func (i *IntentDeclaration) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"operation_type": i.OperationType,
	}
	if i.DataSensitivity != "" {
		m["data_sensitivity"] = i.DataSensitivity
	}
	if i.Reason != "" {
		m["reason"] = i.Reason
	}
	return m
}

// IntentFromMap creates an IntentDeclaration from a map
func IntentFromMap(m map[string]interface{}) *IntentDeclaration {
	if m == nil {
		return nil
	}

	intent := &IntentDeclaration{}

	if opType, ok := m["operation_type"].(string); ok {
		intent.OperationType = opType
	}
	if sensitivity, ok := m["data_sensitivity"].(string); ok {
		intent.DataSensitivity = sensitivity
	}
	if reason, ok := m["reason"].(string); ok {
		intent.Reason = reason
	}

	return intent
}
