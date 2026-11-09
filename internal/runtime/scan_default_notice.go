package runtime

import (
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// warnScanDefaultOnce logs, once per installation, which enabled servers run
// under the scan default because their trust_mode is empty (spec 120 FR-011).
// The marker is written on the first start that sees at least one configured
// server, whether or not any is defaulted, so a fresh install with no servers
// logs nothing and keeps the notice for the start that has servers. A marker
// read error logs nothing and writes nothing (the next start retries).
func (r *Runtime) warnScanDefaultOnce() {
	if r.storageManager == nil {
		return
	}
	cfg := r.Config()
	if cfg == nil || len(cfg.Servers) == 0 {
		return
	}
	state, err := r.storageManager.LoadScanDefaultNoticeState()
	if err != nil {
		r.logger.Debug("scan default notice: could not read marker, skipping", zap.Error(err))
		return
	}
	if state != nil {
		return
	}
	names := config.ScanDefaultTrustServers(cfg)
	if len(names) > 0 {
		r.logger.Warn(config.ScanDefaultTrustMessage(names),
			zap.Strings("servers", names),
			zap.String("spec", "120"))
	}
	if err := r.storageManager.SaveScanDefaultNoticeState(storage.ScanDefaultNoticeState{NoticedAt: time.Now().UTC(), Servers: names}); err != nil {
		r.logger.Debug("scan default notice: could not write marker", zap.Error(err))
	}
}
