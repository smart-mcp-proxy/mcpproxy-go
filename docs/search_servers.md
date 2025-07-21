Search Servers Feature Design for MCPProxy

🔬 Repository Detection & Install Command Enhancement (NEW)

The search_servers functionality now includes an experimental repository detection feature that automatically identifies whether MCP servers are available as npm or PyPI packages. This feature enhances the search results with accurate installation commands and package information.

**Key Features:**
• **Automatic Package Detection**: Uses HTTP calls to npm registry (registry.npmjs.org) and PyPI JSON API (pypi.org/pypi) to detect if servers are published as packages
• **Smart Install Commands**: Generates accurate install commands (npm install package-name or pip install package-name) when packages are detected  
• **Intelligent Caching**: Implements caching with 6-hour TTL to reduce API calls and improve performance
• **Configurable**: Can be enabled/disabled via the `check_server_repo` configuration parameter (enabled by default)
• **Result Limits**: Enforces default limit of 10 results (maximum 50) to ensure reasonable response times

**Configuration:**
```json
{
  "check_server_repo": true,  // Enable repository detection (default: true)
  "listen": ":8080",
  // ... other config options
}
```

**Enhanced Output Format:**
Results now include repository information when packages are detected and clearly separate MCP endpoints from source code repositories:

**Field Descriptions:**
- `url`: **MCP endpoint URL only** - Direct connection URL for remote MCP servers (e.g., `https://weather.example.com/mcp`)
- `source_code_url`: **Source code repository URL** - Link to the source code repository (e.g., GitHub URLs)
- `connectUrl`: Alternative connection URL for remote servers
- `installCmd`: Command to install the server locally (npm/pip)

```json
[
  {
    "id": "weather-service",
    "name": "Weather MCP Server",
    "description": "Provides weather data via MCP",
    "url": "https://weather.example.com/mcp",
    "source_code_url": "https://github.com/example/weather-mcp-server",
    "installCmd": "npm install weather-mcp-server",
    "repository_info": {
      "npm": {
        "type": "npm",
        "package_name": "weather-mcp-server", 
        "version": "1.2.3",
        "description": "Weather MCP server package",
        "install_cmd": "npm install weather-mcp-server",
        "url": "https://www.npmjs.com/package/weather-mcp-server",
        "exists": true
      }
    },
    "registry": "Example Registry"
  }
]
```

📌 Functional Spec & Example Input/Output

Objective: Extend mcpproxy with a new search_servers capability, enabling discovery of Model Context Protocol (MCP) servers from a built-in registry-of-registries. This mirrors the functionality of Mastra’s registryServers tool – allowing users or AI agents to list available MCP servers by querying known registries ￼. The feature will support filtering by a specific registry (exact match on registry ID or name) as well as optional search terms and tags for narrowing results.

User Workflow:
	•	Specify Registry: The user (or agent) can target a particular MCP registry by providing its ID or exact name via a CLI flag or tool input (e.g. --registry mcprun). If a registry filter is given, search_servers will query only that registry. Without this filter, the command can either (a) list all known registries or (b) search all registries. For this design, we assume an explicit registry is required (similar to Mastra’s approach), to avoid heavy multi-registry queries. Attempting to search without specifying a registry will prompt the user to supply one or list available registries.
	•	Search and Tag Filters: The user may also provide a search term to match against server names/descriptions, and/or a tag to filter results. The search is case-insensitive and matches substrings in the server’s name or description ￼. (Mastra’s implementation demonstrates filtering server entries by search term ￼.) If a tag is provided and the target registry categorizes servers by tags, the results will be filtered accordingly (though many MCP server listings may not have tags on individual servers).
	•	Output: The output will be a structured list of servers (e.g. in JSON or table form) rich enough to directly feed into the upstream_servers add command. Specifically, each result will include all details needed to add that server as an upstream in MCPProxy. This includes at least a human-friendly name, a description, and crucially the connection endpoint or URL of the MCP server. By providing the server’s address in the search results, the user/agent can immediately invoke the add command without making another lookup call. For example, if searching the “MCP Run” registry for “weather”, the output might be:

[
  {
    "registry": "MCP Run",
    "id": "weather",
    "name": "WeatherInfo",
    "description": "Provides real-time weather data",
    "url": "https://weather.mcp.run/mcp/",
    "source_code_url": "https://github.com/mcprun/weather-service",
    "updatedAt": "2025-05-01T12:00:00Z"
  }
]

In this example, the result indicates a server named “WeatherInfo”, with a base MCP endpoint URL for direct connection and a separate source code repository URL. The user or agent can then call:

mcpproxy upstream_servers add --name "WeatherInfo" --url "https://weather.mcp.run/mcp/"

or equivalently invoke the upstream_servers add tool with the given URL to integrate the server. The goal is that no further registry queries are needed after search_servers – the result is immediately actionable. (If multiple results are returned, the user can pick one and supply its URL to the add command.)

	•	Error Handling: If the specified registry ID is not found in the embedded registry list, an error is returned (e.g. “Registry with ID ‘X’ not found”). If the registry is found but has no known servers_url (no server listing endpoint), an error is returned indicating that the registry cannot provide server data. Network or parsing errors during fetch will be surfaced as well (with clear error messages). These mirror error conditions tested in Mastra’s getServersFromRegistry (e.g. error when registry ID is invalid ￼).
	•	Example CLI Usage:

# Example 1: Search "MCP Run" registry for servers with "weather" in name or description
mcpproxy search-servers --registry mcprun --search weather

# Example 2: List all servers from the "Pulse MCP" registry tagged as "finance" (limit to 5 results)
mcpproxy search-servers --registry pulse --tag finance --limit 5

# Example 3: Search with custom limit (max 50)
mcpproxy search-servers --registry smithery --search "database" --limit 20

