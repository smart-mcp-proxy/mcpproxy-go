package runtime

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
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
// A server is gated only when ALL of these hold:
//
//   - config.db was readable (see "Storage failures" below), AND
//   - the parsed config document never stated a `quarantined` value
//     (ServerConfig.QuarantineExplicitlySet() — an operator who wrote the key,
//     either way, has spoken and is obeyed), AND
//   - the server is not yet known to config.db.
//
// # The upgrade boundary (deliberate, and the reason for the last condition)
//
// "Not yet known to config.db" is the proxy for "has never been through
// admission". Every server an existing user is running already has a config.db
// record, so an upgrade re-quarantines nothing — which is the whole point:
// a server the user has already vetted must not suddenly disappear behind a
// quarantine wall on a version bump.
//
// The cost of that choice is that an install ALREADY hit by #937 keeps its
// poisoned server live, because the buggy admission left exactly the config.db
// record this gate reads as "vetted". Nothing distinguishes the two at load
// time, so instead of guessing, reportPreFixAdmissions below names those
// servers in a WARN so an operator can review them. See the "Upgrading" section
// of docs/features/security-quarantine.md.
//
// The other boundary is precise and worth stating: a server that is in a
// hand-written config but NOT in config.db is treated as first-seen. That is
// true after `rm -rf ~/.mcpproxy/*.db`, or when a config file is moved to a
// machine whose data dir has never seen it — both of which are exactly the
// "config files get shared, templated, copied between machines" case the issue
// calls out, so gating them is the intended behaviour rather than a regression.
//
// # Storage failures
//
// A failed ListUpstreamServers() must NOT be flattened into "no servers are
// known" — that would make every configured server look first-seen and wall off
// the user's entire server list behind a quarantine, permanently, because the
// next boot would inherit the decision. A single bbolt lock contention at boot
// is not a security event. When storage is unreadable the gate abstains
// entirely: unknown is not the same as new.
//
// # Durability
//
// The gate decision is persisted by the caller's SaveUpstreamServer, and the
// second branch below re-reads it, so a config file that stays silent about
// `quarantined` inherits mcpproxy's own recorded state instead of resolving an
// absent key back to false.
//
// That durability depends on mcpproxy never FABRICATING an operator statement
// when it writes the config file. ServerConfig.MarshalJSON omits `quarantined`
// for a server that is not quarantined and never stated the key; without that,
// a single save (any `PUT /api/v1/config`) stamped `"quarantined": false` on
// every server, the presence check below skipped them forever, and the save
// loop in LoadConfiguredServers then wrote false over the config.db quarantine.
//
// # Immutability
//
// The gate NEVER writes through a *config.ServerConfig it was handed. Those
// pointers live in the snapshot published by configsvc, which the supervisor's
// reconcile loop and serverEligibleForIndexing read concurrently and which
// internal/server/server.go documents as immutable. A gated server is a COPY in
// a fresh Servers slice, and the caller republishes the whole config so
// subscribers observe the decision through an atomic snapshot swap.
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

// admissionDecision is one server the gate wants to change, plus the activity
// text to publish once the decision has actually been committed.
type admissionDecision struct {
	server string
	reason string
	newly  bool
}

