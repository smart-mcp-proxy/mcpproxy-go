package output

// StructuredError represents errors with machine-parseable metadata for AI agent recovery.
type StructuredError struct {
	// Code is a machine-readable error identifier (e.g., "CONFIG_NOT_FOUND")
	Code string `json:"code" yaml:"code"`

	// Message is a human-readable error description
	Message string `json:"message" yaml:"message"`

	// Guidance provides context on why this error occurred
	Guidance string `json:"guidance,omitempty" yaml:"guidance,omitempty"`

	// RecoveryCommand suggests a command to fix the issue
	RecoveryCommand string `json:"recovery_command,omitempty" yaml:"recovery_command,omitempty"`

	// Context contains additional structured data about the error
	Context map[string]interface{} `json:"context,omitempty" yaml:"context,omitempty"`

	// RequestID is the server-generated request ID for log correlation (T023)
	RequestID string `json:"request_id,omitempty" yaml:"request_id,omitempty"`
}

// Error implements the error interface for StructuredError.
func (e StructuredError) Error() string {
	return e.Message
}

// Common error codes for CLI operations
const (
	ErrCodeConfigNotFound      = "CONFIG_NOT_FOUND"
	ErrCodeServerNotFound      = "SERVER_NOT_FOUND"
	ErrCodeDaemonNotRunning    = "DAEMON_NOT_RUNNING"
	ErrCodeInvalidOutputFormat = "INVALID_OUTPUT_FORMAT"
	ErrCodeAuthRequired        = "AUTH_REQUIRED"
	ErrCodeConnectionFailed    = "CONNECTION_FAILED"
	ErrCodeTimeout             = "TIMEOUT"
	ErrCodePermissionDenied    = "PERMISSION_DENIED"
	ErrCodeInvalidInput        = "INVALID_INPUT"
	ErrCodeOperationFailed     = "OPERATION_FAILED"
)

// NewStructuredError creates a new StructuredError with the given code and message.
func NewStructuredError(code, message string) StructuredError {
	return StructuredError{
		Code:    code,
		Message: message,
	}
}

// WithGuidance adds guidance to the error.
func (e StructuredError) WithGuidance(guidance string) StructuredError {
	e.Guidance = guidance
	return e
}

// WithRecoveryCommand adds a recovery command suggestion.
func (e StructuredError) WithRecoveryCommand(cmd string) StructuredError {
	e.RecoveryCommand = cmd
	return e
}

// WithContext adds context data to the error.
func (e StructuredError) WithContext(key string, value interface{}) StructuredError {
	if e.Context == nil {
		e.Context = make(map[string]interface{})
	}
	e.Context[key] = value
	return e
}

// WithRequestID adds a request ID for log correlation (T023).
func (e StructuredError) WithRequestID(requestID string) StructuredError {
	e.RequestID = requestID
	return e
}

// FromError converts a standard error to a StructuredError.
func FromError(err error, code string) StructuredError {
	if se, ok := err.(StructuredError); ok {
		return se
	}
	return StructuredError{
		Code:    code,
		Message: err.Error(),
	}
}
