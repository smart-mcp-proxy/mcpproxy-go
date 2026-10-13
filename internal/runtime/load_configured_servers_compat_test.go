package runtime

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// LoadConfiguredServers is the pre-Spec-116 reconcile entry point, kept for
// this package's tests only (it does not exist in the production build). A nil
// cfg reconciles storage and the manager with the live configuration
// (ReconcileLive); a non-nil cfg is the configuration the test submits, as a
// file reload would (ReplaceConfig). Neither writes the config file, as the
// old reconcile never did.
func (r *Runtime) LoadConfiguredServers(cfg *config.Config) error {
	_, err := r.Commit("reconcile", func(tx *CommitTx) error {
		if cfg != nil {
			tx.ReplaceConfig(cfg)
		} else {
			tx.ReconcileLive()
		}
		tx.noDisk()
		return nil
	})
	return err
}
