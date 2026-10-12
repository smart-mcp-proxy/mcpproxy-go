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
// It is one config commit (Spec 116); the default disk write keeps any change
// still pending a restart.
func (r *Runtime) SetServerShared(name string, shared bool) (*config.ServerConfig, error) {
	var updated *config.ServerConfig
	_, err := r.Commit("set_server_shared", func(tx *CommitTx) error {
		target := ""
		for _, sc := range tx.Config().Servers {
			if sc != nil && strings.EqualFold(sc.Name, name) {
				target = sc.Name
				break
			}
		}
		if target == "" {
			return fmt.Errorf("server not found: %w", os.ErrNotExist)
		}
		if err := tx.UpdateServer(target, func(cur *config.ServerConfig) error {
			cur.Shared = shared
			cur.Updated = time.Now().UTC()
			return nil
		}); err != nil {
			return err
		}
		updated, _ = tx.Server(target)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}
