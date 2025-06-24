# mcpproxy-go — Design Document (v0.1.0)

## 1  Goals

Re‑implement the existing Python MCP proxy in Go, delivering a single‑binary desktop proxy with concurrent performance, local BM25 search, and a minimal cross‑platform system‑tray UI.

### MVP scope (v0.1)

* **Transport** – downstream Streamable‑HTTP only.
* **Built‑in MCP tools**

  * `retrieve_tools` – BM25 keyword search over all upstream tools.
  * `call_tool` – invoke any upstream tool.
  * `upstream_mcp_servers` – CRUD for upstream registry (`enabled` flag).
  * `tools_stat` – aggregate statistics (total tools, call counts, top‑N).
* **Index lifecycle** – full rebuild at start‑up and whenever an *enabled* upstream is added, removed or updated.
* **Tray UI** – first‑class feature; icon and systray menu only (no full GUI); toggle proxy, manage upstreams, show counters.
* **Local persistence** – defaults to `~/.mcpproxy/`:

  * `config.db` – bbolt key/value store (servers, stats, hashes).
  * `index.bleve/` – Bleve Scorch index (BM25).

## 2  Non‑Goals (v0.1)

* WebSocket/stdin transports, distributed indexing, vector search.

## 3  Tech Stack

| Concern           | Library                           | Reason                             |
| ----------------- | --------------------------------- | ---------------------------------- |
| MCP server/client | **`mark3labs/mcp-go`**            | Native Go, Streamable‑HTTP support |
| Full‑text search  | **Bleve v2**                      | Embeddable BM25                    |
| CLI & config      | **`spf13/cobra` + `spf13/viper`** | Flags → env → file binding         |
| Persistence       | **bbolt**                         | Single‑file ACID                   |
| Sys‑tray          | **`fyne.io/systray`**             | Tiny cross‑platform tray           |
| Logging           | **zap** / **slog**                | Structured logs                    |
| Metrics           | `prometheus/client_golang`        | Optional `/metrics`                |

## 4  Architecture Overview

```
┌────────────┐ Streamable‑HTTP ┌──────────────────────────────┐ Streamable‑HTTP ┌──────────────┐
│  Clients   │ ⇆ :8080 ⇆       │        mcpproxy‑go           │ ⇆  Upstream N ⇆ │  MCP Server  │
└────────────┘                 │                              │                 └──────────────┘
                               │  ┌───────────────┐           │
                               │  │ Tray Daemon   │───────────┘
                               │  └───────────────┘
                               │   ↙ Bleve index
                               └──→ bbolt (cfg, stats, hashes)
```

* **Downstream server** – `server.ServeStreamableHTTP`.
* **Upstream map** – persistent `client.Client` per *enabled* server.

  * On *add/enable* → call `ListTools`, compute hashes, rebuild index.
* **Indexer** – rebuilds index; swaps pointer atomically on finish.
* **Stats** – middleware increments per‑tool counters inside Bolt.

## 5  Naming & Hashing Scheme

* **`tool_name`** – canonical identifier used throughout the proxy.

  * Format: `<serverName>:<originalToolName>` (e.g. `prod:compress`).
* **`tool_hash`** – `sha256(serverName + toolName + parametersSchemaJSON)`.

  * Stored alongside tool metadata in both Bleve doc and Bolt `toolhash` bucket.
  * During re‑sync `ListTools` results are hashed; unchanged hashes skip re‑indexing.

## 6  Data Model (bbolt)

| Bucket      | Key           | Value                        |
| ----------- | ------------- | ---------------------------- |
| `upstreams` | `<uuid>`      | `{name,url,enabled,created}` |
| `toolstats` | `<tool_name>` | uint64 call‑count            |
| `toolhash`  | `<tool_name>` | 32‑byte sha256               |
| `meta`      | `schema`      | uint version                 |

## 7  Indexer Flow

```mermaid
graph LR
  A[App start / upstream change] --> B{For each enabled server}
  B --> C(ListTools)
  C --> D{{Compute sha256}}
  D -->|hash changed| E(Add/Update Bleve doc)
  D -->|same hash| F[Skip]
  E --> G[Commit]
  F --> G
```

* Bleve doc fields: `tool_name`, `server`, `description`, `tags`, `hash`.
* Query path: `retrieve_tools` → `bleve.Search` (MatchQuery) top‑20.

## 8  MCP Tool Specifications

### 8.1  `retrieve_tools`

```jsonc
Input:  {"query":"rate limit"}
Output: {"tools":[{"tool_name":"prod:compress","score":0.81}]}
```

### 8.2  `call_tool`

```jsonc
Input: {
  "name": "prod:compress",  // tool_name
  "args": {"text": "abc"}  // transparently forwarded
}
Output: <raw upstream JSON>
```

The proxy parses `name` to pick the correct upstream client and forwards `args` unchanged.

### 8.3  `upstream_mcp_servers`

```jsonc
Input:  {"operation":"add","url":"https://api.mcp.dev","name":"dev"}
Output: {"id":"uuid","enabled":true}
```

Operations: `list` / `add` / `remove` / `update`.

### 8.4  `tools_stat`

