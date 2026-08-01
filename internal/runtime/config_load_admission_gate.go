package runtime

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #937 — the trust-mode admission gate on the CONFIG-LOAD path.
//
// Before this, Config.QuarantineDefaultForServer() was invoked from exactly
// three ADD-time sites (registry add, the upstream_servers MCP tool, the REST
// handler). A server written straight into mcp_config.json reached the upstream
// manager without passing any of them: it was admitted unquarantined, its tools
// auto-approved, and a poisoned tool description served verbatim to agents —
// under the DEFAULT `manual` trust mode with quarantine_enabled:true. That
// contradicts the trust-mode table published in
// docs/features/security-quarantine.md, which states that `manual` means
// "Quarantined for human review".
//
// # What is gated
//
// A server is gated only when BOTH hold:
//
//   - the parsed config document never stated a `quarantined` value
//     (ServerConfig.QuarantineExplicitlySet() — an operator who wrote the key,
//     either way, has spoken and is obeyed), AND
//   - the server is not yet known to config.db.
//
// # The upgrade boundary (deliberate, and the reason for the second condition)
//
// "Not yet known to config.db" is the proxy for "has never been through
// admission". Every server an existing user is running already has a config.db
// record, so an upgrade re-quarantines nothing — which is the whole point:
// a server the user has already vetted must not suddenly disappear behind a
// quarantine wall on a version bump.
//
// The boundary this leaves is precise and worth stating: a server that is in a
// hand-written config but NOT in config.db is treated as first-seen. That is
// true after `rm -rf ~/.mcpproxy/*.db`, or when a config file is moved to a
// machine whose data dir has never seen it — both of which are exactly the
// "config files get shared, templated, copied between machines" case the issue
// calls out, so gating them is the intended behaviour rather than a regression.
//
// # Durability
//
// The gate decision is persisted by the caller's SaveUpstreamServer. On the
// next start the server IS in config.db while the config file still says
// nothing, so the second branch below re-reads the stored value instead of
// letting an absent key silently resolve back to false. Without that, the hole
// would reopen one restart later.
//
// # Direction
//
// The gate can only ever ADD quarantine. Both branches are guarded on
// !sc.Quarantined, so a config (or an in-flight struct from an add path) that
// already asks for quarantine is never relaxed by a stale storage record.
// Un-quarantining stays exclusively a user action, which goes through
// Runtime.QuarantineServer and writes both stores.
//
// # Not done here
//
// No TPA scan runs at config-load admission. Quarantine-by-default already
// prevents the tools from reaching an agent, and the scan is what the human
// review step in the quarantine UI performs. Running a synchronous scan on the
// startup path would block boot on every first-seen server.
func (r *Runtime) applyConfigLoadAdmissionGate(cfg *config.Config, stored map[string]*config.ServerConfig) {
	if cfg == nil {
		return
	}

	for _, sc := range cfg.Servers {
		if sc == nil || sc.Quarantined || sc.QuarantineExplicitlySet() {
			continue
		}

		if prev, known := stored[sc.Name]; known {
			// Already been through admission. Inherit whatever mcpproxy itself
			// recorded, so a gate decision from a previous run survives a config
			// file that never mentions the key.
			if prev != nil && prev.Quarantined {
				sc.Quarantined = true
				r.logger.Info("Retaining recorded quarantine for server with no explicit config value",
					zap.String("server", sc.Name))
			}
			continue
		}

		if !cfg.QuarantineDefaultForServer(sc) {
			continue
		}

		sc.Quarantined = true
		r.logger.Warn("Quarantining first-seen server from configuration file",
			zap.String("server", sc.Name),
			zap.String("trust_mode", string(sc.EffectiveTrustMode())),
			zap.String("reason", "config-load admission gate: server has not been reviewed (issue #937)"))
		r.EmitActivityQuarantineChange(sc.Name, true,
			"first-seen server from configuration file held for review by the "+
				string(sc.EffectiveTrustMode())+" trust mode")
	}
}
