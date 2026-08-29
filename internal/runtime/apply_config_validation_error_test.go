package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// #1084: a config value the validator rejects is the OPERATOR's mistake, not a
// server fault. ApplyConfig returned the detail only inside the error string,
// so callers could not tell "you sent a bad value" from "we failed to persist a
// good one" without string-matching — and the REST handlers reported every
// apply failure as 500, including a plain enum typo.
//
// The structured errors now ride on the RESULT, which is what lets a caller
// classify without parsing prose.
func TestApplyConfig_ValidationFailureCarriesStructuredErrors(t *testing.T) {
	r := &Runtime{}

	cfg := &config.Config{
		Listen:                 "127.0.0.1:8080",
		DataDir:                t.TempDir(),
		TLS:                    &config.TLSConfig{},
		DirectToolResponseMode: "bogus",
	}

	result, err := r.ApplyConfig(cfg, "")
	require.Error(t, err, "an invalid enum value must fail the apply")
	require.NotNil(t, result, "the result must be returned even on failure — it is what classifies the error")

	assert.False(t, result.Success)
	require.NotEmpty(t, result.ValidationErrors,
		"without these the caller cannot distinguish a bad value from a server fault")

	var found bool
	for _, ve := range result.ValidationErrors {
		if ve.Field == "direct_tool_response_mode" {
			found = true
			assert.Contains(t, ve.Message, "bogus", "the message must name the rejected value")
		}
	}
	assert.True(t, found, "the offending field must be named: %+v", result.ValidationErrors)
}

// The nil-config guard is a genuine caller/programming fault, not operator
// input: it must NOT be dressed up as a validation error, or the handler would
// answer 400 for a server-side bug.
func TestApplyConfig_NilConfigIsNotAValidationError(t *testing.T) {
	r := &Runtime{}
	result, err := r.ApplyConfig(nil, "")
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.ValidationErrors,
		"a nil config is a server-side fault and must stay in the 500 class")
}
