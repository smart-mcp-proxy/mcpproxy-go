package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/experiments"
)

// protocolOfficial is the official Model Context Protocol registry protocol
// (https://registry.modelcontextprotocol.io/v0.1/servers). Responses wrap each
// entry as { "server": <server.json>, "_meta": {...} } and paginate via an
// opaque metadata.nextCursor. parseOfficialPage understands this envelope and
// classifies each entry per transport (packages => local/stdio, remotes =>
// remote) — never "remotes present ⇒ remote" (GH #567 root fix).
const protocolOfficial = "modelcontextprotocol/registry"

// officialMetaKey is the reserved _meta namespace the official registry uses for
// publication status (status / isLatest / timestamps).
const officialMetaKey = "io.modelcontextprotocol.registry/official"

const (
	// officialPageLimit is the per-request page size requested from the registry.
	officialPageLimit = 100
	// officialMaxPages bounds the cursor follow-loop so a misbehaving or huge
	// registry can never make search/list unbounded.
	officialMaxPages = 20
)

// fetchOfficialServers fetches every (active, latest) server from an official
// v0.1 registry, following metadata.nextCursor up to officialMaxPages. The
// optional query is passed through as the registry-side `search` parameter to
// reduce pagination; surfaces still filter client-side for exactness.
func fetchOfficialServers(ctx context.Context, reg *RegistryEntry, guesser *experiments.Guesser, query string) ([]ServerEntry, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	var all []ServerEntry
	cursor := ""
	for page := 0; page < officialMaxPages; page++ {
		reqURL, err := buildOfficialURL(reg.ServersURL, query, cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid registry URL %q: %w", reg.ServersURL, err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		// Some registries reject empty/bare User-Agents (issue #566).
		req.Header.Set("User-Agent", registryUserAgent())
		// Opt-in official-protocol registries (e.g. Smithery) authenticate via
		// their configured key.
		applyRegistryAuth(req, reg)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch servers: %w", err)
		}

		var rawData interface{}
		decodeErr := json.NewDecoder(resp.Body).Decode(&rawData)
		status := resp.StatusCode
		resp.Body.Close()

		if status != http.StatusOK {
			return nil, fmt.Errorf("registry query returned %d", status)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("invalid JSON from registry: %w", decodeErr)
		}

		servers, next := parseOfficialPage(rawData)
		all = append(all, servers...)

		if next == "" || next == cursor {
			break
		}
		cursor = next
	}

	if guesser != nil {
		all = applyBatchRepositoryGuessing(ctx, all, guesser)
	}
	return all, nil
}

// buildOfficialURL appends version=latest, the page limit, an optional search
// query, and the page cursor to the registry's servers endpoint.
func buildOfficialURL(base, query, cursor string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("version", "latest")
	q.Set("limit", fmt.Sprintf("%d", officialPageLimit))
	if query != "" {
		q.Set("search", query)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// parseOfficialPage parses one page of an official v0.1 registry response and
// returns the classified server entries plus the opaque nextCursor (empty when
// the listing is exhausted). Entries whose publication status is
// deleted/deprecated, or that are not the latest version, are skipped.
func parseOfficialPage(rawData interface{}) (servers []ServerEntry, nextCursor string) {
	root, ok := rawData.(map[string]interface{})
	if !ok {
		// Some generic v0.1 endpoints return a bare array of wrapped items.
		if arr, isArr := rawData.([]interface{}); isArr {
			return parseOfficialItems(arr), ""
		}
		return nil, ""
	}

	if meta, ok := root["metadata"].(map[string]interface{}); ok {
		nextCursor = firstString(meta, "nextCursor", "next_cursor")
	}

	items, _ := root["servers"].([]interface{})
	if items == nil {
		// Tolerate the alternative "data" envelope.
		items, _ = root["data"].([]interface{})
	}

	// Back-compat: a legacy "modelcontextprotocol/registry" response is a flat
	// array of ServerEntry-shaped objects (no per-item {server,_meta} wrapper).
	// Detect that and delegate to the legacy direct-unmarshal parser.
	if len(items) > 0 && !itemsAreWrapped(items) {
		return parseOpenAPIRegistry(rawData), nextCursor
	}
	return parseOfficialItems(items), nextCursor
}

// itemsAreWrapped reports whether any list item uses the official
// { "server": {...}, "_meta": {...} } envelope.
func itemsAreWrapped(items []interface{}) bool {
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, has := m["server"]; has {
			return true
		}
		if _, has := m["_meta"]; has {
			return true
		}
	}
	return false
}

// parseOfficialItems converts wrapped { server, _meta } items into classified
// ServerEntry values, applying the status/isLatest filter.
func parseOfficialItems(items []interface{}) []ServerEntry {
	servers := []ServerEntry{}
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Descend into the wrapped .server object; tolerate a flat item where the
		// server fields live at the top level (back-compat with older flat lists).
		serverMap, ok := itemMap["server"].(map[string]interface{})
		if !ok {
			serverMap = itemMap
		}

		// Publication metadata lives under _meta[officialMetaKey]; it may sit on
		// the wrapper or, in some payloads, inside the server object.
		if !officialEntryIncluded(itemMap, serverMap) {
			continue
		}

		entry := officialServerToEntry(serverMap)
		if entry.Name == "" && entry.ID == "" {
			continue
		}
		servers = append(servers, entry)
	}
	return servers
}

