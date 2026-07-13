package registries

import (
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// protocolGenericJSON is the protocol for a plain-JSON registry source: any
// https URL that serves a list of MCP servers as a static document rather than
// implementing the official v0.1 registry API (GH discussion #783).
//
// The distinction that matters at fetch time: a generic/json source is fetched
// EXACTLY as configured. Nothing is appended to its path (buildOfficialURL's
// version/limit/search/cursor query and the /v0.1/servers route are official-
// protocol details), because a static document has no routes and no pagination.
//
// Defined in config so the load-time repair (config.RepairMangledRegistryURLs)
// can name it without importing this package — that would be an import cycle.
const protocolGenericJSON = config.RegistryProtocolGenericJSON

// genericListKeys are the envelope keys a static registry may wrap its list in.
// A bare top-level array is also supported.
var genericListKeys = []string{"servers", "apps", "items", "data", "results"}

// parseGenericJSON parses a plain-JSON registry document into ServerEntry values.
// It is deliberately permissive because these documents are hand-written and
// follow no single schema; the shapes it understands are:
//
//	[ {...}, {...} ]                        — bare array (Fleur apps.json)
//	{ "servers"|"apps"|"items"|…: [ … ] }   — wrapped list
//	{ "mcpServers": { "name": {…} } }       — Claude-desktop style map
//
// Individual items may be official server.json objects (packages/remotes), a
// {command,args} pair, or a Fleur-style {config:{runtime,args}}. An item whose
// launch info is a local command sets InstallCmd and leaves URL EMPTY, so the
// add path builds a stdio transport (issues #483/#567).
func parseGenericJSON(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	for _, item := range genericItems(rawData) {
		if entry, ok := genericItemToEntry(item.name, item.data); ok {
			servers = append(servers, entry)
		}
	}
	return servers
}

// genericItem is one candidate server plus the key it was filed under (for the
// map form, where the key IS the server name).
type genericItem struct {
	name string
	data map[string]interface{}
}

// genericItems normalizes any supported envelope into a flat item list.
func genericItems(rawData interface{}) []genericItem {
	var items []genericItem

	appendList := func(list []interface{}) {
		for _, raw := range list {
			if m, ok := raw.(map[string]interface{}); ok {
				items = append(items, genericItem{data: m})
			}
		}
	}

	switch data := rawData.(type) {
	case []interface{}:
		appendList(data)
	case map[string]interface{}:
		// Claude-desktop style map: the key names the server.
		if m, ok := data["mcpServers"].(map[string]interface{}); ok {
			for name, raw := range m {
				if entry, ok := raw.(map[string]interface{}); ok {
					items = append(items, genericItem{name: name, data: entry})
				}
			}
		}
		for _, key := range genericListKeys {
			if list, ok := data[key].([]interface{}); ok {
				appendList(list)
				break
			}
		}
	}
	return items
}

// genericItemToEntry maps one item to a ServerEntry. keyName is the map key for
// the mcpServers form and empty otherwise. Returns ok=false when the item has no
// usable name.
func genericItemToEntry(keyName string, item map[string]interface{}) (ServerEntry, bool) {
	// A static file may hold items in the official {server, _meta} envelope (e.g.
	// a dumped registry page). Unwrap it so those entries parse here too, instead
	// of yielding a nameless item that gets dropped.
	if wrapped, ok := item["server"].(map[string]interface{}); ok {
		return genericItemToEntry(keyName, wrapped)
	}

	// An official server.json object (packages/remotes) is classified by the
	// official mapper so a static file holding real server.json entries behaves
	// identically to the same entries served by the official registry.
	if item["packages"] != nil || item["remotes"] != nil {
		entry := officialServerToEntry(item)
		if entry.Name == "" && keyName != "" {
			entry.Name, entry.ID = keyName, keyName
		}
		return entry, entry.Name != ""
	}

	name := firstString(item, "name", "title", "displayName", "display_name", "id")
	if name == "" {
		name = keyName
	}
	if name == "" {
		return ServerEntry{}, false
	}

	entry := ServerEntry{
		Name:          name,
		ID:            firstString(item, "id", "name"),
		Description:   firstString(item, "description", "summary", "short_description"),
		SourceCodeURL: genericSourceURL(item),
		InstallCmd:    genericInstallCmd(item),
	}
	if entry.ID == "" {
		entry.ID = name
	}
	// URL is the MCP endpoint and is meaningful for REMOTE servers only. A local
	// server (InstallCmd set) must leave it empty, or the add path would register
	// an http transport for a stdio server (#483).
	if entry.InstallCmd == "" {
		entry.URL = genericEndpointURL(item)
	}
	if entry.Description == "" {
		entry.Description = noDescAvailable
	}
	return entry, true
}

// genericInstallCmd derives the local launch command from the shapes a
// hand-written registry uses: a pre-rendered string, a {command,args} pair, or
// Fleur's {config:{runtime,args}}.
func genericInstallCmd(item map[string]interface{}) string {
	if cmd := firstString(item, "installCmd", "install_cmd", "install_command", "installation"); cmd != "" {
		return cmd
	}
	if cmd := commandWithArgs(item); cmd != "" {
		return cmd
	}
	// Fleur: config: { mcpKey, runtime: "uvx"|"npx", args: [...] }
	if cfg, ok := item["config"].(map[string]interface{}); ok {
		return commandWithArgs(cfg)
	}
	return ""
}

// commandWithArgs renders a {command|runtime, args} pair into a command line.
func commandWithArgs(m map[string]interface{}) string {
	command := firstString(m, "command", "runtime", "runtimeHint", "runtime_hint")
	if command == "" {
		return ""
	}
	parts := append([]string{command}, stringList(m, "args", "arguments")...)
	return strings.Join(parts, " ")
}

// stringList returns the first present array of strings among the given keys,
// skipping non-string elements.
func stringList(m map[string]interface{}, keys ...string) []string {
	var out []string
	for _, raw := range sliceField(m, keys...) {
		if s, ok := raw.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// genericEndpointURL extracts a remote MCP endpoint, accepting either a plain
// url field or a remotes[] list. Only http(s) endpoints qualify.
func genericEndpointURL(item map[string]interface{}) string {
	candidate := firstString(item, "url", "endpoint", "serverUrl", "server_url", "remote_url")
	if candidate == "" {
		for _, raw := range sliceField(item, "remotes") {
			if rem, ok := raw.(map[string]interface{}); ok {
				if candidate = firstString(rem, "url", "url_direct"); candidate != "" {
					break
				}
			}
		}
	}
	if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
		return candidate
	}
	return ""
}

// genericSourceURL extracts the source-code repository URL, which may be a
// string field or a nested repository object.
func genericSourceURL(item map[string]interface{}) string {
	if u := firstString(item, "sourceUrl", "source_url", "source_code_url", "sourceCodeUrl", "repository", "repo"); u != "" {
		return u
	}
	if repo, ok := item["repository"].(map[string]interface{}); ok {
		return firstString(repo, "url")
	}
	return ""
}
