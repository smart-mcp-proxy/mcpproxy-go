package config

import (
	"net/url"
	"strings"
)

// RegistryProtocolGenericJSON marks a registry that is a plain JSON document
// rather than an implementation of the official registry API: it is fetched
// exactly as configured, with no routes, pagination, or query parameters added.
// (The registries package aliases this; it lives here so the load-time repair
// below can name it without importing registries — which would be an import
// cycle.)
const RegistryProtocolGenericJSON = "custom/json"

// RepairMangledRegistryURLs undoes the URL mangling described in GH #783 for
// registries a user added BEFORE the fix, and reports how many it repaired.
//
// The old add path appended "/v0.1/servers" to any source URL that did not
// already contain "/servers", so a pasted static document was persisted as
//
//	url:         https://.../apps.json
//	servers_url: https://.../apps.json/v0.1/servers   <- 404 forever
//	protocol:    modelcontextprotocol/registry        <- wrong parser
//
// Fixing the add path alone leaves those users broken after upgrading, since the
// merge copies servers_url/protocol straight through.
//
// The repair is deliberately surgical. It fires ONLY when the persisted
// servers_url is exactly what the old derivation would have produced AND the
// source URL's final path segment names a document (apps.json, registry.yaml).
// Such a path cannot host a /v0.1/servers route, so rewriting it cannot break a
// registry that works. A path-based registry (https://acme.example/api) may
// genuinely serve its collection at /api/v0.1/servers, so it is left untouched.
//
// Idempotent: a repaired entry no longer matches the old-derivation fingerprint.
func RepairMangledRegistryURLs(cfg *Config) int {
	if cfg == nil || len(cfg.Registries) == 0 {
		return 0
	}

	defaults := DefaultRegistries()
	isDefault := make(map[string]bool, len(defaults))
	for i := range defaults {
		isDefault[strings.ToLower(defaults[i].ID)] = true
	}

	repaired := 0
	for i := range cfg.Registries {
		entry := &cfg.Registries[i]
		if isDefault[strings.ToLower(entry.ID)] {
			continue // a shipped default is not ours to rewrite
		}
		if !registryURLWasMangled(entry.URL, entry.ServersURL) {
			continue
		}
		entry.ServersURL = entry.URL
		entry.Protocol = RegistryProtocolGenericJSON
		repaired++
	}
	return repaired
}

// registryURLWasMangled reports whether serversURL is the old derivation applied
// to a document URL — the exact fingerprint of the #783 bug.
func registryURLWasMangled(rawURL, serversURL string) bool {
	if rawURL == "" || serversURL == "" {
		return false
	}
	// The old rule: strings.TrimRight(url, "/") + "/v0.1/servers", applied to any
	// URL without "/servers" in it.
	if serversURL != strings.TrimRight(rawURL, "/")+"/v0.1/servers" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return pathNamesADocument(u.Path)
}

// pathNamesADocument reports whether a URL path ends in a concrete file name
// (a final segment containing a dot), e.g. /refs/heads/main/apps.json. Appending
// a route to such a path can only 404.
func pathNamesADocument(path string) bool {
	last := strings.TrimSuffix(path, "/")
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	return strings.Contains(last, ".")
}