// officialEntryIncluded reports whether an entry should be surfaced: by default
// only active (non-deleted, non-deprecated) latest versions are included. When
// no official metadata is present the entry is included (generic endpoints).
func officialEntryIncluded(wrapper, server map[string]interface{}) bool {
	meta := officialMetaBlock(wrapper)
	if meta == nil {
		meta = officialMetaBlock(server)
	}
	if meta == nil {
		return true // generic v0.1 endpoint without official publication metadata
	}
	status := strings.ToLower(firstString(meta, "status"))
	switch status {
	case "deleted", "deprecated":
		return false
	}
	// isLatest defaults to true when absent so non-official metadata blocks that
	// omit the flag are not silently dropped.
	if v, ok := meta["isLatest"]; ok {
		if b, isBool := v.(bool); isBool && !b {
			return false
		}
	}
	if v, ok := meta["is_latest"]; ok {
		if b, isBool := v.(bool); isBool && !b {
			return false
		}
	}
	return true
}

// officialMetaBlock returns the io.modelcontextprotocol.registry/official block
// from a _meta map, or nil.
func officialMetaBlock(m map[string]interface{}) map[string]interface{} {
	metaRaw, ok := m["_meta"].(map[string]interface{})
	if !ok {
		return nil
	}
	block, _ := metaRaw[officialMetaKey].(map[string]interface{})
	return block
}

// officialServerToEntry classifies a single server.json object. THE #567 rule:
// packages[] => local/stdio (InstallCmd set, URL left empty); remotes[] =>
// remote (URL set). A hybrid server prefers its package for stdio while keeping
// the remote endpoint as ConnectURL.
func officialServerToEntry(server map[string]interface{}) ServerEntry {
	entry := ServerEntry{
		Name:        firstString(server, "name"),
		Description: firstString(server, "description"),
	}
	entry.ID = entry.Name

	if repo, ok := server["repository"].(map[string]interface{}); ok {
		entry.SourceCodeURL = firstString(repo, "url")
	}

	packages, _ := server["packages"].([]interface{})
	remotes, _ := server["remotes"].([]interface{})

	// LOCAL/stdio classification from the first usable package.
	for _, p := range packages {
		pkg, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if cmd := buildPackageCommand(pkg); cmd != "" {
			entry.InstallCmd = cmd
			entry.RequiredInputs = append(entry.RequiredInputs, inputsFromList(pkg, "environmentVariables", "environment_variables")...)
			break
		}
	}

	// REMOTE classification.
	for _, r := range remotes {
		rem, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		remoteURL := firstString(rem, "url")
		if remoteURL == "" {
			continue
		}
		if entry.InstallCmd == "" {
			// Pure remote: this IS the connection endpoint.
			entry.URL = remoteURL
		} else {
			// Hybrid: package wins for stdio; keep the remote as a fallback.
			entry.ConnectURL = remoteURL
		}
		entry.RequiredInputs = append(entry.RequiredInputs, inputsFromList(rem, "headers")...)
		break
	}

	if entry.Description == "" {
		entry.Description = noDescAvailable
	}
	return entry
}

