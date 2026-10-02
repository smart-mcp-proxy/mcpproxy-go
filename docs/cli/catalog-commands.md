---
id: catalog-commands
title: Catalog Commands
sidebar_label: Catalog Commands
sidebar_position: 8
description: "mcpproxy catalog search, show and add: find a server across every catalog source, ranked the same way as the Web UI and macOS Add Server, and add it quarantined"
keywords: [catalog, registry, search, add, quarantine, secret, cli]
---

# Catalog Commands

`mcpproxy catalog` is the CLI side of **Add Server → Catalog** in the Web UI and macOS app. It searches every enabled catalog source at once, ranks the results the same way, and adds the server you pick **quarantined** so you can review it before agents see it.

```
mcpproxy catalog
├── search [query]        Search every enabled source (no query: browse Official and Popular)
├── show <source>/<id>    Details of one entry
└── add <source>/<id>     Add the entry as a quarantined upstream server
```

All subcommands accept the global `-o table|json|yaml` flag. With `-o json` the output is the `data` object of `GET /api/v1/catalog/search`.

## catalog search

```bash
mcpproxy catalog search github
mcpproxy catalog search github --source official
mcpproxy catalog search                    # browse the Official and Popular sections
```

| Flag | Description |
|---|---|
| `--source <id>` | Narrow to one catalog source (`mcpproxy registry list` shows the ids). The filter applies before ranking and the limit |
| `--limit`, `-l <n>` | Maximum results (default 20, maximum 50) |
| `--tag`, `-t` | Not supported: catalog entries carry no tags, so a non-empty value is rejected |

Results from every source are merged and ranked: the official source first, then verified publishers, then popularity (stars or installs when the source provides them; a missing value counts as zero), then text relevance, then title. A source that times out is reported as unavailable and the other sources' results still print. The order is identical on REST, the Web UI, macOS and the MCP `search_servers` tool.

```
SOURCE     ID                                 TITLE                    TRANSPORT  ADDED
official   io.github.github/github-mcp-server  GitHub                   http
official   io.github.example/github-notes     GitHub Notes             stdio
community  acme/github-fork                   GitHub (community fork)  http
Found 3 results. Add one with: mcpproxy catalog add <source>/<id>
```

With no query the output has two blocks, `Official:` and `Popular:`. A source that did not answer prints `⚠ <source> unavailable: <reason>` after the table. `-o json` adds `official`, `verified`, `publisher` and `popularity` to every result.

## catalog show

```bash
mcpproxy catalog show official/io.github.github/github-mcp-server
```

The reference is `<source>/<id>`. It splits on the **first** slash only, because an official-registry id is itself reverse-DNS shaped and contains further slashes. The output names the title, the reference, publisher, official and verified flags, transport, install URL or command, description, each required input (marked `(secret)` when its name looks like a secret) and whether the server is already added.

## catalog add

```bash
mcpproxy catalog add official/io.github.github/github-mcp-server
mcpproxy catalog add official/io.github.github/github-mcp-server --name gh --env GITHUB_TOKEN=...
```

| Flag | Description |
|---|---|
| `--name <name>` | Override the server name |
| `--env KEY=VALUE` | Set an environment variable (repeatable) |
| `--enabled` | Whether the added server is enabled (default true) |

The command prints `Added <name> to MCPProxy (quarantined for review)`, the same wording as the Web UI's "Add to MCPProxy → Added" button. It needs a running daemon, because the add is the same server-side operation as `registry add`; the daemon re-derives the runnable config from the catalog entry and never accepts a config blob from the client. Review the server before use:

```bash
mcpproxy review show <name>
mcpproxy review approve <name>
```

### Keeping secrets out of the config

`catalog add --env` stores the value in the config file. To keep a value in the OS keyring instead, add the server by hand with `upstream add`:

```bash
mcpproxy upstream add github https://api.githubcopilot.com/mcp/ --secret-header "Authorization: Bearer ghp_..."
mcpproxy upstream add weather -- npx -y weather-mcp --secret-env WEATHER_API_KEY=abc123
```

`--secret-env` and `--secret-header` write the value to the keyring and store `${keyring:<server>-env-<name>}` in the config. The Web UI and macOS Add Server forms offer the same choice as a **Value · Secret** toggle that defaults to Secret for names like `*_TOKEN`, `*_KEY`, `*SECRET*` and `*PASSWORD*`. See [Keyring Integration](/features/keyring-integration).

## Catalog sources and the older commands

`mcpproxy registry list`, `add-source`, `edit` and `remove` still manage the catalog **sources**. `registry search` and `registry add` are deprecated aliases of `catalog search` and `catalog add`: they keep working and print a one-line notice. See [Registry Add](/features/registry-add) for the REST and MCP routes and the security model.
