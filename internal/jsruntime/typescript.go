package jsruntime

import (
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// SupportedLanguages lists all valid language values for code execution.
var SupportedLanguages = []string{"javascript", "typescript"}

// compactTypeScriptTokens is the token count (typeScriptSourceTokens) above
// which esbuild prints its output without whitespace. esbuild indents every
// nested line, so its readable output grows with the square of the nesting
// depth: 35,000 nested blocks (70 KB of source, within maxSourceTokens) came
// out as 2.4 GB and took 5.4 GB of memory. The token count bounds the
// nesting depth, so up to this count the readable output stays small (a few
// MB at most) and ordinary scripts keep it.
const compactTypeScriptTokens = 4096

// TranspileTypeScript transpiles TypeScript code to JavaScript using esbuild.
// It performs type-stripping only (no type checking or semantic validation).
// Returns the transpiled JavaScript code on success, or a JsError on failure.
//
// esbuild's parser recurses on the Go stack once per nesting level, before
// anything else can stop it, so source that could overflow it is refused
// first (typeScriptSourceTokens, source_guard.go) with the same SYNTAX_ERROR
// as JavaScript source over the limit.
func TranspileTypeScript(code string) (string, *JsError) {
	tokens := typeScriptSourceTokens(code, maxSourceTokens)
	if tokens > maxSourceTokens {
		return "", NewJsError(ErrorCodeSyntaxError, "RangeError: "+sourceTooDeepError().Error())
	}
	result := api.Transform(code, api.TransformOptions{
		Loader:           api.LoaderTS,
		MinifyWhitespace: tokens > compactTypeScriptTokens,
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
