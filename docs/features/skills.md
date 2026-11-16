---
id: skills
title: Skills over MCP
sidebar_label: Skills over MCP
sidebar_position: 14
description: Read the agent skills that upstream MCP servers serve through the skills extension, verify every file, and inspect them with the mcpproxy skills commands. Off by default and visible to administrators only in this release.
keywords: [skills, agent skills, SKILL.md, skill://, enable_skills, expose_skills, digest, sha256, cli, rest]
---

# Skills over MCP

Some MCP servers serve **agent skills**: folders with a `SKILL.md` file and supporting files, listed and read through the MCP skills extension (`io.modelcontextprotocol/skills`). MCPProxy reads those skills from your upstream servers, checks every file, and keeps a record of each one that you can list and read.

:::caution In this release skills are visible to administrators only
Skills are **not offered to agents yet**. Only the API key, the Unix socket and an administrator session can list or read them. A scoped agent token is refused on every skills endpoint, and agents see nothing about skills. Review, approval and the agent-facing tools arrive in later releases.
:::

## Turn it on

Skills over MCP is **off by default**. Turn it on in `~/.mcpproxy/mcp_config.json`:

```json
{
  "enable_skills": true
}
```

To keep one server out, set `expose_skills` to `false` on it:

```json
{
  "enable_skills": true,
  "mcpServers": [
    { "name": "hf", "url": "https://huggingface.co/mcp", "protocol": "http", "enabled": true },
    { "name": "internal", "url": "https://mcp.internal.example/mcp", "protocol": "http", "enabled": true, "expose_skills": false }
  ]
}
```

Both fields hot-reload. See [Configuration](../configuration.md) for the full field list. A server is read only while it is enabled and not quarantined, and only if it announces the skills extension.

## What MCPProxy reads and verifies

For each skill a server lists, MCPProxy:

- reads the listing (skill name, `skill://` address, the list of files);
- reads every file and compares its **size and SHA-256 digest** with what the server listed;
- compares the `SKILL.md` frontmatter (`name`, `description`) with the listing;
- stores the text files and computes one content hash (`sk1-...`) over the whole skill.

A skill that fails any check is not dropped. It is kept with the status `unusable` and a reason, so you can see what the server sent.

### Limits

These limits are fixed and not configurable:

| Limit | Value |
|-------|-------|
| Files per skill | 512 |
| Size of one skill | 16 MiB |
| Skills per server | 200 |
| Size of one listing (all pages) | 16 MiB |
| Stored text per server | 64 MiB |
| Name and description kept for display | 256 bytes and 4 KiB (longer text is cut and marked with …) |

The `mcpproxy skills` commands show control characters in skill names, descriptions, paths and file text as visible escapes (`\x1b`, `\u202e`), because this text comes from the upstream server and is shown before anyone approved it. Use `-o json` or `-o yaml` for the exact text.

A listing that hits a limit is marked `truncated`. The limit of 200 counts every listed entry, including ones MCPProxy cannot decode.

A size in a listing is only what the server claims. For servers reached over streamable HTTP, MCPProxy also caps each response before it is buffered, at what the listed size allows, so a server that sends more than it listed is cut off. For stdio and SSE servers a response arrives on a stream shared with everything else the server sends and cannot be cut off per request: the length is checked after the response has arrived, which is the same exposure that already exists for any tool result from that server.

## Skill states in this release

| Status | Meaning |
|--------|---------|
| `pending` | Read and verified. Nobody has approved it, so it is not available to agents. |
| `unusable` | A check failed. The `unusable_reason` says which. |
| `approved`, `changed` | Reserved for the review step that follows. They appear only after approval exists. |

Every usable skill is `pending` in this release, and `available` is `false` for all of them.

### Reasons a skill is unusable

| Reason | Meaning |
|--------|---------|
| `invalid_uri` | The skill or file address is not a valid `skill://` address. |
| `name_mismatch` | The `name` in the `SKILL.md` frontmatter differs from the last segment of the skill's path. |
| `dynamic` | The server declares the resources as dynamic, so their content cannot be verified. |
| `too_many_files` | More than 512 files. |
| `too_large` | The skill is larger than 16 MiB. |
| `server_bound_exceeded` | The refresh read budget or the server's stored text budget (64 MiB) ran out before this skill was read. |
| `unsupported_digest` | A file digest is not `sha256`. |
| `digest_mismatch` | The bytes read do not match the listed SHA-256. |
| `size_mismatch` | The bytes read do not match the listed size. |
| `frontmatter_mismatch` | The `SKILL.md` frontmatter disagrees with the listing. |
| `frontmatter_invalid` | The `SKILL.md` frontmatter cannot be parsed. |
| `read_failed` | The server failed or timed out while a file was read. |
| `stored_copy_damaged` | The stored copy no longer matches its digest. |

## Command line

All three commands need a running core (`mcpproxy serve`) and administrator credentials. They support `-o json` and `-o yaml`.

### `mcpproxy skills list`

```bash
mcpproxy skills list
```

```
SERVER  SKILL     STATUS                       FILES  SIZE
hf      hf-cli    pending                      3      33.4 KB
hf      hf-jobs   pending                      1      4.2 KB
hf      broken    unusable (digest_mismatch)   2      1.1 KB
hf: listing truncated (a skills limit was reached; some skills are missing)
```

`--server <name>` lists one server. A server whose last refresh failed gets a line with the error. With `enable_skills` off the command prints one line saying so.

### `mcpproxy skills show <server>:<skill>`

```bash
mcpproxy skills show hf:hf-cli
mcpproxy skills show hf:hf-cli --file references/auth.md
```

```
Skill:        hf:hf-cli
Name:         hf-cli
Description:  Hugging Face CLI
Status:       pending
Hash:         sk1-0d9c...
Available:    false

FILE        SIZE     TYPE    DIGEST
SKILL.md    32.1 KB  text    sha256:3b1f...
logo.png    1.3 KB   binary  sha256:9a02...

--- SKILL.md (current copy) ---
# hf-cli
...
```

`--file` picks another text file. `--approved` reads the approved copy; there is none until review exists. The command reads long files in chunks until the end. A binary file cannot be shown.

### `mcpproxy skills refresh <server>`

```bash
mcpproxy skills refresh hf
```

```
hf: refreshed, 3 skills (2 pending, 1 unusable)
```

A refresh also runs by itself when a server connects, on the periodic discovery sweep, and when the configuration changes.

## REST API

All three require administrator credentials; a scoped agent token gets `403`. The same shapes are in [REST API: Skills](../api/rest-api.md#skills).

| Endpoint | Purpose |
|----------|---------|
| `GET /api/v1/servers/{id}/skills` | Every skill record of the server |
| `GET /api/v1/servers/{id}/skills/content?skill=<path>&file=<relpath>&copy=current\|approved&offset=<n>&limit=<n>` | One text file, in chunks of at most 64 KiB |
| `POST /api/v1/servers/{id}/skills/refresh` | Read the server's skills again and return the listing |

## Troubleshooting

**The list is empty and `supported` is `false`.** The server does not announce the skills extension. `mcpproxy skills refresh <server>` answers `skills_not_supported` in that case. Check that the server is enabled and not quarantined, and that it is a server that serves skills.

**The command prints that skills are off.** Set `enable_skills: true` in the configuration. REST answers `{"enabled": false, "skills": []}`, and refresh answers `409 skills_disabled`.

**`truncated` is `true`.** The server listed more skills, or a larger listing, than the limits above allow, and MCPProxy stopped reading. The skills it did read are kept.

**A skill shows `server_bound_exceeded`.** The refresh read budget or the server's stored text (64 MiB) ran out before this skill was read. The skills read before the limit are kept; later ones are marked unusable. Removing large skills on the server, or excluding it with `expose_skills: false`, frees the budget.

**`409 stored_copy_damaged` when reading a file.** The stored bytes no longer match their digest. Run `mcpproxy skills refresh <server>` to read the skill again.
