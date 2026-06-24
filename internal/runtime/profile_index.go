package runtime

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"go.uber.org/zap"
)

// profileEffectiveServers builds the desired profile -> effective-server-set map
// from the current config. Each profile's server list is filtered to servers that
// actually exist (warn-skip semantics, see config.ProfileConfig.EffectiveServers).
// Returns nil when there are no profiles.
func profileEffectiveServers(cfg *config.Config) map[string][]string {
	if cfg == nil || len(cfg.Profiles) == 0 {
		return nil
	}
	out := make(map[string][]string, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		out[p.Name] = p.EffectiveServers(cfg)
	}
	return out
}

// reindexAffectedProfiles rebuilds every per-profile index that references
// serverName, after that server's tools changed in the shared index. Profiles
// that do not reference the server are left untouched (reload isolation). Called
// from applyDifferentialToolUpdate only when the shared index actually changed.
func (r *Runtime) reindexAffectedProfiles(serverName string) {
	if r.indexManager == nil {
		return
	}
	desired := profileEffectiveServers(r.Config())
	if len(desired) == 0 {
		return
	}

	r.profileIndexMu.Lock()
	defer r.profileIndexMu.Unlock()

	for name, servers := range desired {
		if !containsString(servers, serverName) {
			continue
		}
		if err := r.indexManager.RebuildProfileFromShared(name, servers); err != nil {
			r.logger.Error("Failed to rebuild per-profile index",
				zap.String("profile", name),
				zap.String("trigger_server", serverName),
				zap.Error(err))
			continue
		}
		r.profileMembership[name] = servers
	}
}

// reconcileProfileIndexes reconciles all per-profile indexes against the current
// config and shared index. It builds new profiles, rebuilds profiles whose
// effective server set changed since the last sync, and drops profiles that were
// removed from config (including orphaned index dirs left by a previous run).
// Profiles whose membership is unchanged are left untouched. Called after each
// discovery pass and after config reloads.
func (r *Runtime) reconcileProfileIndexes() {
	if r.indexManager == nil {
		return
	}
	desired := profileEffectiveServers(r.Config())

	r.profileIndexMu.Lock()
	defer r.profileIndexMu.Unlock()

	// Build or rebuild new / changed profiles; leave unchanged ones untouched.
	for name, servers := range desired {
		if prev, existed := r.profileMembership[name]; existed && stringSlicesEqual(prev, servers) {
			continue
		}
		if err := r.indexManager.RebuildProfileFromShared(name, servers); err != nil {
			r.logger.Error("Failed to (re)build per-profile index",
				zap.String("profile", name),
				zap.Error(err))
			continue
		}
		r.profileMembership[name] = servers
	}

	// Drop profiles no longer present in config — both those we tracked this run
	// and orphaned directories persisted from a prior run.
	stale := make(map[string]struct{})
	for name := range r.profileMembership {
		if _, ok := desired[name]; !ok {
			stale[name] = struct{}{}
		}
	}
	if onDisk, err := r.indexManager.ExistingProfileDirs(); err != nil {
		r.logger.Warn("Failed to list per-profile index dirs during reconcile", zap.Error(err))
	} else {
		for _, name := range onDisk {
			if _, ok := desired[name]; !ok {
				stale[name] = struct{}{}
			}
		}
	}
	for name := range stale {
		if err := r.indexManager.DropProfile(name); err != nil {
			r.logger.Error("Failed to drop removed per-profile index",
				zap.String("profile", name),
				zap.Error(err))
		}
		delete(r.profileMembership, name)
	}
}

func containsString(s []string, target string) bool {
	for _, v := range s {
		if v == target {
			return true
		}
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
