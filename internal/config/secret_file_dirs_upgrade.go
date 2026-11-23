package config

import (
	"encoding/json"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// MsgUpstreamFileRefsOutsideAllowList is the startup warning for upstream
// ${file:} references that secret_file_dirs no longer lets through. Before
// #1721 any absolute path resolved, so these servers stop resolving on
// upgrade; the warning names them so the fix is one step.
const MsgUpstreamFileRefsOutsideAllowList = "upstream ${file:} references are outside secret_file_dirs and will not resolve; " +
	"add their directories to secret_file_dirs in the config file, or move the files under <data-dir>/secrets"

// UpstreamFileRefsOutsideAllowList returns the names of the upstream servers
// with at least one ${file:} reference that the effective secret_file_dirs
// refuses by path. Only paths are checked: no secret file is opened and no
// other reference (env, keyring) is resolved. Wildcard mode reports nothing.
func UpstreamFileRefsOutsideAllowList(cfg *Config) []string {
	if cfg == nil || len(cfg.Servers) == 0 {
		return nil
	}
	wildcard, roots, disabled := EffectiveSecretFileDirRoots(cfg, true)
	if wildcard {
		return nil
	}
	policy := &secret.FilePolicy{Scope: secret.ScopeUpstream, Disabled: disabled}
	if !disabled {
		policy.AllowedRoots = append([]string(nil), roots...)
	}

	var names []string
	for _, sc := range cfg.Servers {
		if sc == nil {
			continue
		}
		if serverHasFileRefOutside(sc, policy) {
			names = append(names, sc.Name)
		}
	}
	sort.Strings(names)
	return names
}

func serverHasFileRefOutside(sc *ServerConfig, policy *secret.FilePolicy) bool {
	raw, err := json.Marshal(sc)
	if err != nil {
		return false
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return false
	}
	found := false
	walkJSONStrings(tree, func(s string) {
		if found {
			return
		}
		for _, ref := range secret.FindSecretRefs(s) {
			if ref.Type == secret.SecretTypeFile && !policy.PathWithinAllowList(ref.Name) {
				found = true
				return
			}
		}
	})
	return found
}

func walkJSONStrings(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		fn(t)
	case []any:
		for _, e := range t {
			walkJSONStrings(e, fn)
		}
	case map[string]any:
		for _, e := range t {
			walkJSONStrings(e, fn)
		}
	}
}