// applyConfigLoadAdmissionGate resolves the admission decision for every server
// in cfg and returns the config to use.
//
// It never mutates cfg. When nothing changes it returns (cfg, false); otherwise
// it returns a copy whose gated entries are copies too, so an already-published
// snapshot is never written behind a subscriber's back.
//
// storageOK reports whether `stored` is a trustworthy view of config.db. When
// it is false the gate abstains: an unreadable database means "unknown", not
// "everything is new".
func (r *Runtime) applyConfigLoadAdmissionGate(cfg *config.Config, stored map[string]*config.ServerConfig, storageOK bool) (*config.Config, bool) {
	if cfg == nil {
		return cfg, false
	}

	if !storageOK {
		r.logger.Warn("Skipping config-load admission gate: server storage is unreadable",
			zap.String("reason", "an unreadable config.db cannot distinguish a first-seen server from a known one (issue #937)"))
		return cfg, false
	}

	decisions := make(map[int]admissionDecision)
	var preFix []string

	for i, sc := range cfg.Servers {
		if sc == nil || sc.Quarantined {
			continue
		}

		prev, known := stored[sc.Name]
		if known {
			// Already been through admission. Inherit whatever mcpproxy itself
			// recorded, so a gate decision from a previous run survives a config
			// file that never mentions the key.
			if prev != nil && prev.Quarantined && !sc.QuarantineExplicitlySet() {
				decisions[i] = admissionDecision{
					server: sc.Name,
					reason: "retaining the quarantine recorded in config.db for a server whose config states nothing",
				}
				continue
			}
			// Known, live, never explicitly reviewed, and its trust mode says it
			// should have been held: this is what an install hit by #937 before
			// the fix looks like. Upgrade safety says leave it alone; honesty
			// says say so.
			if !sc.QuarantineExplicitlySet() && cfg.QuarantineDefaultForServer(sc) {
				preFix = append(preFix, sc.Name)
			}
			continue
		}

		if sc.QuarantineExplicitlySet() || !cfg.QuarantineDefaultForServer(sc) {
			continue
		}

		decisions[i] = admissionDecision{
			server: sc.Name,
			reason: "first-seen server from configuration file held for review by the " +
				string(sc.EffectiveTrustMode()) + " trust mode",
			newly: true,
		}
	}

	r.reportPreFixAdmissions(preFix)

	if len(decisions) == 0 {
		return cfg, false
	}

	gated := *cfg
	gated.Servers = make([]*config.ServerConfig, len(cfg.Servers))
	copy(gated.Servers, cfg.Servers)

	for i, d := range decisions {
		sc := config.CopyServerConfig(cfg.Servers[i])
		sc.Quarantined = true
		gated.Servers[i] = sc

		if d.newly {
			r.logger.Warn("Quarantining first-seen server from configuration file",
				zap.String("server", d.server),
				zap.String("trust_mode", string(sc.EffectiveTrustMode())),
				zap.String("reason", "config-load admission gate: server has not been reviewed (issue #937)"))
		} else {
			r.logger.Info("Retaining recorded quarantine for server with no explicit config value",
				zap.String("server", d.server))
		}
	}

	// Activity is emitted off the caller's goroutine: this runs inside
	// configsvc's publish lock on the hot path, and an event subscriber must
	// never be able to stall a config update.
	pending := make([]admissionDecision, 0, len(decisions))
	for _, d := range decisions {
		if d.newly {
			pending = append(pending, d)
		}
	}
	if len(pending) > 0 {
		go func() {
			for _, d := range pending {
				r.EmitActivityQuarantineChange(d.server, true, d.reason)
			}
		}()
	}

	return &gated, true
}

// reportPreFixAdmissions warns about servers that are live, unreviewed, and
// would have been held by their trust mode — the signature of an install that
// was admitted by the #937 bug before it was fixed. The gate deliberately does
// not re-quarantine them (that would break upgrade safety for every legitimately
// vetted server), so this is the only signal an affected operator gets.
func (r *Runtime) reportPreFixAdmissions(names []string) {
	if len(names) == 0 {
		return
	}
	r.logger.Warn("Configured servers predate the config-load admission gate and have never been explicitly reviewed",
		zap.Strings("servers", names),
		zap.String("action", "review them in the quarantine UI, or record the decision with an explicit \"quarantined\" value in mcp_config.json (issue #937)"))
}

// storedServersForAdmission reads config.db's view of the configured servers.
// The second return value reports whether that view is trustworthy; callers
// must not treat a failed read as an empty database.
func (r *Runtime) storedServersForAdmission() (map[string]*config.ServerConfig, bool) {
	if r.storageManager == nil {
		return nil, false
	}
	stored, err := r.storageManager.ListUpstreamServers()
	if err != nil {
		r.logger.Error("Failed to read stored servers for the admission gate", zap.Error(err))
		return nil, false
	}
	byName := make(map[string]*config.ServerConfig, len(stored))
	for _, s := range stored {
		if s != nil {
			byName[s.Name] = s
		}
	}
	return byName, true
}