# Example 4: List all known registries
mcpproxy search-servers --list-registries

# Example 5: MCP Tool Usage (with repository detection)
{
  "name": "search_servers",
  "arguments": {
    "registry": "pulse",
    "search": "weather",
    "limit": 10
  }
}

Example Output (for Example 1):

[
  {
    "registry": "MCP Run",
    "id": "weather",
    "name": "WeatherInfo",
    "description": "Provides real-time weather data",
    "url": "https://weather.mcp.run/mcp/",
    "source_code_url": "https://github.com/mcprun/weather-service",
    "updatedAt": "2025-05-01T12:00:00Z"
  },
  {
    "registry": "MCP Run",
    "id": "weather-forecast",
    "name": "ForecastPro",
    "description": "7-day weather forecast tool",
    "url": "https://forecast.mcp.run/mcp/",
    "source_code_url": "https://github.com/mcprun/forecast-pro",
    "updatedAt": "2025-04-20T08:30:00Z"
  }
]

In a real scenario, the above JSON could be printed to the console or returned as an MCP tool response. The user/agent sees two matching servers from MCP Run and can choose one to add. Each entry includes url (the base endpoint to connect to the MCP server) and source_code_url (link to the source repository) so that no additional lookup is required beyond this search.

⚙️ Architecture & Flowchart

The search_servers feature will be implemented with a modular, layered design to keep it decoupled and testable. The core idea is to embed a static registry-of-registries (a list of known MCP registries and how to query them) into the proxy, and to provide logic that:
	1.	Loads Registry Metadata: At startup, MCPProxy loads a compiled-in JSON (or Go data) structure containing all known registries and their metadata. This registry list is similar to Mastra’s registryData object ￼ ￼, but defined in Go and embedded via the embed package (see Embedded Config below).
	2.	User Invocation: The user or AI agent invokes search_servers (via CLI command or MCP tool call). Input parameters include:
	•	registryId or name to specify which registry to query (required for network search).
	•	Optional search term (string) to filter server results by name/description.
	•	Optional tag to filter server results by tag/category (if applicable).
	3.	Registry Lookup: The search_servers handler looks up the registry entry in the loaded metadata by the given ID/name. This yields a RegistryEntry struct containing fields like the registry’s name, base URL, servers_url (endpoint for servers listing), supported protocol, and possibly a pointer to a custom parser function if needed (analogous to Mastra’s postProcessServers ￼).
	4.	Dispatch to Fetcher: Based on the registry’s metadata, the system decides how to fetch and interpret the server list:
	•	If the registry entry specifies a protocol type (e.g. "modelcontextprotocol/registry", or "custom/apify", etc.), a corresponding fetcher/processor will be used. Each known protocol or format is handled by a small module or function. For example:
	•	A registry with protocol: "modelcontextprotocol/registry" uses a generic OpenAPI fetcher that calls the registry’s /v0/servers endpoint and expects a JSON response containing a list of servers.
	•	A registry with custom format like Apify or Docker uses a specialized parser (similar to Mastra’s processApifyServers, processDockerServers, etc. ￼ ￼). These parse the specific JSON or HTML formats of those sources.
	•	If a registry entry has a dedicated post-processing function in the metadata, we invoke it to transform the raw fetched data into our standard ServerEntry list. (This mirrors Mastra’s logic: fetch JSON from servers_url, then if postProcessServers is defined, use it ￼.)
	•	If no special protocol or parser is specified (and the data format is assumed to already match our ServerEntry schema), a default handler will be used. The default will attempt to parse the fetched JSON in a standard way. For example, if the response JSON has a top-level "servers" array (as in the MCP Registry OpenAPI), the default handler will extract that. If the response is directly an array of server objects, it will parse it directly into []ServerEntry. This ensures that even registries without custom logic can be integrated as long as they return a reasonable JSON format (or can be extended in the future).
	5.	Fetching Data: The appropriate fetcher then makes an HTTP GET request to the registry’s servers_url. For instance, if querying the “Pulse MCP” registry, it will GET https://api.pulsemcp.com/v0beta/servers ￼. The design uses Go’s built-in net/http (with context and timeouts) to retrieve the data. Note: All registry endpoints in the embedded list are assumed to be public and require no auth (if some do, we might extend the metadata with auth info, but out of scope for now).
	6.	Parse & Normalize: The raw response is parsed into a list of ServerEntry objects. Each ServerEntry represents one MCP server with fields like id, name, description, and possibly updatedAt/createdAt. Crucially, we also include a field for the server’s connection URL or address (populated by the fetcher if available, or constructed if needed). The custom processors will ensure this structure is populated:
	•	OpenAPI/Registry Example: The JSON from an OpenAPI-based registry likely includes id, name, description, etc., but may not directly provide the server’s URL. In such cases, our fetcher can perform an additional step: for each server entry, call the registry’s server detail endpoint (e.g. /v0/servers/{id}) to retrieve its base_url or connection info, if available. This extra call happens within the search_servers execution, so the user still experiences a single operation. (If the registry returns the base URL in the listing or uses the id as a URL, we can skip this step. The system is designed to accommodate either scenario.)
	•	Custom Parsers: For sources like Apify or Docker Hub that list tools, the parser might construct an MCP endpoint URL from known patterns or metadata. For example, if Docker MCP Catalog lists image names, the parser might translate those to a hosted endpoint URL template. Each custom parser will output standardized ServerEntry objects.
	7.	Filter Results: Once the full list of servers is obtained, the system filters them based on the user’s query parameters:
	•	Search Term: If search was provided, filter out any servers whose name and description do not contain the term (case-insensitive) ￼.
	•	Tag: If tag was provided, and if our ServerEntry or extended metadata includes tags or categories for servers, filter by those. (In many cases, server entries might not have tags; in such cases this filter yields either unfiltered or empty if expecting a tag that isn’t present.)
	•	Result Count: The filtered list (or full list if no filters) is the final result. We may also include a count of servers in the output for user information.
	8.	Output Construction: The output is formatted as a JSON array of server entries (as shown in the example above). This JSON is printed to STDOUT for CLI, and also returned as the content of the MCP tool response if search_servers is invoked via the MCP protocol. We ensure that each entry includes the url (connection endpoint) along with human-readable name/description. This satisfies the requirement that a user/agent can directly use the output to add a server without further queries.
	9.	Integration with Upstream Add: There is no direct invocation of upstream_servers add within search_servers (we keep tools decoupled), but by design the user can seamlessly chain the two. For instance, if only one server is returned by search, the CLI might display a message like “Found 1 server. Run mcpproxy upstream_servers add --url <URL> to add it.” (The MCP tool response could likewise include an advisory.) In an AI agent scenario, the agent could parse the JSON and then call the add tool with the provided URL programmatically.

