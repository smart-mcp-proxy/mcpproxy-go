package runtime

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// warnUpstreamFileRefsOutsideAllowList logs one startup WARN naming every
// upstream server whose ${file:} reference secret_file_dirs refuses by path
// (#1721 upgrade path: such references resolved before the allow-list
// existed). Paths are not logged; the server names are enough to find them in
// the config file.
func warnUpstreamFileRefsOutsideAllowList(logger *zap.Logger, cfg *config.Config) {
	if logger == nil {
		return
	}
	names := config.UpstreamFileRefsOutsideAllowList(cfg)
	if len(names) == 0 {
		return
	}
	logger.Warn(config.MsgUpstreamFileRefsOutsideAllowList,
		zap.Strings("servers", names),
		zap.String("key", "secret_file_dirs"))
}
