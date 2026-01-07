package main

import (
	"errors"
	"fmt"

	"mcpproxy-go/internal/cliclient"
)

// cmd_helpers.go provides type-safe helper functions for extracting fields
// from JSON-decoded map[string]interface{} responses from the MCPProxy API.
//
// These functions are used throughout the CLI commands to safely extract
// typed values from API responses, handling missing keys and type mismatches
// gracefully by returning zero values.

// formatErrorWithRequestID formats an error for CLI output, including request_id if available.
// T023: Helper for CLI error display with request ID suggestion.
func formatErrorWithRequestID(err error) string {
	if err == nil {
		return ""
	}

	// Check if the error is an APIError with request_id
	var apiErr *cliclient.APIError
	if errors.As(err, &apiErr) && apiErr.HasRequestID() {
		return apiErr.FormatWithRequestID()
	}

	// Fall back to standard error message
	return err.Error()
}

// cliError returns a formatted error suitable for CLI output.
// It includes request_id when available from API errors.
// T023: Wrapper for CLI commands to display errors with request ID.
func cliError(prefix string, err error) error {
	formattedMsg := formatErrorWithRequestID(err)
	return fmt.Errorf("%s: %s", prefix, formattedMsg)
}

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getIntField(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}

func getArrayField(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key]; ok && v != nil {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

func getStringArrayField(m map[string]interface{}, key string) []string {
	if v, ok := m[key]; ok && v != nil {
		if arr, ok := v.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	return nil
}

func getMapField(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok && v != nil {
		if mm, ok := v.(map[string]interface{}); ok {
			return mm
		}
	}
	return nil
}
