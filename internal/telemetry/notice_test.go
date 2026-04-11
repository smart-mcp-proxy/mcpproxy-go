package telemetry

import (
	"bytes"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestMaybePrintFirstRunNoticePrintsOnce(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	cfg := &config.Config{}
	var buf bytes.Buffer

	if !MaybePrintFirstRunNotice(cfg, &buf) {
		t.Error("expected notice to print on first call")
	}
	if !strings.Contains(buf.String(), "mcpproxy collects anonymous usage telemetry") {
		t.Errorf("notice text wrong: %q", buf.String())
	}
	if !cfg.Telemetry.NoticeShown {
		t.Error("NoticeShown flag should be true after print")
	}

	buf.Reset()
	if MaybePrintFirstRunNotice(cfg, &buf) {
		t.Error("expected no print on second call")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output on second call, got %q", buf.String())
	}
}

func TestMaybePrintFirstRunNoticeSkipWhenDisabledByConfig(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	disabled := false
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{Enabled: &disabled},
	}
	var buf bytes.Buffer

	if MaybePrintFirstRunNotice(cfg, &buf) {
		t.Error("expected no print when telemetry is disabled in config")
	}
	if !cfg.Telemetry.NoticeShown {
		t.Error("NoticeShown should still be set so we don't re-check next run")
	}
}

func TestMaybePrintFirstRunNoticeSkipWhenDisabledByEnv(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")

	cfg := &config.Config{}
	var buf bytes.Buffer

	if MaybePrintFirstRunNotice(cfg, &buf) {
		t.Error("expected no print when DO_NOT_TRACK is set")
	}
	if !cfg.Telemetry.NoticeShown {
		t.Error("NoticeShown should be set even with env disable")
	}
}