Below is a high-level flowchart of the process:

User/Agent -> [search_servers Tool] -> (Load embedded registry list)
    -> Find registry by ID
    -> Fetch servers (HTTP GET to registry.servers_url)
    -> Parse JSON (use custom or default handler)
    -> Filter results (search term, tag)
    -> Return list of ServerEntry (with connection URLs)
User/Agent -> [upstream_servers add Tool] -> Add chosen server to upstreams

Each step is designed to be testable in isolation: e.g., the fetch logic can be unit-tested with mocked HTTP responses for a given registry, and filtering logic can be tested with synthetic data (as is done in Mastra ￼ ￼).

📦 Module Layout in Go

To implement this cleanly, we introduce a new internal package and extend a few existing components:
	•	internal/registries (new package): Encapsulates registry metadata and search logic.
	•	registry_data.go: Contains the embedded registry list (JSON data) and code to load/unmarshal it into Go structs at startup.
	•	types.go: Defines data structures:
	•	RegistryEntry – representing a registry (fields: ID, Name, Description, URL, optional ServersURL, optional Tags[], optional Count, and possibly Protocol or Type to indicate how to fetch). This maps to entries in the static list (e.g. id: “mcprun”, servers_url: “…/api/servlets”, tags: [“verified”] ￼, etc.).
	•	ServerEntry – representing an MCP server discovered via a registry (fields: ID, Name, Description, UpdatedAt, etc., plus a URL field for the server’s endpoint). This aligns with the MCP Registry schema (ID, name, description, timestamps) ￼, extended with our url for convenience.
	•	fetch.go (or part of search.go): Implements functions to fetch and parse servers from a registry. For example:
	•	FetchServers(reg RegistryEntry) ([]ServerEntry, error) – core logic that dispatches to the correct handler based on reg.Protocol or known reg.ID.
	•	Internally, this may call specific parsing functions or methods in sub-packages or closures (e.g., fetchOpenAPIRegistryServers(reg), fetchMcpRunServers(reg), etc.) similar to Mastra’s processor modules ￼ ￼ but in Go. These can live in internal/registries or a subdirectory (registries/processors) for clarity.
	•	search.go: Implements the high-level search operation:
	•	SearchServers(registryID string, tag string, query string) ([]ServerEntry, error) – orchestrates the steps: find registry by ID, call FetchServers, apply filtering on the result list, and return the filtered list. This function is the main entry point used by the CLI command or MCP tool handler.
	•	Filtering subroutines can be included here (e.g., filterServers(list, tag, query)), or simply done inline since it’s straightforward (loop and match substrings/tag).
	•	Testing: internal/registries/search_test.go etc., to unit test filtering and perhaps use stub HTTP servers to simulate registry responses. (We can inject a custom HTTP client or use Go’s http.TestServer to feed known JSON and verify parsing.)
	•	cmd/mcpproxy (CLI integration):
	•	If mcpproxy uses a CLI library (like Cobra), we add a new subcommand search-servers (or search_servers) in cmd/mcpproxy/main.go or a sub-file (e.g., cmd/mcpproxy/search.go). This sets up flags --registry, --search, --tag and when invoked, calls internal/registries.SearchServers with the provided flags, then prints the results.
	•	The CLI output will pretty-print the JSON array. Alternatively, for a friendlier CLI, it could format results in a table (with columns Name, Description, URL), but JSON is straightforward and also what the MCP tool interface will use. We can start with JSON output to align with how other tools may output raw data (Mastra’s registryServers tool returns JSON as text content ￼).
	•	Example integration in main.go: after setting up other commands, something like:

proxy.RegisterTool(searchservers.Tool()) 
// if the proxy exposes tools to MCP clients

and for CLI, adding:

app.Command("search-servers", "Search for MCP servers in known registries",
    func(cmd *Command) {
       // parse flags and call internal/registries.SearchServers
    })

(Pseudo-code, actual implementation depends on the CLI framework in use.)

	•	MCP Tool Interface:
	•	MCPProxy likely has a mechanism to expose certain functions as MCP “tools” to connected clients (so that an AI agent can call them). We will register search_servers as a new tool in the proxy’s tool registry, similar to how upstream_servers and others are registered. For example, if there’s a struct of built-in tools, we add:

{
  Name: "search_servers",
  Description: "Discover MCP servers from known registries. Filter by registry, tag, or name.",
  Inputs: schema defining `registryId`, `tag`, `search` (all strings),
  Handler: function(ctx, inputs) -> executes SearchServers and returns results (as JSON text content or structured).
}

