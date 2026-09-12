package runtime

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// SetServerShared commits sharing against the current configuration and
// publishes it before returning. A successful unshare must not leave a file
// watcher delay in which existing credentials can still reach the server.
func (r *Runtime) SetServerShared(name string, shared bool) (*config.ServerConfig, error) {
	r.configCommitMu.Lock()
	defer r.configCommitMu.Unlock()
	snapshot := r.ConfigSnapshot()
	next := snapshot.Clone()
	if next == nil {
		return nil, fmt.Errorf("configuration unavailable")
	}
	// Preserve unrelated changes already saved pending a restart.
	if desired := r.pendingAwareDiskConfig(next); desired != nil {
		next = desired
	}
	for _, server := range next.Servers {
		if server == nil || !strings.EqualFold(server.Name, name) {
			continue
		}
		server.Shared = shared
		server.Updated = time.Now().UTC()
		if _, err := r.applyConfigLocked(next, snapshot.Path); err != nil {
			return nil, err
		}
		return server, nil
	}
	return nil, fmt.Errorf("server not found: %w", os.ErrNotExist)
}
