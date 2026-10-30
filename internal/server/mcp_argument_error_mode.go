package server

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// Parse the management argument strictly even when a caller bypasses schema
// validation. GetString alone would silently discard a non-string setting.
func argumentErrorModeFromRequest(request mcp.CallToolRequest) (string, error) {
	raw, present := request.GetArguments()["tool_argument_error_mode"]
	if !present {
		return "", nil
	}
	mode, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("tool_argument_error_mode must be a string")
	}
	if err := config.ValidateToolArgumentErrorMode(mode); err != nil {
		return "", err
	}
	return mode, nil
}

// argumentErrorDetail applies one dispatch's captured policy and reports the
// recognized argument-error detail. Only an explicit redacted policy may
// classify the cause of the manager's exact top-level generic wrapper; the
// default/detailed classification is the classifier's unchanged input.
func argumentErrorDetail(err error, mode string) (string, bool) {
	if mode == config.ToolArgumentErrorModeRedacted {
		if cause, trusted := upstream.GenericToolCallCause(err); trusted && !isKnownSDKNonArgumentError(cause) {
			err = cause
		}
	}
	return classifyUpstreamInvalidParams(err)
}
