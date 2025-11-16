package jsruntime

import "fmt"

// ErrorCode represents specific JavaScript execution error types
type ErrorCode string

const (
	// ErrorCodeSyntaxError indicates invalid JavaScript syntax
	ErrorCodeSyntaxError ErrorCode = "SYNTAX_ERROR"

	// ErrorCodeRuntimeError indicates JavaScript runtime exception
	ErrorCodeRuntimeError ErrorCode = "RUNTIME_ERROR"

	// ErrorCodeTimeout indicates execution exceeded timeout limit
	ErrorCodeTimeout ErrorCode = "TIMEOUT"

	// ErrorCodeMaxToolCallsExceeded indicates max_tool_calls limit reached
	ErrorCodeMaxToolCallsExceeded ErrorCode = "MAX_TOOL_CALLS_EXCEEDED"

	// ErrorCodeServerNotAllowed indicates call_tool attempted to call disallowed server
	ErrorCodeServerNotAllowed ErrorCode = "SERVER_NOT_ALLOWED"

	// ErrorCodeSerializationError indicates result cannot be JSON-serialized
	ErrorCodeSerializationError ErrorCode = "SERIALIZATION_ERROR"
)

// JsError represents a JavaScript execution error with message, stack trace, and error code
type JsError struct {
	Message string    `json:"message"`
	Stack   string    `json:"stack,omitempty"`
	Code    ErrorCode `json:"code"`
}

// Error implements the error interface
func (e *JsError) Error() string {
	if e.Stack != "" {
		return fmt.Sprintf("%s: %s\n%s", e.Code, e.Message, e.Stack)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewJsError creates a new JsError with the given code and message
func NewJsError(code ErrorCode, message string) *JsError {
	return &JsError{
		Code:    code,
		Message: message,
	}
}

// NewJsErrorWithStack creates a new JsError with code, message, and stack trace
func NewJsErrorWithStack(code ErrorCode, message, stack string) *JsError {
	return &JsError{
		Code:    code,
		Message: message,
		Stack:   stack,
	}
}

// Result represents the outcome of JavaScript execution
type Result struct {
	// Ok indicates success (true) or error (false)
	Ok bool `json:"ok"`

	// Value contains the JavaScript return value if Ok=true (must be JSON-serializable)
	Value interface{} `json:"value,omitempty"`

	// Error contains error details if Ok=false
	Error *JsError `json:"error,omitempty"`
}

// NewSuccessResult creates a Result with ok=true and the given value
func NewSuccessResult(value interface{}) *Result {
	return &Result{
		Ok:    true,
		Value: value,
	}
}

// NewErrorResult creates a Result with ok=false and the given error
func NewErrorResult(err *JsError) *Result {
	return &Result{
		Ok:    false,
		Error: err,
	}
}