// gateConfigForAdmission is the one-call form used by paths that hold a config
// they have not published yet (ApplyConfig before its disk write, and the
// configsvc pre-publish hook). It reads storage itself.
func (r *Runtime) gateConfigForAdmission(cfg *config.Config) *config.Config {
	stored, ok := r.storedServersForAdmission()
	gated, _ := r.applyConfigLoadAdmissionGate(cfg, stored, ok)
	return gated
}

// installAdmissionGateHook wires the gate into configsvc so that EVERY
// configuration published to subscribers has already been through admission.
//
// Without it there is a real exposure window: ReloadConfiguration publishes the
// freshly-parsed file (waking the supervisor's reconcile loop) and only then
// calls LoadConfiguredServers, so a poisoned server hand-added to a running
// mcpproxy could be connected and its tools indexed before the gate flipped the
// bit.
func (r *Runtime) installAdmissionGateHook() {
	if r.configSvc == nil {
		return
	}
	r.configSvc.SetPrePublishHook(func(cfg *config.Config) *config.Config {
		return r.gateConfigForAdmission(cfg)
	})
}

// gateInitialConfig applies admission to the snapshot configsvc was constructed
// with. NewService stores that snapshot directly, so it never passes through the
// pre-publish hook; this must run before anything can observe it (i.e. before
// supervisor.Start()).
func (r *Runtime) gateInitialConfig() {
	if r.configSvc == nil {
		return
	}
	current := r.configSvc.Current()
	if current == nil || current.Config == nil {
		return
	}
	if gated := r.gateConfigForAdmission(current.Config); gated != current.Config {
		if err := r.configSvc.Update(gated, configsvc.UpdateTypeModify, "config_load_admission_gate"); err != nil {
			r.logger.Error("Failed to publish admission-gated startup configuration", zap.Error(err))
			return
		}
		r.mu.Lock()
		r.cfg = gated
		r.setDesiredLocked(gated)
		r.mu.Unlock()
	}
}

// markQuarantineDecisionExplicit records, in the published configuration, that
// a quarantine value has been stated for this server. Called when a human
// toggles quarantine: config.db cannot carry the presence bit, so without this
// the next SaveConfiguration would drop the key and the server would look
// never-reviewed again.
//
// Republished as a copy for the same reason the gate is: the snapshot's
// ServerConfig pointers are read concurrently by the supervisor.
func (r *Runtime) markQuarantineDecisionExplicit(serverName string) {
	if r.configSvc == nil {
		return
	}
	current := r.configSvc.Current()
	if current == nil || current.Config == nil {
		return
	}

	for i, sc := range current.Config.Servers {
		if sc == nil || sc.Name != serverName || sc.QuarantineExplicitlySet() {
			continue
		}
		updated := *current.Config
		updated.Servers = make([]*config.ServerConfig, len(current.Config.Servers))
		copy(updated.Servers, current.Config.Servers)
		stamped := config.CopyServerConfig(sc)
		stamped.MarkQuarantineExplicitlySet(true)
		updated.Servers[i] = stamped
		r.publishAdmissionGatedConfig(current.Config, &updated)
		return
	}
}

// publishAdmissionGatedConfig swaps a gated config in for the one it was
// derived from, atomically, so subscribers see the decision.
//
// It is a no-op when `previous` is no longer the published config: someone
// committed a newer one while we were gating, and that config went through the
// pre-publish hook on its own way in. Clobbering it here would revert a
// concurrent apply.
func (r *Runtime) publishAdmissionGatedConfig(previous, gated *config.Config) {
	if r.configSvc == nil || previous == gated {
		return
	}
	if current := r.configSvc.Current(); current == nil || current.Config != previous {
		return
	}
	if err := r.configSvc.Update(gated, configsvc.UpdateTypeModify, "config_load_admission_gate"); err != nil {
		r.logger.Error("Failed to publish admission-gated configuration", zap.Error(err))
		return
	}
	r.mu.Lock()
	if r.cfg == previous {
		r.cfg = gated
		r.setDesiredLocked(gated)
	}
	r.mu.Unlock()
}