This parallels Mastra’s tool definitions ￼ ￼ but in Go. The output content will be a JSON string or object representing the servers list. By embedding the search_servers logic into the running proxy, AI agents connected to MCPProxy can invoke it just like any other tool to find new capabilities on the fly.

	•	internal/upstream (upstream server management):
	•	The existing upstream management (responsible for upstream_servers add/remove) likely resides here. We do not need to heavily modify this for search_servers, but we ensure the add path can accept the output from search. Typically, upstream_servers add might accept a URL and optional name. Our search output provides those. If needed, we could enhance add to accept a JSON blob or structured input directly, but that’s not necessary – the user/agent can pass the URL and name as separate arguments.
	•	One potential extension: allow add to reference a registry entry directly (like --registry X --id Y), upon which the proxy could internally look up the details (similar to how search_servers does) and then add. However, this adds complexity and overlaps with search_servers, so we opt not to implement that now. Instead, the separation of concerns remains: search_servers finds and returns info; upstream_servers add simply takes the info and adds the server.

This module layout ensures low coupling: the registry search logic is self-contained and interacts with the rest of the system through well-defined interfaces (function calls for CLI or tool registration). Other parts of the proxy (like indexing or OAuth) remain unaffected. We also make it easy to extend: adding a new known registry later means updating the JSON and possibly adding a parser function in internal/registries without touching unrelated code.

🔐 Embedded Registries Config Layout

We will embed a static configuration file (e.g. registries.json) into the Go binary using Go’s embed package (Go 1.16+). This approach ensures the registry metadata is versioned with the binary and doesn’t rely on external files at runtime ￼. It’s the recommended practice for bundling read-only data like presets or seeds in Go.

File: internal/registries/registries.json (to be embedded). This JSON will contain an array of registry definitions, for example:

{
  "registries": [
    {
      "id": "mcprun",
      "name": "MCP Run",
      "description": "One platform for vertical AI across your organization.",
      "url": "https://www.mcp.run/",
      "servers_url": "https://www.mcp.run/api/servlets",
      "tags": ["verified"],
      "protocol": "custom/mcprun"
    },
    {
      "id": "pulse",
      "name": "Pulse MCP",
      "description": "Browse and discover MCP use cases, servers, clients, and news.",
      "url": "https://www.pulsemcp.com/",
      "servers_url": "https://api.pulsemcp.com/v0beta/servers",
      "tags": ["verified"],
      "protocol": "custom/pulse"
    },
    {
      "id": "smithery",
      "name": "Smithery",
      "description": "Extend your agent with 4,274 capabilities via MCP servers.",
      "url": "https://smithery.ai/",
      "servers_url": "https://registry.smithery.ai/servers",
      "tags": ["verified"],
      "protocol": "modelcontextprotocol/registry"
    },
    {
      "id": "modelcontextprotocol-official",
      "name": "Model Context Protocol Registry",
      "description": "Official community-driven registry of MCP servers.",
      "url": "http://localhost:8080", 
      "servers_url": "http://localhost:8080/v0/servers",
      "tags": ["official"],
      "protocol": "modelcontextprotocol/registry"
    }
    // ... (other entries)
  ]
}

This is an illustrative snippet. In practice, we will include all relevant registries (Mastra’s list contains ~17 entries ￼ ￼, which we can adapt). Each entry has:
	•	id: a unique key (short string) for the registry.
	•	name: human-friendly name.
	•	description: brief info.
	•	url: homepage or base URL.
	•	servers_url: endpoint to fetch the list of servers (if available).
	•	tags: categories/labels (e.g. “verified”, “open-source”, “official”).
	•	protocol/type: (added by us) indicating how to handle this registry’s data. For example, "modelcontextprotocol/registry" for any registry conforming to the MCP Registry API (OpenAPI) ￼; "custom/..." or omitted for those requiring a custom parse.

Loading the Data: In registry_data.go, we will use //go:embed to embed this JSON file as a string or bytes. On initialization, we parse the JSON into a struct like RegistryFile { Registries []RegistryEntry }. We will validate the structure (e.g., ensuring required fields like id, name, url are present) similar to Mastra’s schema validation with Zod ￼ (in Go we can simply rely on JSON parsing and maybe post-validate key fields). If parsing fails, the proxy can log a critical error and fall back to an empty list (though a compile-time known JSON should not fail).

Embedding ensures the data is read-only and secure (cannot be tampered by user except via new binary). If updates are needed (e.g., new registries), releasing a new version of MCPProxy with an updated JSON is expected. This approach also avoids any startup latency or failure from fetching registry info from an external source; everything needed for discovery is baked in.

Registry Entry Config Examples: We base these largely on known sources:
	•	MCP Run: has a JSON API of servlets, requiring a custom parser (marked custom/mcprun).
	•	Pulse MCP: has a /v0beta/servers endpoint returning a list of servers (likely needs custom parser for its specific schema, hence custom/pulse).
	•	Smithery: likely implements the standard MCP Registry API (note the /servers endpoint and the large count ~4274 ￼). We tag it as modelcontextprotocol/registry to use the generic OpenAPI fetching logic.
	•	Official MCP Registry: (if one is running, e.g., the open-source project on localhost:8080 as per dev) – also uses modelcontextprotocol/registry.
	•	Others (Apify, Docker, etc.) can be included with protocol: custom/... if we plan to support them via specialized parsers (e.g., Apify’s API format).

Having the protocol field explicitly in the JSON makes the dispatch logic straightforward and data-driven, rather than hard-coding based on the registry ID. It’s easy to extend: a new protocol type would correspond to writing a new handler function and marking relevant entries with that type.

📥 API and CLI Extensions

