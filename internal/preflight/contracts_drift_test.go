package preflight_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// The taxonomy lives once, in internal/preflight. internal/contracts mirrors it
// for the wire (and, through cmd/generate-types, for the frontend). This test is
// the fence: a code added on one side without the other fails here rather than
// in production, where a UI would render an unknown badge or the OpenAPI enum
// would omit a real verdict.
func TestContractsMirrorsTheReasonEnum(t *testing.T) {
	mirrored := []contracts.PreflightReason{
		contracts.PreflightReasonServerInitializing,
		contracts.PreflightReasonServerUnhealthy,
		contracts.PreflightReasonServerDisabled,
		contracts.PreflightReasonServerQuarantined,
		contracts.PreflightReasonToolPendingApproval,
		contracts.PreflightReasonToolChanged,
		contracts.PreflightReasonToolBlockedByUser,
		contracts.PreflightReasonOAuthRequired,
		contracts.PreflightReasonHashMismatch,
		contracts.PreflightReasonServerNotInScope,
		contracts.PreflightReasonToolDeniedByConfig,
		contracts.PreflightReasonMissingAnnotation,
		contracts.PreflightReasonPolicyFiltered,
		contracts.PreflightReasonNotFound,
		contracts.PreflightReasonServerNotConfigured,
	}

	wire := make(map[string]bool, len(mirrored))
	for _, code := range mirrored {
		assert.True(t, preflight.ValidReason(code), "contracts exposes %q, which the evaluator does not know", code)
		wire[code] = true
	}

	evaluator := make(map[string]bool)
	for _, code := range preflight.AllReasons() {
		evaluator[code] = true
	}
	assert.Equal(t, evaluator, wire, "internal/preflight and internal/contracts must expose the same closed enum")
}

func TestContractsMirrorsStatusesAndVerdicts(t *testing.T) {
	assert.Equal(t, preflight.StatusReady, contracts.PreflightStatusReady)
	assert.Equal(t, preflight.StatusUnavailable, contracts.PreflightStatusUnavailable)

	assert.Equal(t, preflight.VerdictReady, contracts.PreflightVerdictReady)
	assert.Equal(t, preflight.VerdictDegradedRetryable, contracts.PreflightVerdictDegradedRetryable)
	assert.Equal(t, preflight.VerdictBlocked, contracts.PreflightVerdictBlocked)
	assert.Equal(t, preflight.VerdictUnknownIDs, contracts.PreflightVerdictUnknownIDs)
}

// The generated TypeScript is the third copy. Reading the committed file keeps
// the frontend union honest without a Node toolchain in the Go test suite.
func TestGeneratedTypeScriptCarriesEveryReason(t *testing.T) {
	data, err := os.ReadFile("../../frontend/src/types/contracts.ts")
	require.NoError(t, err, "run: go run ./cmd/generate-types")
	ts := string(data)

	for _, code := range preflight.AllReasons() {
		assert.True(t, strings.Contains(ts, "'"+code+"'"),
			"frontend/src/types/contracts.ts is missing reason %q — add it to cmd/generate-types and regenerate", code)
	}
	for _, name := range []string{"PreflightRequest", "PreflightResponse", "PreflightToolResult", "PreflightToolRef", "PreflightPolicy"} {
		assert.True(t, strings.Contains(ts, name), "generated types are missing %s", name)
	}
}