Returns `{total_tools, top:[{tool_name,count}]}`.

## 9  Concurrency & Error Handling

* Bolt writes serialised via a `storage.Manager`.
* Upstream map behind `sync.RWMutex`.
* Index rebuild runs in goroutine; stale index served until swap.
* Panic‑safe wrappers restart crashed upstream clients.

## 10  CLI, Config & Tray

* `mcpproxy [--listen :8080] [--data-dir ~/.mcpproxy] [--upstream "prod=https://api"]`
* Viper reads `$MCPP_` envs and `config.toml`.
* Tray (systray): icon + menu items (Enable, Disable, Add…, Reindex, Quit).

## 11  Build & Packaging

* Cross‑compile via `GOOS=darwin/windows/linux`.
* macOS: bundle into `.app`, sign & notarise; DMG + Homebrew Cask.
* Windows: `-H windowsgui` exe → MSIX/MSI; code‑sign EV cert.
* Icon embedded using `go:embed` + `.syso` for Windows.

## 12  Future Roadmap

* Incremental index updates on `tool_hash` diff.
* Hybrid BM25 + vector search.
* Auto‑update channel.
* GUI front‑end built with Wails.

## Upstream Server Management

### Dynamic Server Configuration

The proxy supports dynamic management of upstream MCP servers through the `upstream_servers` MCP tool. This enables:

- **Runtime Configuration**: Add, remove, and modify servers without restart
- **Batch Operations**: Import multiple servers simultaneously
- **Hot Reloading**: Immediate connection attempts and tool index updates
- **Persistent Storage**: Configuration automatically saved to disk

### Configuration Persistence

#### Storage Architecture

```
~/.mcpproxy/
├── mcp_config.json          # Main configuration file
├── data.bolt                # BoltDB storage (tool stats, metadata)
└── index.bleve/             # Search index directory
```

#### Configuration Flow

1. **Runtime Changes**: MCP tool calls modify server configurations
2. **Storage Update**: Changes written to BoltDB for immediate persistence
3. **Config Sync**: Configuration file updated with current state
4. **Connection Management**: Upstream manager connects/disconnects servers
5. **Index Update**: Tool discovery runs to update search index

#### Configuration Sync Architecture

```mermaid
graph TD
    A["Config File<br/>(Single Source of Truth)"] --> B["File Watcher<br/>(Runtime Sync)"]
    A --> C["loadConfiguredServers()<br/>(Startup & Manual Sync)"]
    
    B --> D["ReloadConfiguration()"]
    D --> C
    
    C --> E["Compare Config vs Storage"]
    E --> F["Sync to Storage/Database"]
    E --> G["Sync to UpstreamManager"]
    E --> H["Remove Orphaned Servers"]
    
    H --> I["Delete from Storage"]
    H --> J["Remove from UpstreamManager"]
    H --> K["Delete Tools from Index"]
    
    L["LLM Server Changes"] --> M["Save to Storage"]
    M --> N["SaveConfiguration()"]
    N --> O["Write to Config File"]
    O --> B
    
    P["Quarantined Servers"] --> Q["Remove from UpstreamManager<br/>(Not Active)"]
    Q --> R["Keep in Storage<br/>(For UI Display)"]
    
    style A fill:#ffeeee
    style E fill:#ffffcc
    style H fill:#ffcccc
    style K fill:#ccffcc
```

The configuration sync system ensures **config file remains the single source of truth** by:

- **Bidirectional sync**: Changes in config file automatically sync to database and vice versa
- **Orphan cleanup**: Removes servers from database/index when removed from config file
- **Runtime changes**: LLM-added servers are immediately saved to config file
- **Startup reconciliation**: At startup, config file state overrides database state
- **File watching**: Runtime config file changes trigger automatic resync

### Server Types

#### HTTP Servers
- **Transport**: HTTP/HTTPS requests
- **Authentication**: Headers-based (API keys, tokens)
- **Configuration**: URL + optional headers
- **Example**: REST-based MCP servers

#### Stdio Servers  
- **Transport**: Standard input/output communication
- **Process Management**: Command execution with arguments
- **Environment**: Custom environment variables
- **Example**: Python/Node.js MCP server scripts

### Import Mechanisms

#### Cursor IDE Compatibility
- **Format**: Direct import of Cursor `mcp.json` configuration
- **Conversion**: Automatic mapping to internal server configuration
- **Validation**: Type detection and parameter validation

#### Batch Import
- **Multiple Servers**: Array of server configurations
- **Mixed Types**: HTTP and stdio servers in single operation
- **Error Handling**: Individual server failures don't block others

### Real-time Updates

#### Connection Management
- **Background Connections**: Non-blocking server connection attempts
- **Retry Logic**: Exponential backoff for failed connections
- **Status Tracking**: Real-time connection status updates

#### Tool Discovery
- **Automatic Indexing**: New servers trigger tool discovery
- **Search Updates**: BM25 index updated with new tools
- **Statistics**: Tool usage tracking across servers

#### UI Integration
- **Tray Updates**: System tray reflects server changes
- **Status Broadcasting**: Real-time status updates to UI components
- **Configuration Sync**: UI displays current server state