CLI Extension: We introduce a new top-level CLI command search-servers (or subcommand under a grouping, depending on CLI design). The usage is as described: mcpproxy search-servers --registry <id> [--search <term>] [--tag <tag>]. This command’s implementation will:
	•	Parse flags and validate input (require --registry unless perhaps --list-registries is used).
	•	Call internal/registries.SearchServers(reg, tag, term).
	•	If results are returned, format them as JSON and print to console. If no results, output a friendly message, e.g., “No servers found for registry X and query Y.”
	•	If the user requested to list registries (e.g., a --list-registries flag or using the command without a registry), we will output the embedded registries list (perhaps in summary form: ID, Name, Description, Tags), to help the user decide which to search. This uses the same embedded data (internal/registries can provide a function for listing registries, filtered or not, similar to Mastra’s registryList ￼).

MCP Tool Interface: On the protocol level, MCPProxy acts as an MCP server to clients (like IDEs or AI agents). We will expose search_servers as a tool in the MCP protocol sense, so that an agent can do something like call_tool("search_servers", {"registry": "mcprun", "search": "weather"}). Under the hood, this triggers our SearchServers function, and the results are returned in the MCP response JSON. The tool schema will be defined (likely using the MCP Go library to register tools). For example:

tool := mcp.Tool{
    Name: "search_servers",
    Description: "Search known MCP registries for servers. Filter by registry ID (exact), and optionally by tag or name.",
    InputSchema: map[string]*mcp.Schema{
        "registry": {Type: mcp.String, Description: "Exact registry ID or name to search in"},
        "search":   {Type: mcp.String, Description: "Search term for server name/description (optional)"},
        "tag":      {Type: mcp.String, Description: "Filter by server tag (optional)"},
    },
    OutputSchema: ... // e.g. array of ServerEntry objects (with fields id, name, description, url, etc.)
    Handler: func(ctx context.Context, input map[string]interface{}) (interface{}, error) {
        reg := input["registry"].(string)
        term := input["search"].(string)
        tag := input["tag"].(string)
        servers, err := registries.SearchServers(reg, tag, term)
        if err != nil { return nil, err }
        return servers, nil  // the MCP framework will encode this as JSON in response
    },
}
proxy.RegisterTool(tool)

(Above is conceptual; actual integration depends on the mcp-go library’s patterns.)

By doing this, any AI agent connected to MCPProxy can invoke search_servers as a tool, get the list of new servers, and then invoke upstream_servers add likewise to add one. This is extremely powerful: it enables self-service discovery of new tools by the agent. For instance, an agent could decide it needs a particular capability, use search_servers to find an appropriate MCP server providing it, then add it to the proxy, and finally use that new server’s tools – all autonomously.

HTTP API (if any): MCPProxy primarily focuses on the MCP protocol and CLI, not a REST API. We won’t create a separate HTTP endpoint for searching, since the MCP tool interface covers programmatic access. (The proxy’s HTTP endpoints are usually for the unified MCP endpoint /mcp/ and OAuth callbacks, not for management operations.) The CLI and MCP tool suffice for users and agents respectively.

Configuration: No new user configuration is required for this feature, since the registry list is baked in. We may add a toggle in config (e.g., to disable network access for search_servers if needed for offline mode), but by default it will be enabled. The search will respect any relevant global settings like timeouts (we might use a reasonable default timeout for the HTTP fetch, e.g. 5 seconds, to avoid hanging if a registry is down).

Security Considerations: All search_servers operations are read-only and target public endpoints. Still, the proxy should guard against potential issues:
	•	If any registry servers_url is HTTP (not HTTPS), we should consider marking it insecure or possibly disallow by default (could be overridden if needed). In the example above, mcpstore.co uses HTTP ￼; perhaps we avoid such cases or handle carefully.
	•	The JSON parsing should be done with proper error handling to avoid panics. Malformed or unexpected data should result in a clean error message to the user.
	•	We might implement rate-limiting or caching if a user calls search_servers very frequently, to avoid spamming registry endpoints. A simple caching strategy could be to remember results for a short period (e.g., one minute) for the same registry query, since these listings don’t change frequently. However, initially, a fresh fetch each time is acceptable.

🔁 Chaining search_servers → upstream_servers add

One of the primary use-cases of search_servers is to streamline adding new upstream servers to MCPProxy. The design ensures that this chaining is as smooth as possible:
	1.	Seamless Data Handover: The output of search_servers provides all necessary info to add a server. In practice, the upstream_servers add tool typically needs a server identifier (likely a URL or address, and perhaps a name). Our ServerEntry.url fulfills the address requirement, and ServerEntry.name can serve as a default friendly name. For example, if search_servers returns an entry:

{ "id": "weather", "name": "WeatherInfo", "url": "https://weather.mcp.run/mcp/" }

a user could do:

mcpproxy upstream_servers add --name "WeatherInfo" --url "https://weather.mcp.run/mcp/"

