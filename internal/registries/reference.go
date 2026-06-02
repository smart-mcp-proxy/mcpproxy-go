package registries

// protocolReference is the built-in curated "reference" source. Its servers are
// shipped in-binary (no network) because the canonical @modelcontextprotocol
// reference servers currently 404 on the official registry
// (modelcontextprotocol/servers#3047). Shipping them built-in guarantees the
// basics are always discoverable, even fully offline.
//
// Its config entry uses the sentinel ServersURL "builtin://reference" purely to
// pass the "has a servers endpoint" guard; fetchServers short-circuits on the
// protocol and never performs a network request for it.
const protocolReference = "builtin/reference"

// referenceServer describes one curated reference entry.
type referenceServer struct {
	id          string
	name        string
	description string
	installCmd  string
	sourceURL   string
}

// curatedReferenceServers is the static set of @modelcontextprotocol reference
// servers. All are local/stdio: InstallCmd is set and URL is left empty so the
// add-from-registry path builds a stdio transport (never a bogus http one).
var curatedReferenceServers = []referenceServer{
	{
		id:          "reference/filesystem",
		name:        "filesystem",
		description: "Secure file operations with configurable access controls.",
		installCmd:  "npx -y @modelcontextprotocol/server-filesystem",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
	},
	{
		id:          "reference/memory",
		name:        "memory",
		description: "Knowledge-graph-based persistent memory system.",
		installCmd:  "npx -y @modelcontextprotocol/server-memory",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/memory",
	},
	{
		id:          "reference/everything",
		name:        "everything",
		description: "Reference/test server exercising all MCP protocol features.",
		installCmd:  "npx -y @modelcontextprotocol/server-everything",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/everything",
	},
	{
		id:          "reference/sequentialthinking",
		name:        "sequentialthinking",
		description: "Dynamic and reflective problem-solving through thought sequences.",
		installCmd:  "npx -y @modelcontextprotocol/server-sequential-thinking",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/sequentialthinking",
	},
	{
		id:          "reference/fetch",
		name:        "fetch",
		description: "Web content fetching and conversion for efficient LLM usage.",
		installCmd:  "uvx mcp-server-fetch",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch",
	},
	{
		id:          "reference/git",
		name:        "git",
		description: "Tools to read, search, and manipulate Git repositories.",
		installCmd:  "uvx mcp-server-git",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/git",
	},
	{
		id:          "reference/time",
		name:        "time",
		description: "Time and timezone conversion capabilities.",
		installCmd:  "uvx mcp-server-time",
		sourceURL:   "https://github.com/modelcontextprotocol/servers/tree/main/src/time",
	},
}

// referenceServers returns a fresh copy of the curated reference set as
// ServerEntry values (local/stdio, URL empty).
func referenceServers() []ServerEntry {
	servers := make([]ServerEntry, 0, len(curatedReferenceServers))
	for i := range curatedReferenceServers {
		r := &curatedReferenceServers[i]
		servers = append(servers, ServerEntry{
			ID:            r.id,
			Name:          r.name,
			Description:   r.description,
			InstallCmd:    r.installCmd,
			SourceCodeURL: r.sourceURL,
		})
	}
	return servers
}
