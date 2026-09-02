package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// POST /api/v1/config/apply takes a FULL document and decodes it into a fresh,
// zero-valued Config (oauth.UnmaskLiveConfigDocument). A body that merely omits
// `activity_max_response_size` therefore arrives with the pointer nil, which
// resolves back to the 64KB default — so an operator's explicit `0` (the
// documented "store responses whole") is deleted from disk and the cap silently
// returns at the next restart.
//
// Round 2 made the field a *int so an explicit 0 SURVIVES a GET → POST round
// trip. It did not make the deletion visible when a client omits the key, and
// deliberately left the activity_* keys out of DetectConfigChanges on the
// grounds that reporting a change with no hot-apply path over-promises.
//
// That reasoning holds for a CHANGED value; it does not hold for the SILENT
// DELETION of an explicitly-set field. The middle path is the one
// code_execution_pool_size already uses in the same function: name the field in
// ChangedFields, set RequiresRestart, and do NOT claim it applied.

func capPtr(v int) *int { return &v }

// The deletion is the whole finding: nothing about the response would have told
// the operator their off switch was gone.
func TestOmittingTheCapReportsTheDeletion(t *testing.T) {
	oldCfg := &config.Config{ActivityMaxResponseSize: capPtr(0)} // explicitly disabled
	newCfg := &config.Config{}                                   // an apply body that omits the key

	result := DetectConfigChanges(oldCfg, newCfg)

	require.NotNil(t, result)
	assert.Contains(t, result.ChangedFields, "activity_max_response_size",
		"deleting an explicitly-set off switch must not be silent")
	assert.True(t, result.RequiresRestart,
		"the cap is read once in runtime.New, so the change is not live")
	assert.NotEqual(t, "No configuration changes detected", result.RestartReason,
		"the apply result must not tell the caller nothing moved")
}

// The reverse direction — turning the cap off — is the same kind of event and
// must be just as visible.
func TestDisablingTheCapIsReported(t *testing.T) {
	oldCfg := &config.Config{}                                   // absent: the 64KB default
	newCfg := &config.Config{ActivityMaxResponseSize: capPtr(0)} // explicitly disabled

	result := DetectConfigChanges(oldCfg, newCfg)

	assert.Contains(t, result.ChangedFields, "activity_max_response_size")
	assert.True(t, result.RequiresRestart)
}

func TestChangingTheCapValueIsReported(t *testing.T) {
	oldCfg := &config.Config{ActivityMaxResponseSize: capPtr(65536)}
	newCfg := &config.Config{ActivityMaxResponseSize: capPtr(4096)}

	result := DetectConfigChanges(oldCfg, newCfg)

	assert.Contains(t, result.ChangedFields, "activity_max_response_size")
}

// Detection is on the RESOLVED value, so materializing the built-in default
// over an absent key is not a change. Comparing the pointers instead would make
// every config rewrite that spells out the defaults demand a restart — the same
// false positive the HTTP-timeout and routing-mode clauses had to be taught to
// avoid.
func TestWritingTheDefaultOutExplicitlyIsNotAChange(t *testing.T) {
	oldCfg := &config.Config{}
	newCfg := &config.Config{ActivityMaxResponseSize: capPtr(config.DefaultActivityMaxResponseSizeBytes)}

	result := DetectConfigChanges(oldCfg, newCfg)

	assert.NotContains(t, result.ChangedFields, "activity_max_response_size")
	assert.False(t, result.RequiresRestart)
}

// A negative value is documented as invalid and resolves to the default, so it
// must compare equal to absent rather than looking like a new setting.
func TestNegativeCapResolvesToTheDefaultAndIsNotAChange(t *testing.T) {
	oldCfg := &config.Config{}
	newCfg := &config.Config{ActivityMaxResponseSize: capPtr(-1)}

	result := DetectConfigChanges(oldCfg, newCfg)

	assert.NotContains(t, result.ChangedFields, "activity_max_response_size")
}

// The clause must not short-circuit the rest of detection the way the
// restart-required block at the top of the function does: a body that both
// deletes the cap and edits a hot-reloadable field has to report BOTH, or
// reporting the deletion would cost the operator the other change.
func TestCapChangeDoesNotSwallowOtherChangedFields(t *testing.T) {
	oldCfg := &config.Config{
		ActivityMaxResponseSize: capPtr(0),
		ReadOnlyMode:            false,
	}
	newCfg := &config.Config{
		ReadOnlyMode: true,
	}

	result := DetectConfigChanges(oldCfg, newCfg)

	assert.Contains(t, result.ChangedFields, "activity_max_response_size")
	assert.Contains(t, result.ChangedFields, "read_only_mode")
}