If the CLI for add doesn’t require --name (it might derive from URL or allow omission), the user could just provide the URL. The key is that the URL is on hand from the search result.

	2.	Direct Invocation in MCP context: If an AI agent is orchestrating this, it would call the search_servers tool via MCP, parse the JSON result it gets, then programmatically call upstream_servers add with the included URL. No further lookup or human input is needed. The agent doesn’t need to call any external API or have prior knowledge of the server’s address – it’s all provided by the proxy’s search. This meets the goal of “without additional calls” (beyond the two tool calls themselves).
	3.	Internal Chaining (Future Enhancement): While not required now, the architecture allows an interesting future feature: the search_servers tool could be extended to accept a flag like --auto-add or even a query like “search and add the first result”. This would combine the search and add steps internally. We mention this as a possibility, but our current design keeps them separate for clarity and user control (especially since search can return multiple servers, and choosing which to add is a decision we leave to the user/agent).
	4.	Example Scenario: Suppose an agent wants to translate text to Morse code but none of the current upstreams have that capability. The agent can:
	•	Call search_servers with search="Morse", possibly across all registries or a specific one known to host community tools.
	•	The proxy returns a server entry “MorseCoder” with URL wss://morse.api.example/mcp (maybe a WebSocket MCP server).
	•	The agent then calls upstream_servers add with that URL (and a name). The proxy adds it and immediately indexes its tools (the proxy’s indexer will detect a new upstream and pull its tool list).
	•	Now the agent sees a new tool “Translate to Morse” available via the unified proxy, and can invoke it as if it was always there.
	•	Total calls by agent: 1 to search, 1 to add – no manual steps or external web queries needed.
	5.	Upstream Indexing: When a new server is added via upstream_servers add, MCPProxy’s indexing mechanism will kick in (likely re-indexing tools from all upstreams). This means any tools on the newly added server become searchable by the proxy’s normal tool search. The design doesn’t need changes here, but it’s worth noting: after chaining search→add, there’s automatically a refresh of available tools. (The design doc for MCPProxy indicates a “full rebuild [of the index] at startup and whenever an enabled upstream is added”, so this fits perfectly.)
	6.	Testing the Chain: We will test the end-to-end flow in a development environment:
	•	Use a known registry (perhaps set up a test MCP Registry on localhost with a dummy server entry) to run search_servers.
	•	Immediately run the upstream_servers add with the output URL.
	•	Verify that the new upstream appears in upstream_servers list and that its tools are invocable.
	•	Automate this in an integration test if possible (maybe flag it as requiring internet if using a real registry). Mastra’s integration tests provide a guide, as they actually performed live fetches from registries ￼. We can emulate a similar approach for a couple of registries to ensure our parsing and chain flow work correctly.

By carefully designing the output of search_servers and leveraging MCPProxy’s existing upstream add logic, we achieve a smooth user experience: discover a server and plug it in immediately. This encourages exploration of the growing MCP ecosystem with minimal friction, directly from within MCPProxy.

⸻

Note: The addition of search_servers does not disrupt any existing functionality; it’s an additive feature. Users who don’t need it can ignore it, but those who do can now readily expand their toolset. This aligns with MCPProxy’s mission of “smart tool discovery and proxying for MCP servers” ￼ by making the discovery part first-class. With this design in place, MCPProxy becomes not just a proxy for known servers but a gateway to finding new ones.

⸻

Sources:
	•	Mastra MCP Registry Registry – tool behaviors and data structures ￼ ￼ ￼
	•	Model Context Protocol Registry (OpenAPI) – provides standard /v0/servers for listing MCP servers ￼
	•	MCPProxy Design Reference – context on upstream management and indexing ￼

Implementation Prompt (for use with an LLM to generate code):

You are contributing to the `mcpproxy-go` project. Implement the `search_servers` feature as designed above. The goal is to add a new tool/command that allows discovery of MCP servers from a static list of registries, and to output results that can be directly used to add an upstream server.

Follow these steps and file modifications:

1. **Create Data Structures and Embed Registry List:**
   - **File:** `internal/registries/types.go`  
     Define the types for registry entries and server entries. For example:  
     ```go
     package registries
     type RegistryEntry struct {
         ID          string   `json:"id"`
         Name        string   `json:"name"`
         Description string   `json:"description"`
         URL         string   `json:"url"`
         ServersURL  string   `json:"servers_url,omitempty"`
         Tags        []string `json:"tags,omitempty"`
         Protocol    string   `json:"protocol,omitempty"`
         Count       interface{} `json:"count,omitempty"` // number or string
     }
     type ServerEntry struct {
         ID          string `json:"id"`
         Name        string `json:"name"`
         Description string `json:"description"`
         URL         string `json:"url"`         // MCP endpoint for direct connection
         UpdatedAt   string `json:"updatedAt,omitempty"`
         CreatedAt   string `json:"createdAt,omitempty"`
         // (Add other fields if needed, but these are sufficient)
     }
     ```
     These structs map to the JSON structure of registries and servers.

   - **File:** `internal/registries/registry_data.go`  
     Use the Go `embed` package to include a JSON file with registry metadata. For example:  
     ```go
     package registries
     import _ "embed"
     //go:embed registries.json
     var registryDataJSON []byte
     var registryList []RegistryEntry
     func init() {
         // Unmarshal registryDataJSON into registryList ([]RegistryEntry)
         err := json.Unmarshal(registryDataJSON, &registryStruct)
         if err != nil {
             log.Fatalf("Failed to load embedded registries: %v", err)
         }
         registryList = registryStruct.Registries
     }
     ```  
     Assume `registries.json` has a top-level object with `registries: [...]`. You might need a small helper struct, e.g. `registryStruct := struct{ Registries []RegistryEntry }{}` for unmarshaling. After init, `registryList` holds all known registries.  
     *Note:* Include necessary imports (`encoding/json`, `log`, etc.). Ensure this runs at startup so the data is ready.

   - **File:** `internal/registries/registries.json`  
     Create this JSON file containing the registry entries as designed (you can seed it with a subset for now, e.g., mcprun, pulse, smithery, etc., as examples). Ensure the JSON keys match the `RegistryEntry` struct tags. For example:
     ```json
     {
       "registries": [
         {
           "id": "mcprun",
           "name": "MCP Run",
           "description": "...",
           "url": "https://www.mcp.run/",
           "servers_url": "https://www.mcp.run/api/servlets",
           "tags": ["verified"],
           "protocol": "custom/mcprun"
         },
         {
           "id": "smithery",
           "name": "Smithery",
           "description": "...",
           "url": "https://smithery.ai/",
           "servers_url": "https://registry.smithery.ai/servers",
           "tags": ["verified"],
           "protocol": "modelcontextprotocol/registry"
         }
       ]
     }
     ```  
     (Fill in real descriptions and additional entries as needed.)

