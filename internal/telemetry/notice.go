package telemetry

import (
	"fmt"
	"io"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// FirstRunNoticeText is the one-time banner printed to stderr the first time
// `mcpproxy serve` runs against a config that has no telemetry_notice_shown
// flag. Spec 042 User Story 10.
const FirstRunNoticeText = `
mcpproxy collects anonymous usage telemetry to help shape the roadmap.
Learn what's collected: https://mcpproxy.app/telemetry
Disable with: mcpproxy telemetry disable    OR    DO_NOT_TRACK=1
`

// MaybePrintFirstRunNotice prints the first-run telemetry notice to w if it
// has not been shown before. It mutates cfg.Telemetry.NoticeShown so the
// caller can persist the change. Returns true if the notice was printed.
//
// Spec 042 User Story 10. Idempotent: subsequent calls with the same config
// are no-ops.
func MaybePrintFirstRunNotice(cfg *config.Config, w io.Writer) bool {
	if cfg == nil || w == nil {
		return false
	}
	if cfg.Telemetry == nil {
		cfg.Telemetry = &config.TelemetryConfig{}
	}
	if cfg.Telemetry.NoticeShown {
		return false
	}
	// Skip the notice if telemetry is already disabled — no point nagging
	// users who already opted out (e.g. via env var or prior `disable`).
	if !cfg.IsTelemetryEnabled() {
		cfg.Telemetry.NoticeShown = true
		return false
	}
	if disabled, _ := IsDisabledByEnv(); disabled {
		cfg.Telemetry.NoticeShown = true
		return false
	}
	fmt.Fprint(w, FirstRunNoticeText)
	cfg.Telemetry.NoticeShown = true
	return true
}
