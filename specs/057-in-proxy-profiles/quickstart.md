# Quickstart: In-Proxy Profiles

Run one MCPProxy, give each client its own scoped URL.

## 1. Configure two profiles

`~/.mcpproxy/mcp_config.json`:

```json
{
  "listen": "127.0.0.1:8080",
  "mcpServers": [
    { "name": "github", "url": "https://api.github.com/mcp", "protocol": "http" },
    { "name": "k8s",    "command": "kubectl-mcp",            "protocol": "stdio" },
    { "name": "fs",     "command": "fs-mcp",                 "protocol": "stdio" },
    { "name": "web",    "command": "web-search-mcp",         "protocol": "stdio" }
  ],
  "profiles": [
    { "name": "research", "servers": ["fs", "web"] },
    { "name": "deploy",   "servers": ["github", "k8s"] }
  ]
}
```

No restart needed — `profiles` is hot-reloaded with the rest of the config.

## 2. Point each client at its profile URL

```bash
K="$(jq -r .api_key ~/.mcpproxy/mcp_config.json)"

# Research client — sees only fs + web tools
curl -H "X-API-Key: $K" http://127.0.0.1:8080/mcp/p/research

# Deploy client — sees only github + k8s tools
curl -H "X-API-Key: $K" http://127.0.0.1:8080/mcp/p/deploy

# Full union (unchanged behaviour)
curl -H "X-API-Key: $K" http://127.0.0.1:8080/mcp
```

`retrieve_tools` at `/mcp/p/research` returns only `fs`/`web` tools; a `call_tool_read` into `github` from that URL is rejected with `"server 'github' is not in profile 'research'"`.

## 3. Compose with an agent token (intersection)

```bash
# Token scoped to {github, fs, web}; profile 'deploy' scoped to {github, k8s}.
# Effective scope at /mcp/p/deploy = intersection = {github}.
mcpproxy token create --name ci-bot --servers github,fs,web --permissions read,write --expires 30d

curl -H "Authorization: Bearer mcp_agt_..." http://127.0.0.1:8080/mcp/p/deploy
# retrieve_tools → only github tools.
# call into fs → "not in profile 'deploy'"; call into k8s → "not in scope for this agent token".
```

## 4. Per-server tool denylist still applies inside a profile

```json
{ "name": "github", "url": "...", "protocol": "http", "disabled_tools": ["delete_repo", "force_push"] }
```

A client at `/mcp/p/deploy` sees every `github` tool except `delete_repo`/`force_push` — enforced by the existing per-server denylist.

## 5. Error & not-found paths

```bash
# No profiles configured at all:
curl -i http://127.0.0.1:8080/mcp/p/anything   # 404 {"error":"no profiles configured"}

# Unknown slug when profiles exist:
curl -i -H "X-API-Key: $K" http://127.0.0.1:8080/mcp/p/nope
# 404 {"error":"unknown profile 'nope'","available":["research","deploy"]}
```

## Verify (acceptance)

- Two clients on two profile URLs each see only their profile's tools (SC-001).
- `/mcp`, `/mcp/code`, `/mcp/call` behave exactly as before (SC-002).
- Token × profile filtering is the intersection, with errors naming the blocking primitive (SC-003).
- A config without `profiles` round-trips byte-identically (SC-004).
- Reserved/invalid/duplicate slugs are rejected at load with a precise diagnostic (SC-005).
- Activity records from a profile URL carry `metadata["profile"]` (FR-011).