// buildPackageCommand derives the local launch command for a package entry from
// runtimeHint + runtimeArguments + identifier(@version) + packageArguments.
func buildPackageCommand(pkg map[string]interface{}) string {
	identifier := firstString(pkg, "identifier", "name")
	if identifier == "" {
		return ""
	}
	registryType := strings.ToLower(firstString(pkg, "registryType", "registry_type", "registry_name"))
	hint := strings.ToLower(firstString(pkg, "runtimeHint", "runtime_hint"))
	if hint == "" {
		hint = defaultRuntimeForRegistry(registryType)
	}
	version := firstString(pkg, "version")

	runtimeArgs := argTokens(pkg, "runtimeArguments", "runtime_arguments")
	packageArgs := argTokens(pkg, "packageArguments", "package_arguments")

	var parts []string
	switch hint {
	case dockerProtocol, "oci":
		ref := identifier
		if version != "" {
			ref = identifier + ":" + version
		}
		parts = append(parts, "docker", "run", "-i", "--rm")
		parts = append(parts, runtimeArgs...)
		parts = append(parts, ref)
		parts = append(parts, packageArgs...)
	default:
		ref := identifier
		// Only npm-style runtimes use the pkg@version reference form; pypi/uvx and
		// others take the bare identifier to avoid breaking install resolution.
		if version != "" && (hint == "npx" || registryType == "npm") {
			ref = identifier + "@" + version
		}
		if hint == "" {
			hint = "npx"
		}
		parts = append(parts, hint)
		parts = append(parts, runtimeArgs...)
		parts = append(parts, ref)
		parts = append(parts, packageArgs...)
	}
	return strings.Join(parts, " ")
}

// defaultRuntimeForRegistry maps a package registry type to its conventional
// local runtime when runtimeHint is absent.
func defaultRuntimeForRegistry(registryType string) string {
	switch registryType {
	case "npm":
		return "npx"
	case "pypi", "pip":
		return "uvx"
	case "oci", dockerProtocol:
		return dockerProtocol
	case "nuget":
		return "dnx"
	default:
		return ""
	}
}

// argTokens renders a server.json argument list (runtimeArguments /
// packageArguments) into command tokens. Named args contribute "name [value]";
// positional args contribute their value (falling back to default).
func argTokens(pkg map[string]interface{}, keys ...string) []string {
	var out []string
	for _, raw := range sliceField(pkg, keys...) {
		arg, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name := firstString(arg, "name")
		value := firstString(arg, "value", "default")
		switch {
		case name != "":
			out = append(out, name)
			if value != "" {
				out = append(out, value)
			}
		case value != "":
			out = append(out, value)
		}
	}
	return out
}

// inputsFromList maps an environmentVariables[]/headers[] list to RequiredInputs.
func inputsFromList(m map[string]interface{}, keys ...string) []RequiredInput {
	var inputs []RequiredInput
	for _, raw := range sliceField(m, keys...) {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name := firstString(item, "name")
		if name == "" {
			continue
		}
		secret := boolField(item, "isSecret", "is_secret")
		inputs = append(inputs, RequiredInput{
			Name:        name,
			Description: firstString(item, "description"),
			Secret:      secret,
		})
	}
	return inputs
}

// firstString returns the first non-empty string value among the given keys.
func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// boolField returns the first present boolean value among the given keys.
func boolField(m map[string]interface{}, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k].(bool); ok {
			return v
		}
	}
	return false
}

// sliceField returns the first present array value among the given keys.
func sliceField(m map[string]interface{}, keys ...string) []interface{} {
	for _, k := range keys {
		if v, ok := m[k].([]interface{}); ok {
			return v
		}
	}
	return nil
}
