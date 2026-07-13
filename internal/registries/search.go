package registries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/experiments"
)

// Sentinel errors for single-server lookup. Surfaces map these to stable error
// codes (registry_not_found / server_not_found) for cross-surface consistency
// (CN-004).
var (
	ErrRegistryNotFound = errors.New("registry not found")
	ErrServerNotFound   = errors.New("server not found in registry")
)

// Constants for repeated strings
const (
	protocolDocker  = "custom/docker"
	dockerProtocol  = "docker"
	noDescAvailable = "No description available"
)

// GitHub URL pattern for matching https://github.com/<author|org>/<repo>
var githubURLPattern = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)(?:/.*)?$`)

// buildVersion holds the build-time version used in the outbound User-Agent.
// Set via SetVersion at process startup. Defaults to "dev" so tests run
// without setup. Some registries (e.g. Pulse, issue #566) reject requests
// with an empty or bare User-Agent and require a versioned one.
var buildVersion = "dev"

// SetVersion sets the version reported in the registry User-Agent header.
func SetVersion(v string) {
	if v != "" {
		buildVersion = v
	}
}

// registryUserAgent returns the versioned User-Agent for registry HTTP requests.
func registryUserAgent() string {
	return "mcpproxy/" + buildVersion
}

// SearchServers searches the given registry for servers matching optional tag and query
// with optional repository guessing and result limiting
func SearchServers(ctx context.Context, registryID, tag, query string, limit int, guesser *experiments.Guesser) ([]ServerEntry, error) {
	// Find registry by ID or name
	reg := FindRegistry(registryID)
	if reg == nil {
		return nil, fmt.Errorf("registry '%s' not found", registryID)
	}

	// FR-008: skip a key-requiring registry when no key is configured, rather
	// than performing a doomed fetch. Surfaces map ErrRegistryKeyMissing to an
	// "unavailable" marker so the overall search still succeeds.
	if err := checkRegistryKey(reg); err != nil {
		return nil, err
	}

	if reg.ServersURL == "" {
		return nil, fmt.Errorf("registry '%s' has no servers endpoint", reg.Name)
	}

	// Fetch servers from registry WITHOUT repository guessing (for performance).
	// Forward the query so search-capable protocols can filter server-side.
	servers, err := fetchServers(ctx, reg, nil, query) // Pass nil guesser to skip expensive operations
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers from %s: %w", reg.Name, err)
	}

	// Filter results BEFORE expensive repository guessing
	filtered := filterServers(servers, tag, query)

	// Apply limit BEFORE expensive repository guessing (default 10, max 50)
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if limit > 50 {
		limit = 50 // Max limit
	}

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	// NOW apply repository guessing only to the limited, filtered set
	if guesser != nil && len(filtered) > 0 {
		filtered = applyBatchRepositoryGuessing(ctx, filtered, guesser)
	}

	// Set registry name
	for i := range filtered {
		filtered[i].Registry = reg.Name
	}

	return filtered, nil
}

// FindServerByID resolves a single server within a registry by its exact ID.
// It performs a live registry fetch and is the shared resolution path used by
// every add-from-registry surface (CN-001/CN-004). Returns ErrRegistryNotFound
// when registryID does not resolve and ErrServerNotFound when no server matches.
func FindServerByID(ctx context.Context, registryID, serverID string, guesser *experiments.Guesser) (*ServerEntry, error) {
	reg := FindRegistry(registryID)
	if reg == nil {
		return nil, ErrRegistryNotFound
	}

	// Honor key-requiring registries (FR-008) on the add path too.
	if err := checkRegistryKey(reg); err != nil {
		return nil, err
	}

	// Match against the FULL registry listing, never the UI/search limit: a
	// server beyond the first page of results must still be addable. Routing the
	// add path through SearchServers truncated the listing to 50 before
	// matching, so any entry past the first page was searchable but not addable
	// (Codex RV #1).
	match, err := findServerByIDFetch(ctx, reg, serverID)
	if err != nil {
		return nil, err
	}

	// Enrich only the single matched entry (cheap) rather than the whole listing.
	if guesser != nil {
		if enriched := applyBatchRepositoryGuessing(ctx, []ServerEntry{*match}, guesser); len(enriched) > 0 {
			match = &enriched[0]
		}
	}
	match.Registry = reg.Name
	return match, nil
}

// findServerByIDFetch resolves serverID against a registry's full listing. It
// first forwards serverID as a server-side `search` hint (cheap for the
// paginating official protocol) and falls back to a full unfiltered fetch when
// the hinted search does not surface the exact entry — so the match is found
// regardless of its position in the listing.
func findServerByIDFetch(ctx context.Context, reg *RegistryEntry, serverID string) (*ServerEntry, error) {
	if servers, err := fetchServers(ctx, reg, nil, serverID); err == nil {
		if match, err := findServerByIDIn(servers, serverID); err == nil {
			return match, nil
		}
	}
	servers, err := fetchServers(ctx, reg, nil, "")
	if err != nil {
		return nil, err
	}
	return findServerByIDIn(servers, serverID)
}

// findServerByIDIn returns the first server whose ID exactly matches serverID.
// Pure (no network) so the not-found path is unit-testable.
func findServerByIDIn(servers []ServerEntry, serverID string) (*ServerEntry, error) {
	for i := range servers {
		if servers[i].ID == serverID {
			match := servers[i] // copy to avoid aliasing the slice backing array
			return &match, nil
		}
	}
	return nil, ErrServerNotFound
}

// fetchServers fetches and parses servers from a registry based on its protocol.
// The optional query is forwarded to protocols that support server-side search
// (currently the official v0.1 protocol); other protocols filter client-side.
func fetchServers(ctx context.Context, reg *RegistryEntry, guesser *experiments.Guesser, query string) ([]ServerEntry, error) {
	// The official protocol paginates (cursor follow-loop) and is handled by a
	// dedicated fetcher; the built-in reference source is served in-binary with
	// no network request at all.
	switch reg.Protocol {
	case protocolOfficial:
		return fetchOfficialServers(ctx, reg, guesser, query)
	case protocolReference:
		return referenceServers(), nil
	}

	// registryGet sets the standard headers (Accept/User-Agent/auth), checks the
	// status, and auto-retries transient failures (timeouts, 5xx/429) with
	// exponential backoff so a transient hiccup no longer fails the search.
	body, err := registryGet(ctx, reg, reg.ServersURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
	}

	// Parse response JSON
	var rawData interface{}
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, fmt.Errorf("invalid JSON from registry: %w", err)
	}

	// Process based on protocol
	servers := parseServers(ctx, rawData, reg, guesser)
	return servers, nil
}

// parseServers parses the raw JSON response based on the registry protocol
// Uses batch processing for repository guessing to improve performance
func parseServers(ctx context.Context, rawData interface{}, reg *RegistryEntry, guesser *experiments.Guesser) []ServerEntry {
	var servers []ServerEntry

	switch reg.Protocol {
	case protocolOfficial:
		// Single-page parse; the paginating fetcher (fetchOfficialServers) is the
		// normal entry point and already follows the cursor.
		servers, _ = parseOfficialPage(rawData)
	case "custom/pulse":
		servers = parsePulseWithoutGuesser(rawData) // Parse without guesser first
	case protocolDocker:
		servers = parseDocker(rawData)
	case "custom/apitracker":
		servers = parseAPITracker(rawData)
	case "custom/apify":
		servers = parseApify(rawData)
	default:
		// Default handling: try to unmarshal directly into []ServerEntry
		servers = parseDefault(rawData)
	}

	// NOTE: URL synthesis (constructServerURL) was deliberately removed. Local
	// servers MUST leave URL empty so the add path builds a stdio transport
	// (issues #483/#567); the official parser already sets URL only for true
	// remote endpoints.

	// Apply batch repository guessing if guesser is provided
	if guesser != nil {
		servers = applyBatchRepositoryGuessing(ctx, servers, guesser)
	}

	return servers
}

// applyBatchRepositoryGuessing applies repository guessing to all servers using batch processing
func applyBatchRepositoryGuessing(ctx context.Context, servers []ServerEntry, guesser *experiments.Guesser) []ServerEntry {
	if len(servers) == 0 {
		return servers
	}

	// Collect all GitHub URLs that need checking
	var githubURLs []string
	urlToServerIndex := make(map[int][]int) // Maps URL index to server indices that use it

	for i := range servers {
		server := &servers[i]
		var githubURL string

		// Check if server has a SourceCodeURL that looks like a GitHub repository
		if server.SourceCodeURL != "" && isGitHubURL(server.SourceCodeURL) {
			githubURL = server.SourceCodeURL
		}

		// Add to batch if we found a GitHub URL
		if githubURL != "" {
			// Check if we already have this URL
			urlIndex := -1
			for j, existingURL := range githubURLs {
				if existingURL == githubURL {
					urlIndex = j
					break
				}
			}

			// If URL not found, add it
			if urlIndex == -1 {
				urlIndex = len(githubURLs)
				githubURLs = append(githubURLs, githubURL)
			}

			// Map this URL index to the server index
			urlToServerIndex[urlIndex] = append(urlToServerIndex[urlIndex], i)
		}
	}

	// If no GitHub URLs found, return servers unchanged
	if len(githubURLs) == 0 {
		return servers
	}

	// Perform batch guessing
	batchResults := guesser.GuessRepositoryTypesBatch(ctx, githubURLs)

	// Apply results back to servers
	for urlIndex, guessResult := range batchResults {
		if guessResult != nil && guessResult.NPM != nil && guessResult.NPM.Exists {
			// Apply this result to all servers that use this URL
			for _, serverIndex := range urlToServerIndex[urlIndex] {
				servers[serverIndex].RepositoryInfo = guessResult
				// Set install command if not already set
				if servers[serverIndex].InstallCmd == "" {
					servers[serverIndex].InstallCmd = guessResult.NPM.InstallCmd
				}
			}
		}
	}

	return servers
}

// isGitHubURL checks if a URL is a GitHub repository URL
func isGitHubURL(url string) bool {
	return githubURLPattern.MatchString(url)
}

// parseOpenAPIRegistry handles the standard MCP Registry API format
func parseOpenAPIRegistry(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	switch data := rawData.(type) {
	case map[string]interface{}:
		// Try "servers" field first (standard)
		if serversData := data["servers"]; serversData != nil {
			if marshaledData, err := json.Marshal(serversData); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
		} else if dataField := data["data"]; dataField != nil {
			// Try "data" field (paginated response)
			if marshaledData, err := json.Marshal(dataField); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
		}
	case []interface{}:
		// Response is directly an array
		if marshaledData, err := json.Marshal(data); err == nil {
			_ = json.Unmarshal(marshaledData, &servers)
		}
	}

	return servers
}

// parseDocker handles Docker registry format
func parseDocker(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	data, ok := rawData.(map[string]interface{})
	if !ok {
		return servers
	}

	results, ok := data["results"].([]interface{})
	if !ok {
		return servers
	}

	for _, item := range results {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := itemMap["name"].(string); ok && name != "" {
			// Docker Hub's mcp/ namespace returns image repo names (e.g. "sqlite",
			// "git", "fetch"). The full reference is mcp/<name>. These images are
			// stdio MCP servers, so the canonical launch is `docker run -i --rm`.
			// We MUST set InstallCmd (not URL) — the URL field is reserved for HTTP/SSE
			// remote endpoints. See issue #483: synthesising "docker://mcp/<name>" as a
			// URL caused the frontend to register it as an http transport, leading to
			// `Post "docker://mcp/sqlite": unsupported protocol scheme "docker"`.
			server := ServerEntry{
				ID:         name,
				Name:       name,
				InstallCmd: fmt.Sprintf("docker run -i --rm mcp/%s", name),
			}

			// Try to get description from images array
			if images, ok := itemMap["images"].([]interface{}); ok && len(images) > 0 {
				if firstImage, ok := images[0].(map[string]interface{}); ok {
					if description, ok := firstImage["description"].(string); ok {
						server.Description = description
					}
				}
			}

			// Fallback to short_description if images description not found
			if server.Description == "" {
				if desc, ok := itemMap["short_description"].(string); ok {
					server.Description = desc
				}
			}

			if server.Description == "" {
				server.Description = noDescAvailable
			}

			// Extract last_updated as updatedAt
			if lastUpdated, ok := itemMap["last_updated"].(string); ok {
				server.UpdatedAt = lastUpdated
			}

			servers = append(servers, server)
		}
	}

	return servers
}

// parseAPITracker handles APITracker registry format
func parseAPITracker(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	switch data := rawData.(type) {
	case map[string]interface{}:
		if serversData := data["servers"]; serversData != nil {
			if marshaledData, err := json.Marshal(serversData); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
		} else if packagesData := data["packages"]; packagesData != nil {
			if marshaledData, err := json.Marshal(packagesData); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
		} else if itemsData := data["items"]; itemsData != nil {
			if marshaledData, err := json.Marshal(itemsData); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
		}
	case []interface{}:
		if marshaledData, err := json.Marshal(data); err == nil {
			_ = json.Unmarshal(marshaledData, &servers)
		}
	}

	return servers
}

// parseApify handles Apify registry format
func parseApify(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	rootData, ok := rawData.(map[string]interface{})
	if !ok {
		return servers
	}

	// Look for data.items structure
	dataField, ok := rootData["data"].(map[string]interface{})
	if !ok {
		return servers
	}

	items, ok := dataField["items"].([]interface{})
	if !ok {
		return servers
	}

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := itemMap["name"].(string); ok && name != "" {
			server := ServerEntry{
				ID:   name,
				Name: name,
			}

			// Use title as Name if available
			if title, ok := itemMap["title"].(string); ok {
				server.Name = title
			}

			if desc, ok := itemMap["description"].(string); ok {
				server.Description = desc
			}

			if server.Description == "" {
				server.Description = noDescAvailable
			}

			// Extract stats for the updated date
			if stats, ok := itemMap["stats"].(map[string]interface{}); ok {
				if lastRunStartedAt, ok := stats["lastRunStartedAt"].(string); ok {
					server.UpdatedAt = lastRunStartedAt
				}
			}

			servers = append(servers, server)
		}
	}

	return servers
}

// parseDefault handles unknown registry formats
func parseDefault(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	if rawData == nil {
		return servers
	}

	if marshaledData, err := json.Marshal(rawData); err == nil {
		_ = json.Unmarshal(marshaledData, &servers)
	}

	return servers
}

// createServerEntry creates a basic server entry from partial data (helper function)
func createServerEntry(data map[string]interface{}) ServerEntry {
	server := ServerEntry{}

	if id, ok := data["id"].(string); ok {
		server.ID = id
	}
	if name, ok := data["name"].(string); ok {
		server.Name = name
	}
	if desc, ok := data["description"].(string); ok {
		server.Description = desc
	}
	if url, ok := data["url"].(string); ok {
		server.URL = url
	}

	return server
}

// filterServers filters servers by tag and query
func filterServers(servers []ServerEntry, tag, query string) []ServerEntry {
	if tag == "" && query == "" {
		return servers
	}

	filtered := []ServerEntry{}
	for i := range servers {
		srv := &servers[i]

		// Filter by query (search in name and description)
		if query != "" {
			q := strings.ToLower(query)
			name := strings.ToLower(srv.Name)
			desc := strings.ToLower(srv.Description)

			if !strings.Contains(name, q) && !strings.Contains(desc, q) {
				continue
			}
		}

		filtered = append(filtered, *srv)
	}

	return filtered
}

// parsePulseWithoutGuesser handles Pulse registry format without repository guessing
func parsePulseWithoutGuesser(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	data, ok := rawData.(map[string]interface{})
	if !ok {
		return servers
	}

	serversData, ok := data["servers"]
	if !ok {
		return servers
	}

	serversArray, ok := serversData.([]interface{})
	if !ok {
		return servers
	}

	for _, item := range serversArray {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := itemMap["name"].(string)
		if !ok || name == "" {
			continue
		}

		server := ServerEntry{
			ID:   name,
			Name: name,
		}

		// Try to get description from multiple fields
		if shortDesc, ok := itemMap["short_description"].(string); ok && shortDesc != "" {
			if len(shortDesc) > 300 {
				server.Description = shortDesc[:300]
			} else {
				server.Description = shortDesc
			}
		} else if aiDesc, ok := itemMap["EXPERIMENTAL_ai_generated_description"].(string); ok && aiDesc != "" {
			if len(aiDesc) > 300 {
				server.Description = aiDesc[:300]
			} else {
				server.Description = aiDesc
			}
		} else {
			server.Description = noDescAvailable
		}

		// Extract installation command and connection URL (without guesser)
		installCmd, connectURL := derivePulseServerDetailsWithoutGuesser(itemMap)
		server.InstallCmd = installCmd
		server.ConnectURL = connectURL

		// Store source_code_url for later batch processing
		if sourceCodeURL, ok := itemMap["source_code_url"].(string); ok && sourceCodeURL != "" {
			server.SourceCodeURL = sourceCodeURL
		}

		servers = append(servers, server)
	}

	return servers
}

// derivePulseServerDetailsWithoutGuesser extracts installation command and connection URL from Pulse server data
// Does not use guesser - only uses existing package_registry and package_name data
func derivePulseServerDetailsWithoutGuesser(itemMap map[string]interface{}) (installCmd, connectURL string) {
	// Extract package registry and name for installation command
	packageRegistry, _ := itemMap["package_registry"].(string)
	packageName, _ := itemMap["package_name"].(string)

	// If package registry and name are available, use them
	if packageRegistry != "" && packageName != "" {
		// Derive installation command based on registry type
		switch packageRegistry {
		case "npm":
			installCmd = "npx -y " + packageName
		case "pypi", "pip":
			installCmd = "pipx run " + packageName
		case "docker":
			installCmd = "docker run -i --rm " + packageName
		}
	}

	// Extract remote connection URL if available
	if remotesInterface, ok := itemMap["remotes"].([]interface{}); ok {
		for _, remote := range remotesInterface {
			if remoteMap, ok := remote.(map[string]interface{}); ok {
				if urlDirect, ok := remoteMap["url_direct"].(string); ok && urlDirect != "" {
					connectURL = urlDirect
					break // Use first available direct URL
				}
			}
		}
	}

	return installCmd, connectURL
}
