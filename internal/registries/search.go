package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Constants for repeated strings
const (
	protocolMCPRun   = "custom/mcprun"
	protocolMCPStore = "custom/mcpstore"
	protocolDocker   = "custom/docker"
	protocolFleur    = "custom/fleur"
	protocolMCPV0    = "mcp/v0"
	protocolRemote   = "custom/remote"
	dockerProtocol   = "docker"
	noDescAvailable  = "No description available"
)

// SearchServers searches the given registry for servers matching optional tag and query
func SearchServers(ctx context.Context, registryID, tag, query string) ([]ServerEntry, error) {
	// Find registry by ID or name
	reg := FindRegistry(registryID)
	if reg == nil {
		return nil, fmt.Errorf("registry '%s' not found", registryID)
	}

	if reg.ServersURL == "" {
		return nil, fmt.Errorf("registry '%s' has no servers endpoint", reg.Name)
	}

	// Fetch servers from registry
	servers, err := fetchServers(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers from %s: %w", reg.Name, err)
	}

	// Filter results
	filtered := filterServers(servers, tag, query)

	// Set registry name for all results
	for i := range filtered {
		filtered[i].Registry = reg.Name
	}

	return filtered, nil
}

// fetchServers fetches and parses servers from a registry based on its protocol
func fetchServers(ctx context.Context, reg *RegistryEntry) ([]ServerEntry, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reg.ServersURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("registry query returned %d: %s", resp.StatusCode, resp.Status)
	}

	// Parse response JSON
	var rawData interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawData); err != nil {
		return nil, fmt.Errorf("invalid JSON from registry: %w", err)
	}

	// Process based on protocol
	servers := parseServers(rawData, reg)
	return servers, nil
}

// parseServers parses the raw JSON response based on the registry protocol
func parseServers(rawData interface{}, reg *RegistryEntry) []ServerEntry {
	var servers []ServerEntry

	switch reg.Protocol {
	case "modelcontextprotocol/registry":
		servers = parseOpenAPIRegistry(rawData)
	case protocolMCPRun:
		servers = parseMCPRun(rawData)
	case "custom/pulse":
		servers = parsePulse(rawData)
	case protocolMCPStore:
		servers = parseMCPStore(rawData)
	case protocolDocker:
		servers = parseDocker(rawData)
	case protocolFleur:
		servers = parseFleur(rawData)
	case "custom/apitracker":
		servers = parseAPITracker(rawData)
	case "custom/apify":
		servers = parseApify(rawData)
	case protocolMCPV0:
		servers = parseAzureMCPDemo(rawData)
	case protocolRemote:
		servers = parseRemoteMCPServers(rawData)
	default:
		// Default handling: try to unmarshal directly into []ServerEntry
		servers = parseDefault(rawData)
	}

	// For servers missing URLs, try to construct them if possible
	for i := range servers {
		if servers[i].URL == "" {
			servers[i].URL = constructServerURL(&servers[i], reg)
		}
	}

	return servers
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

// parseMCPRun handles MCP Run's specific API format
func parseMCPRun(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	if arr, ok := rawData.([]interface{}); ok {
		for _, item := range arr {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if slug, ok := itemMap["slug"].(string); ok && slug != "" {
				server := ServerEntry{
					ID:   slug,
					Name: slug,
				}

				// Extract description from meta if available
				if meta, ok := itemMap["meta"].(map[string]interface{}); ok {
					if desc, ok := meta["description"].(string); ok {
						server.Description = desc
					}
				}

				// For servers without descriptions, use default
				if server.Description == "" {
					server.Description = noDescAvailable
				}

				servers = append(servers, server)
			}
		}
	}

	return servers
}

// parsePulse handles Pulse registry format
func parsePulse(rawData interface{}) []ServerEntry {
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

		// Extract installation command and connection URL
		installCmd, connectURL := derivePulseServerDetails(itemMap)
		server.InstallCmd = installCmd
		server.ConnectURL = connectURL

		servers = append(servers, server)
	}

	return servers
}

