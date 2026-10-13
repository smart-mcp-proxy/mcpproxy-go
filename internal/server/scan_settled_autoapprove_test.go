package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestShouldAutoApproveScanSettled exercises the spec 086 stage-3 async
// admission predicate (FR-011): a scan-mode, still-quarantined server whose
// baseline verdict settled GREEN ("clean") is auto-approved; every other
// combination fails closed.
func TestShouldAutoApproveScanSettled(t *testing.T) {
	tests := []struct {
		name        string
		mode        config.TrustMode
		quarantined bool
		verdict     string
		expected    bool
	}{
		{
			name:        "scan + quarantined + clean: auto-approve",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "clean",
			expected:    true,
		},
		{
			name:        "scan + quarantined + dangerous: stay quarantined",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "dangerous",
			expected:    false,
		},
		{
			name:        "scan + quarantined + warnings: stay quarantined",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "warnings",
			expected:    false,
		},
		{
			name:        "scan + quarantined + failed: stay quarantined (fail closed)",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "failed",
			expected:    false,
		},
		{
			name:        "scan + quarantined + not_scanned: stay quarantined (fail closed)",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "not_scanned",
			expected:    false,
		},
		{
			name:        "scan + quarantined + scanning (transient): stay quarantined",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "scanning",
			expected:    false,
		},
		{
			name:        "scan + quarantined + empty verdict: stay quarantined (fail closed)",
			mode:        config.TrustModeScan,
			quarantined: true,
			verdict:     "",
			expected:    false,
		},
		{
			name:        "manual + quarantined + clean: NEVER auto-approve",
			mode:        config.TrustModeManual,
			quarantined: true,
			verdict:     "clean",
			expected:    false,
		},
		{
			name:        "auto + quarantined + clean: not a scan-mode server, nothing to do",
			mode:        config.TrustModeAuto,
			quarantined: true,
			verdict:     "clean",
			expected:    false,
		},
		{
			name:        "scan + already unquarantined + clean: skip (re-entrancy / stale replay)",
			mode:        config.TrustModeScan,
			quarantined: false,
			verdict:     "clean",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, shouldAutoApproveScanSettled(tt.mode, tt.quarantined, tt.verdict))
		})
	}
}
