package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

// fetchOfficialServers fetches (active, latest) servers from an official v0.1
// registry, following metadata.nextCursor up to officialMaxPages. The optional
// query is passed through as the registry-side `search` parameter to reduce
// pagination; surfaces still filter client-side for exactness.
//
// maxResults (0 = no cap) stops the cursor-follow loop as soon as enough
// matching servers have been collected to satisfy the caller. This matters far
// more than it sounds: the official registry currently answers in ~20s per page,
// so crawling all 20 pages to serve a `limit=3` search took over six minutes and
// simply timed out — the caller's limit was only applied AFTER the whole listing
// had been fetched. A search that the registry has already filtered server-side
// now costs one page.
func fetchOfficialServers(ctx context.Context, reg *RegistryEntry, guesser *experiments.Guesser, query string, maxResults int) ([]ServerEntry, error) {
	var all []ServerEntry
	cursor := ""
	for page := 0; page < officialMaxPages; page++ {
		servers, next, err := fetchOfficialPage(ctx, reg, query, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, servers...)

		// Enough to answer the caller? Stop paying for pages nobody will see.
		// Counted against the SAME client-side filter the caller will apply, so we
		// never stop early on a page whose entries all get filtered out.
		if maxResults > 0 && len(filterServers(all, "", query)) >= maxResults {
			break
		}

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

// fetchOfficialPage fetches and parses ONE page of an official v0.1 registry
// listing, returning its classified entries and the opaque nextCursor (empty
// when exhausted).
func fetchOfficialPage(ctx context.Context, reg *RegistryEntry, query, cursor string) ([]ServerEntry, string, error) {
	reqURL, err := buildOfficialURL(reg.ServersURL, query, cursor)
	if err != nil {
		return nil, "", fmt.Errorf("invalid registry URL %q: %w", reg.ServersURL, err)
	}

	// registryGet sets the standard headers (Accept/User-Agent/auth), checks
	// the status, and auto-retries transient failures (slow pages, 5xx/429)
	// so a single hiccup mid-pagination no longer fails the whole listing.
	body, err := registryGet(ctx, reg, reqURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch servers: %w", err)
	}

	var rawData interface{}
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, "", fmt.Errorf("invalid JSON from registry: %w", err)
	}

	servers, next := parseOfficialPage(rawData)
	return servers, next, nil
}

// officialExpansionPattern gates which typed queries are expanded: a plain
// name-like token or phrase, never a path, a markup fragment or a long string.
var officialExpansionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,63}$`)

// officialExpansionQueries returns the extra registry-side queries a typed q
// is expanded into (Spec 109 D37.2). The official registry's `search` is a
// substring of server.name only and returns names in byte order, so a plain
// `github` page is 100 alphabetical `io.github.*` entries and never reaches
// io.github.github/github-mcp-server. Two anchored queries recover it:
// `.q/` matches the publisher (owner) label and `/q` a name segment that
// starts with q. A multi-word q additionally searches its hyphenated phrase,
// because names hold no spaces. Nil when q is not expandable.
func officialExpansionQueries(q string) []string {
	words := strings.Fields(q)
	if len(words) == 0 {
		return nil
	}
	qh := strings.Join(words, "-")
	if !officialExpansionPattern.MatchString(qh) {
		return nil
	}
	out := make([]string, 0, 3)
	if len(words) > 1 {
		out = append(out, qh)
	}
	return append(out, "."+qh+"/", "/"+qh)
}

// fetchOfficialCatalog is the typed-query fetch for the official protocol
// (D37.2): the main query plus its expansions, ONE page (100) each, requested
// concurrently. Entries merge in this order: owner hits, segment hits, phrase
// hits, main hits, version-collapsed by name. The MAIN query decides whether
// the source is available: its error is returned (with whatever the
// expansions found), while an expansion failure is best-effort and ignored.
func fetchOfficialCatalog(ctx context.Context, reg *RegistryEntry, q string) ([]ServerEntry, error) {
	return fetchOfficialCatalogProgress(ctx, reg, q, nil)
}

// fetchOfficialCatalogProgress is fetchOfficialCatalog that also calls
// onProgress, serialized, with the merged hits of the queries finished so far
// each time one lands (mainLanded says the main query is among them), so a
// caller that gives up waiting can still use them: expansion hits while the
// main query is slow, and a landed main query's hits, which keep the source
// available (D37.2), while an expansion is slow. onProgress may be nil.
func fetchOfficialCatalogProgress(ctx context.Context, reg *RegistryEntry, q string, onProgress func(entries []ServerEntry, mainLanded bool)) ([]ServerEntry, error) {
	expansions := officialExpansionQueries(q)
	// Merge order: owner (".x/"), segment ("/x"), phrase ("x-y"), then main.
	var queries []string
	for _, prefix := range []string{".", "/"} {
		for _, e := range expansions {
			if strings.HasPrefix(e, prefix) {
				queries = append(queries, e)
			}
		}
	}
	for _, e := range expansions {
		if !strings.HasPrefix(e, ".") && !strings.HasPrefix(e, "/") {
			queries = append(queries, e)
		}
	}
	queries = append(queries, q)
	mainIdx := len(queries) - 1

	type pageResult struct {
		entries []ServerEntry
		err     error
	}
	var mu sync.Mutex
	mainLanded := false
	results := make([]pageResult, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Add(1)
		go func(i int, query string) {
			defer wg.Done()
			entries, _, err := fetchOfficialPage(ctx, reg, query, "")
			mu.Lock()
			defer mu.Unlock()
			results[i] = pageResult{entries: entries, err: err}
			if i == mainIdx && err == nil {
				mainLanded = true
			}
			if onProgress == nil || err != nil {
				return
			}
			var merged []ServerEntry
			for j := 0; j <= mainIdx; j++ {
				if results[j].err == nil {
					merged = append(merged, results[j].entries...)
				}
			}
			onProgress(collapseOfficialVersions(merged), mainLanded)
		}(i, query)
	}
	wg.Wait()

	var merged []ServerEntry
	for _, r := range results {
		merged = append(merged, r.entries...)
	}
	return collapseOfficialVersions(merged), results[mainIdx].err
}

// collapseOfficialVersions keeps ONE entry per server name, at the position of
// the name's first occurrence (D37.4). Without version=latest, or on a generic
// endpoint with no publication metadata, one name arrives once per published
// version. The kept entry is the one with an explicit isLatest:true, else the
// highest dotted-numeric version (a prerelease suffix is ignored), else the
// last one seen.
func collapseOfficialVersions(entries []ServerEntry) []ServerEntry {
	best := make(map[string]int, len(entries))
	order := make([]string, 0, len(entries))
	for i := range entries {
		key := entries[i].Name
		if key == "" {
			key = entries[i].ID
		}
		cur, seen := best[key]
		if !seen {
			order = append(order, key)
			best[key] = i
			continue
		}
		if !keepsCurrentVersion(&entries[i], &entries[cur]) {
			best[key] = i
		}
	}
	out := make([]ServerEntry, 0, len(order))
	for _, key := range order {
		out = append(out, entries[best[key]])
	}
	return out
}

// keepsCurrentVersion reports whether current stays the kept entry against a
// later candidate. A tie lets the later candidate win (last one seen).
func keepsCurrentVersion(candidate, current *ServerEntry) bool {
	if candidate.isLatest != current.isLatest {
		return current.isLatest
	}
	return compareDottedVersions(candidate.Version, current.Version) < 0
}

// compareDottedVersions compares two dotted-numeric versions ("1.0.10" >
// "1.0.4"), ignoring any "-prerelease" or "+build" suffix. A non-numeric
// segment reads as 0; a missing segment reads as 0.
func compareDottedVersions(a, b string) int {
	as, bs := versionSegments(a), versionSegments(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionSegments(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
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
		entry.isLatest = officialExplicitLatest(itemMap, serverMap)
		servers = append(servers, entry)
	}
	return collapseOfficialVersions(servers)
}

// officialExplicitLatest reports whether the entry's publication metadata says
// isLatest:true explicitly (absent reads as false: that is not an assertion).
func officialExplicitLatest(wrapper, server map[string]interface{}) bool {
	meta := officialMetaBlock(wrapper)
	if meta == nil {
		meta = officialMetaBlock(server)
	}
	if meta == nil {
		return false
	}
	for _, key := range []string{"isLatest", "is_latest"} {
		if b, ok := meta[key].(bool); ok {
			return b
		}
	}
	return false
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
		Title:       firstString(server, "title"),
		Version:     firstString(server, "version"),
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