// parseMCPStore handles MCP Store's specific API format
func parseMCPStore(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	if m, ok := rawData.(map[string]interface{}); ok {
		if serversData := m["servers"]; serversData != nil {
			if marshaledData, err := json.Marshal(serversData); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
		} else if packagesData := m["packages"]; packagesData != nil {
			// MCP Store might use "packages" instead of "servers"
			if marshaledData, err := json.Marshal(packagesData); err == nil {
				_ = json.Unmarshal(marshaledData, &servers)
			}
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
			server := ServerEntry{
				ID:   name,
				Name: name,
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

// parseFleur handles Fleur registry format
func parseFleur(rawData interface{}) []ServerEntry {
	servers := []ServerEntry{}

	arr, ok := rawData.([]interface{})
	if !ok {
		return servers
	}

	for _, item := range arr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this is an MCP-enabled app
		config, hasConfig := itemMap["config"].(map[string]interface{})
		if !hasConfig {
			continue
		}

		mcpKey, hasMcpKey := config["mcpKey"].(string)
		if !hasMcpKey || mcpKey == "" {
			continue // skip non-MCP entries
		}

		server := ServerEntry{
			ID:   mcpKey,
			Name: mcpKey,
		}

		// Extract description
		if desc, ok := itemMap["description"].(string); ok {
			server.Description = desc
		} else if name, ok := itemMap["name"].(string); ok {
			server.Description = name
		} else {
			server.Description = noDescAvailable
		}

		// Build installation command from config
		if runtime, ok := config["runtime"].(string); ok {
			if argsInterface, ok := config["args"].([]interface{}); ok {
				args := make([]string, 0, len(argsInterface))
				for _, arg := range argsInterface {
					if argStr, ok := arg.(string); ok {
						args = append(args, argStr)
					}
				}
				server.InstallCmd = buildFleurInstallCmd(runtime, args)
			}
		}

		servers = append(servers, server)
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

// constructServerURL tries to construct a server URL if missing
func constructServerURL(server *ServerEntry, reg *RegistryEntry) string {
	if server.URL != "" {
		return server.URL
	}

	// For some registries, we might be able to construct URLs from patterns
	switch reg.Protocol {
	case protocolMCPRun:
		if server.ID != "" {
			// Replace slashes with dashes to create valid subdomain URLs
			// e.g., "G4Vi/weather-service" becomes "https://G4Vi-weather-service.mcp.run/mcp/"
			urlSafeID := strings.ReplaceAll(server.ID, "/", "-")
			return fmt.Sprintf("https://%s.mcp.run/mcp/", urlSafeID)
		}
	case protocolMCPStore:
		if server.ID != "" {
			return fmt.Sprintf("https://api.mcpstore.co/servers/%s/mcp", server.ID)
		}
	case protocolDocker:
		if server.ID != "" {
			return fmt.Sprintf("docker://mcp/%s", server.ID)
		}
	case protocolFleur:
		if server.ID != "" {
			return fmt.Sprintf("https://api.fleurmcp.com/apps/%s/mcp", server.ID)
		}
	case dockerProtocol:
		if server.ID != "" {
			return fmt.Sprintf("docker://%s", server.ID)
		}
	}

	return ""
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

// buildFleurInstallCmd constructs installation command from runtime and args (helper for tests)
func buildFleurInstallCmd(runtime string, args []string) string {
	switch runtime {
	case "npx", "uvx", dockerProtocol:
		return runtime + " " + strings.Join(args, " ")
	case "stdio":
		return strings.Join(args, " ")
	default:
		combined := append([]string{runtime}, args...)
		return strings.Join(combined, " ")
	}
}

// derivePulseServerDetails extracts installation command and connection URL from Pulse server data (helper for tests)
func derivePulseServerDetails(itemMap map[string]interface{}) (installCmd, connectURL string) {
	// Extract package registry and name for installation command
	packageRegistry, _ := itemMap["package_registry"].(string)
	packageName, _ := itemMap["package_name"].(string)

	// Derive installation command based on registry type
	switch packageRegistry {
	case "npm":
		if packageName != "" {
			installCmd = "npx -y " + packageName
		}
	case "pypi", "pip":
		if packageName != "" {
			installCmd = "pipx run " + packageName
		}
	case "docker":
		if packageName != "" {
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

// parseAzureMCPDemo handles Azure MCP Demo registry format (mcp/v0 protocol)
func parseAzureMCPDemo(rawData interface{}) []ServerEntry {
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

		// Extract basic fields
		id, _ := itemMap["id"].(string)
		name, _ := itemMap["name"].(string)
		description, _ := itemMap["description"].(string)

		if id == "" || name == "" {
			continue
		}

		server := ServerEntry{
			ID:          id,
			Name:        name,
			Description: description,
		}

		// Extract repository information for constructing URLs
		if repo, ok := itemMap["repository"].(map[string]interface{}); ok {
			if repoURL, ok := repo["url"].(string); ok && repoURL != "" {
				// Use repository URL as the primary identifier
				// For GitHub repos, we could construct MCP endpoints, but for now use the repo URL
				server.URL = repoURL
			}
		}

		// Extract version information for additional context
		if versionDetail, ok := itemMap["version_detail"].(map[string]interface{}); ok {
			if version, ok := versionDetail["version"].(string); ok && version != "" {
				// Add version info to description if available
				if server.Description != "" {
					server.Description += " (v" + version + ")"
				}
			}
			if releaseDate, ok := versionDetail["release_date"].(string); ok && releaseDate != "" {
				server.UpdatedAt = releaseDate
			}
		}

		// Set default description if empty
		if server.Description == "" {
			server.Description = noDescAvailable
		}

		servers = append(servers, server)
	}

	return servers
}

// parseRemoteMCPServers handles Remote MCP Servers registry format (custom/remote protocol)
func parseRemoteMCPServers(rawData interface{}) []ServerEntry {
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

		// Extract basic fields from the simple format
		id, _ := itemMap["id"].(string)
		name, _ := itemMap["name"].(string)
		url, _ := itemMap["url"].(string)
		auth, _ := itemMap["auth"].(string)

		if id == "" || name == "" || url == "" {
			continue
		}

		server := ServerEntry{
			ID:   id,
			Name: name,
			URL:  url,
		}

		// Create description based on auth type and server name
		switch auth {
		case "oauth":
			server.Description = fmt.Sprintf("%s (OAuth authentication required)", name)
		case "open":
			server.Description = fmt.Sprintf("%s (Open access)", name)
		default:
			server.Description = fmt.Sprintf("%s (Authentication: %s)", name, auth)
		}

		servers = append(servers, server)
	}

	return servers
}
