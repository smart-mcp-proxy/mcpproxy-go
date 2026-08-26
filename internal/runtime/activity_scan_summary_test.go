package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The settled-scan event carries its findings rollup as a map[string]int
// (scanCallbackAdapter.OnScanCompleted builds it that way, and
// Runtime.publishScanSettled passes it straight through). The activity service
// used to read it with a map[string]interface{} type assertion, which never
// matched — so findings_summary was dropped from every security_scan activity
// record ever written. Nothing noticed because nothing rendered it; the
// Activity Log's scan drawer now does (audit finding F25, #1046).
func TestGetSeverityCountsPayload(t *testing.T) {
	t.Run("reads the concrete map the producer actually sends", func(t *testing.T) {
		counts, ok := getSeverityCountsPayload(map[string]any{
			"findings_summary": map[string]int{"high": 2, "low": 1},
		}, "findings_summary")

		require.True(t, ok)
		assert.Equal(t, map[string]interface{}{"high": 2, "low": 1}, counts)
	})

	t.Run("keeps an EMPTY rollup, which is a clean scan", func(t *testing.T) {
		// This is the distinction the drawer depends on: a scan that found
		// nothing reports {}, and a record with no rollup at all reports
		// nothing. Collapsing the two would let the UI call a server clean on
		// the strength of a missing field.
		counts, ok := getSeverityCountsPayload(map[string]any{
			"findings_summary": map[string]int{},
		}, "findings_summary")

		require.True(t, ok)
		assert.Empty(t, counts)
	})

	t.Run("reads a JSON round-tripped rollup too", func(t *testing.T) {
		counts, ok := getSeverityCountsPayload(map[string]any{
			"findings_summary": map[string]interface{}{"critical": float64(1)},
		}, "findings_summary")

		require.True(t, ok)
		assert.Equal(t, map[string]interface{}{"critical": float64(1)}, counts)
	})

	t.Run("absent, nil and malformed are all 'no rollup'", func(t *testing.T) {
		for name, payload := range map[string]map[string]any{
			"absent":    {},
			"nil":       {"findings_summary": nil},
			"malformed": {"findings_summary": "none"},
		} {
			counts, ok := getSeverityCountsPayload(payload, "findings_summary")
			assert.False(t, ok, name)
			assert.Nil(t, counts, name)
		}
	})
}
