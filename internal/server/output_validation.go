package server

import (
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/outputvalidation"
)

// ovDecision is the outcome of the pure output-validation decision core.
//
//   - decision == ""        -> no-op: forward the result unchanged.
//   - decision == "warning" -> warn mode violation: forward unchanged, record a policy_decision.
//   - decision == "blocked" -> strict mode violation: block the call, record a policy_decision.
//
// block is true only when the call must be replaced with an error result.
type ovDecision struct {
	decision string
	reason   string
	block    bool
}

// evaluateOutputValidation is the pure decision core of Spec 056 output-schema
// validation. It performs no I/O and no logging so it is fully unit-testable;
// the caller (applyOutputValidation) owns schema lookup, activity logging, and
// error-result construction.
//
// Inputs:
//   - v:            the validator (nil => no-op)
//   - toolKey:      "server:tool", used as the validator's cache key
//   - schemaJSON:   the tool's declared output schema ("" => no-op, FR-A7)
//   - strict:       true in strict mode, false in warn mode
//   - blockMissing: strict-mode posture when structuredContent is absent (FR-A8)
//   - forwarded:    the result whose StructuredContent is validated; never mutated
func evaluateOutputValidation(v *outputvalidation.Validator, toolKey, schemaJSON string, strict, blockMissing bool, forwarded *mcp.CallToolResult) ovDecision {
	if v == nil || schemaJSON == "" || forwarded == nil || forwarded.IsError {
		return ovDecision{} // FR-A7 / FR-A10
	}

	// Declared schema but no structured content (the ContextForge #4042 trap, FR-A8).
	if forwarded.StructuredContent == nil {
		if strict && blockMissing {
			return ovDecision{
				decision: "blocked",
				reason:   "tool declares an output schema but returned no structured content",
				block:    true,
			}
		}
		return ovDecision{} // warn (or strict+allow) forwards unchanged
	}

	verdict := v.Validate(toolKey, schemaJSON, forwarded.StructuredContent)
	if !verdict.IsViolation() {
		return ovDecision{} // conforming, or uncompilable schema degraded to pass (FR-A9)
	}

	if strict {
		return ovDecision{decision: "blocked", reason: verdict.Reason, block: true}
	}
	return ovDecision{decision: "warning", reason: verdict.Reason, block: false}
}
