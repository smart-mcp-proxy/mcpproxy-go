package jsruntime

import (
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// SupportedLanguages lists all valid language values for code execution.
var SupportedLanguages = []string{"javascript", "typescript"}

// TranspileTypeScript transpiles TypeScript code to JavaScript using esbuild.
// It performs type-stripping only (no type checking or semantic validation).
// Returns the transpiled JavaScript code on success, or a JsError on failure.
func TranspileTypeScript(code string) (string, *JsError) {
	result := api.Transform(code, api.TransformOptions{
		Loader: api.LoaderTS,
	})

	if len(result.Errors) > 0 {
		// Build error message from esbuild errors with location info
		var messages []string
		for _, err := range result.Errors {
			if err.Location != nil {
				messages = append(messages, fmt.Sprintf(
					"line %d, column %d: %s",
					err.Location.Line,
					err.Location.Column,
					err.Text,
				))
			} else {
				messages = append(messages, err.Text)
			}
		}
		errMsg := fmt.Sprintf("TypeScript transpilation failed: %s", strings.Join(messages, "; "))
		return "", NewJsError(ErrorCodeTranspileError, errMsg)
	}

	return string(result.Code), nil
}

// ValidateLanguage checks if the given language string is supported.
// Returns nil if valid, or a JsError if not.
func ValidateLanguage(language string) *JsError {
	switch language {
	case "", "javascript", "typescript":
		return nil
	default:
		return NewJsError(
			ErrorCodeInvalidLanguage,
			fmt.Sprintf("Unsupported language %q. Supported languages: %s",
				language, strings.Join(SupportedLanguages, ", ")),
		)
	}
}