2. **Implement Fetching Logic:**
   - **File:** `internal/registries/search.go` (new file)  
     Implement the core logic for searching a registry and retrieving servers. Steps:
     ```go
     package registries
     import (
         "encoding/json"
         "fmt"
         "net/http"
         "time"
         "strings"
     )
     // SearchServers searches the given registry for servers matching optional tag and query.
     func SearchServers(registryID string, tag string, query string) ([]ServerEntry, error) {
         // 1. Find registry by ID (or name) in registryList
         var reg *RegistryEntry
         for i, r := range registryList {
             if strings.EqualFold(r.ID, registryID) || strings.EqualFold(r.Name, registryID) {
                 reg = &registryList[i]
                 break
             }
         }
         if reg == nil {
             return nil, fmt.Errorf("registry '%s' not found", registryID)
         }
         if reg.ServersURL == "" {
             return nil, fmt.Errorf("registry '%s' has no servers endpoint", reg.Name)
         }
         // 2. Fetch servers from reg.ServersURL
         client := http.Client{ Timeout: 10 * time.Second }
         resp, err := client.Get(reg.ServersURL)
         if err != nil {
             return nil, fmt.Errorf("failed to fetch servers from %s: %w", reg.ServersURL, err)
         }
         defer resp.Body.Close()
         if resp.StatusCode != 200 {
             return nil, fmt.Errorf("registry query returned %d: %s", resp.StatusCode, resp.Status)
         }
         // 3. Parse response JSON
         var rawData interface{}
         if err := json.NewDecoder(resp.Body).Decode(&rawData); err != nil {
             return nil, fmt.Errorf("invalid JSON from registry: %w", err)
         }
         // 4. Process based on protocol or content
         var servers []ServerEntry
         switch reg.Protocol {
         case "modelcontextprotocol/registry":
             // Expect an object with "servers" field (list of server entries)
             m, ok := rawData.(map[string]interface{})
             if ok && m["servers"] != nil {
                 // Marshal "servers" subfield to JSON and then unmarshal to []ServerEntry
                 data, _ := json.Marshal(m["servers"])
                 json.Unmarshal(data, &servers)
             } else if ok && m["data"] != nil {
                 // (If the API returns paginated data in "data" key, handle similarly)
                 data, _ := json.Marshal(m["data"])
                 json.Unmarshal(data, &servers)
             } else if arr, ok := rawData.([]interface{}); ok {
                 // If the response is directly an array
                 data, _ := json.Marshal(arr)
                 json.Unmarshal(data, &servers)
             }
             // For each server, ensure required fields and possibly set URL if provided by API (if the registry API included a field for endpoint)
             // (If needed, a secondary fetch for details could be implemented here.)
         case "custom/mcprun":
             // Example: the MCP Run API returns a certain JSON structure. Implement a custom parse.
             // e.g., perhaps resp JSON has {"servlets": [ {...}, {...} ]}. We would extract those into servers.
             // For demonstration, assume structure similar to OpenAPI:
             m, ok := rawData.(map[string]interface{})
             if ok && m["servlets"] != nil {
                 data, _ := json.Marshal(m["servlets"])
                 json.Unmarshal(data, &servers)
             }
             // Then, if needed, construct the server URLs. (If MCP Run's API returns enough info, fill servers accordingly.)
         case "custom/pulse":
             // Parse Pulse MCP format (assuming similar to modelcontextprotocol but maybe under different fields or requiring transform).
             m, ok := rawData.(map[string]interface{})
             if ok && m["servers"] != nil {
                 data, _ := json.Marshal(m["servers"])
                 json.Unmarshal(data, &servers)
             }
             // Possibly Pulse returns additional metadata, but for simplicity we'll treat similarly.
         default:
             // Default handling: try to unmarshal rawData directly into []ServerEntry
             data, _ := json.Marshal(rawData)
             json.Unmarshal(data, &servers)
         }
         // 5. Filter by search term and tag
         filtered := make([]ServerEntry, 0, len(servers))
         for _, srv := range servers {
             if query != "" {
                 q := strings.ToLower(query)
                 if !strings.Contains(strings.ToLower(srv.Name), q) && !strings.Contains(strings.ToLower(srv.Description), q) {
                     continue
                 }
             }
             if tag != "" {
                 // If we had tags per server, check here. (No tags in ServerEntry by default, so this might always pass.)
                 // This part can be expanded if server entries have a 'tags' field in some protocols.
             }
             // If the server entry lacks a URL (endpoint), try to infer or append if possible:
             if srv.URL == "" {
                 // For known protocols, we might derive the URL.
                 // For example, if the registry provides a base URL or the server ID is a URL slug.
                 // We could also add an optional field in RegistryEntry like BaseURLPrefix to combine with server ID.
                 // For now, leave URL empty if unknown.
             }
             filtered = append(filtered, srv)
         }
         return filtered, nil
     }
     ```
     **Important:** The above code should be refined based on actual data formats. Ensure that `ServerEntry.URL` is populated when possible:
       - If the registry returns a direct URL field for each server, map it.
       - If not, consider adding logic to fetch details: e.g., if `reg.Protocol == "modelcontextprotocol/registry"`, you might loop through each `ServerEntry` (with ID) and call `GET /v0/servers/{id}` on the registry. Parse the `base_url` from that and set `ServerEntry.URL`. This additional step can be added if needed to fully realize the “direct add” goal.
     Also, handle `CreatedAt`/`UpdatedAt` if present in JSON (they will parse into the struct automatically if names match and types are string).

   - **File:** `internal/registries/search_test.go`  
     Write tests for `SearchServers`:
     - Test that an unknown registry returns an error.
     - Test that a known registry with no servers_url returns an error.
     - **Optional:** simulate a registry response. For example, create a dummy HTTP server (using `httptest.NewServer`) that returns a known JSON, then temporarily override a RegistryEntry’s `ServersURL` to point to this dummy server’s URL. Then call `SearchServers` and verify the returned `ServerEntry` list matches expected data (filtered correctly).  
     - Test the filtering logic: e.g., create a small list of `ServerEntry` in code and run the filter portion to ensure search term matching works (similar to Mastra’s test where they verify the search term is contained in results [oai_citation:36‡file-kdtztsq9jqu7kdcvvehwbr](file://file-KDtZtsQ9jqu7KDCvvEHWBr#:~:text=%2F%2F%20Search%20for%20that%20word,search%3A%20searchWord)).

3. **Integrate with CLI:**
   - **File:** `cmd/mcpproxy/main.go` (or a new command file if the project structure uses separate files for commands)  
     Register the new CLI command. If using Cobra or similar, add something like:
     ```go
     var registryFlag, searchFlag, tagFlag string
     cmd := &cobra.Command{
         Use:   "search-servers",
         Short: "Search MCP registries for available servers",
         RunE: func(cmd *cobra.Command, args []string) error {
             if registryFlag == "" {
                 return fmt.Errorf("--registry is required (use `mcpproxy search-servers --list-registries` to see options)")
             }
             results, err := registries.SearchServers(registryFlag, tagFlag, searchFlag)
             if err != nil {
                 return err
             }
             // Print results as JSON
             output, _ := json.MarshalIndent(results, "", "  ")
             fmt.Println(string(output))
             return nil
         },
     }
     cmd.Flags().StringVarP(&registryFlag, "registry", "r", "", "Registry ID or name to search (exact match)")
     cmd.Flags().StringVarP(&searchFlag,   "search", "s", "", "Search term for server name/description")
     cmd.Flags().StringVarP(&tagFlag,      "tag",    "t", "", "Filter servers by tag/category")
     // Perhaps a --list-registries flag:
     cmd.Flags().Bool("list-registries", false, "List all known registries")
     // (If --list-registries is set, ignore other flags and just print the registry list from internal/registries)
     rootCmd.AddCommand(cmd)
     ```
     Adjust to the actual CLI framework in use. For example, if not using Cobra, handle flags parsing accordingly.
     - Implement the `--list-registries` functionality: if that flag is true, output the `registryList` (from `registry_data.go`) in a readable format (could be JSON or a table with ID and Name). This helps users discover registry IDs.
     - Ensure the command is hooked into the main application (added to the root command or executed in main).

   - **File:** `internal/upstream/manager.go` (if needed, for any adjustments in adding logic)  
     Likely no changes here. But confirm how `upstream_servers add` works. If it expects a raw URL, we are fine. If it requires some identification, ensure providing the URL suffices. Possibly test adding a known MCP server URL via CLI to verify the flow.

4. **Register as MCP Tool:**
   - **File:** `internal/proxy/tools.go` or wherever tools are registered for MCP (the proxy might have a method like `proxy.RegisterTool(...)`).  
     Create a Tool definition for `search_servers`:
     ```go
     proxy.RegisterTool(&mcp.Tool{
         Name: "search_servers",
         Description: "Discover MCP servers from known registries. Use registry ID to filter.",
         InputSchema: mcp.JSONSchema{ /* define fields "registry", "search", "tag" as strings */ },
         OutputSchema: mcp.JSONSchema{ /* could be type array of ServerEntry schema */ },
         Handler: func(params map[string]interface{}) (interface{}, error) {
             reg := params["registry"].(string)
             query := ""
             tag := ""
             if v, ok := params["search"]; ok { query = v.(string) }
             if v, ok := params["tag"]; ok { tag = v.(string) }
             return registries.SearchServers(reg, tag, query)
         },
     })
     ```
     The exact types and schema integration will depend on the `mcp-go` library. Essentially, when this tool is invoked, we parse inputs, call `SearchServers`, and return the slice of `ServerEntry` as the result. The MCP framework will handle serializing it to JSON. (Make sure the `ServerEntry` fields are all serializable; since they are basic strings, that’s fine.)

5. **Update Documentation (if any):**
   - **File:** `docs/setup.md` or `README.md`  
     Add usage info for the new feature. For instance, under a “Managing Upstreams” section, describe how to use `mcpproxy search-servers` to find servers and then `mcpproxy upstream_servers add` to add them.

6. **Test Integration Manually:**
   - Run `mcpproxy search-servers --list-registries` to see that embedded registries load correctly.
   - Pick a registry (like one with a known reachable endpoint) and run `mcpproxy search-servers -r <id>` with and without search terms. Confirm that results (or errors) make sense.
   - Try the full chain: `search-servers` to find something, then `upstream_servers add` to add it, then verify the new server’s tools appear via `mcpproxy` (for example, in the UI or via `retrieve_tools` if such a command exists).
   - If possible, test with the official MCP Registry service by running it locally or using a known instance, to ensure our `modelcontextprotocol/registry` handler works (it should list some servers).

**Implementation Hints:**
- Pay attention to error handling and messaging – it should guide the user if they misuse the command (like forgetting `--registry`).
- Keep network operations efficient: consider reusing HTTP connections (the default Client is fine for now, since we do one request per search).
- Make sure to not introduce global state beyond the loaded registry list; `SearchServers` should be thread-safe (our usage of a package-level slice is okay if read-only after init).
- Use logs for debugging if needed, but avoid spamming output on normal execution (unless `-v` verbose is enabled).
- Aim for clean, idiomatic Go code with proper naming (e.g., use `registryID` not just `id` for clarity in contexts, etc.). Add comments for any non-obvious logic like custom parsing.

Once implemented, this feature will allow dynamic expansion of MCPProxy’s capabilities by tapping into the ecosystem of MCP servers. Ensure all unit tests and existing tests pass, and add new tests for this feature as described.