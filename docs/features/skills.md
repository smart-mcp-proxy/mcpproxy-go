---
id: skills
title: Skills over MCP
sidebar_label: Skills over MCP
sidebar_position: 14
description: Read the agent skills that upstream MCP servers serve through the skills extension, verify and scan every file, and review, approve or block each skill. Off by default and visible to administrators only in this release.
keywords: [skills, agent skills, SKILL.md, skill://, enable_skills, expose_skills, digest, sha256, approve, block, review, trust mode, cli, rest]
---

# Skills over MCP

Some MCP servers serve **agent skills**: folders with a `SKILL.md` file and supporting files, listed and read through the MCP skills extension (`io.modelcontextprotocol/skills`). MCPProxy reads those skills from your upstream servers, checks every file, and keeps a record of each one that you can list and read.

:::caution In this release skills are visible to administrators only
Skills are **not offered to agents yet**. Only the API key, the Unix socket and an administrator session can list, read, approve or block them. A scoped agent token is refused on every skills endpoint, and agents see nothing about skills. You can already review and approve skills, so they are ready when the agent-facing tools arrive in a later release.
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

Both fields hot-reload. Both are also listed under [Configuration File: Skills](/configuration/config-file#skills). A server is read only while it is enabled and not quarantined, and only if it announces the skills extension.

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

The `mcpproxy skills` commands show control characters in skill names, descriptions, paths and file text as visible escapes (`\x1b`, `\u202e`), because this text comes from the upstream server and is shown before anyone approved it. Use `-o json` for the exact text: there the same characters are written as JSON escapes (`\u202e`), which a JSON parser decodes back. `-o yaml` shows them as visible escapes, like the table output.

A listing that hits a limit is marked `truncated`. The limit of 200 counts every listed entry, including ones MCPProxy cannot decode.

A size in a listing is only what the server claims. For servers reached over streamable HTTP, MCPProxy also caps each response before it is buffered, at what the listed size allows, so a server that sends more than it listed is cut off. For stdio and SSE servers a response arrives on a stream shared with everything else the server sends and cannot be cut off per request: the length is checked after the response has arrived, which is the same exposure that already exists for any tool result from that server.

## Skill states

| Status | Meaning |
|--------|---------|
| `pending` | Read and verified, never approved. Held for review. |
| `approved` | Approved for exactly its current content. |
| `changed` | It was approved, and its content has changed since. Held for review again. |
| `unusable` | A check failed. The `unusable_reason` says which. It cannot be approved. |

A skill also has a **blocked** mark that only a person sets and clears. `available` is `true` only for a skill that is `approved`, not blocked, still listed by its server, on a server that is enabled, not quarantined and not excluded with `expose_skills: false`.

## Reviewing skills {#reviewing-skills}

A skill is instruction text that an agent will follow, so MCPProxy treats it like a tool description: nothing is served until it is approved, and an approval covers one exact content.

### What an approval covers

An approval is bound to the skill's content hash (`sk1-...`). The hash covers the skill's address, every file (address, path, digest, size) and the `SKILL.md` frontmatter. If the server changes one byte of one file, adds or removes a file, or edits the frontmatter, the hash changes and the skill is no longer approved.

MCPProxy keeps its own copy of what you approved. When a skill changes, the approved copy stays in the store next to the new one, so you can compare them. If the server later returns to the approved content, the skill is approved again without a new review.

An approval also belongs to the server it was given for. Removing a server removes its skill approvals; a server added again under the same name starts with none.

### Who approves: the server's trust mode

The server's [trust mode](./security-quarantine.md#trust-modes-auto--scan--manual) decides what happens to a new and to a changed skill. The rule for changes is stricter than the rule for tools.

| Trust mode | New skill | Changed skill |
|------------|-----------|---------------|
| `manual` | held for you | held for you |
| `scan` (default) | approved if the scan of every text file is clean and complete; otherwise held | held for you, whatever the scan says |
| `auto` | approved | approved |

With quarantine turned off (`quarantine_enabled: false`) skills are approved as in `auto`. On a quarantined server no skill is available in any mode.

A skill that declares its files as `dynamic` has no content an approval could cover. It is `unusable`, cannot be approved, and no setting changes that.

### The scan

`SKILL.md` and every other text file of a skill go through the same offline scanner that checks tool descriptions. The result is stored with the skill:

- `verdict`: `clean`, `warnings` or `dangerous`;
- `complete`: whether every text file was scanned in full;
- `findings`: the rule, the file and the position in the file, with a short excerpt.

A scan is **not complete** when the skill has more than 1 MiB of text (`size_cap`), when the scan of one skill took longer than two minutes (`time_limit`), when the scanner reported reduced coverage (`coverage`), or when it was stopped before the end (`interrupted`). A scan that is not complete never approves a skill in `scan` mode. Binary files are not scanned and not served.

The scanner was tuned on tool descriptions. A skill that tells an agent to run commands can be flagged without being hostile; in `scan` mode that only means the skill waits for you.

### Approve, block, unblock

```bash
mcpproxy skills list --server hf          # what is held, with the scan result
mcpproxy skills show hf:hf-cli            # read it
mcpproxy skills show hf:hf-cli --diff     # what changed since the last approval
mcpproxy skills approve hf:hf-cli         # approve exactly what was shown
mcpproxy skills approve --server hf --all # approve every held skill of one server
mcpproxy skills block hf:hf-cli           # never available until you unblock it
mcpproxy skills unblock hf:hf-cli
```

`approve` sends the content hash it showed you. If the skill changed between the listing and the approval, the request is refused with `review_out_of_date` and **nothing** is approved: read the skill again. If the server was removed and added again in between, the answer is `server_replaced`.

A blocked skill stays blocked through refreshes, content changes and automatic approvals. Blocking does not delete the approval: `unblock` makes the skill available again if its content is still the approved one.

Every approval (by you, by the scan, or by `auto` mode), every hold of a changed skill, and every block and unblock is recorded in the activity log as `skill_quarantine_change`, with who did it and the hashes before and after:

```bash
mcpproxy activity list --type skill_quarantine_change
```

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

All commands need a running core (`mcpproxy serve`) and administrator credentials. They support `-o json` and `-o yaml`. `approve`, `block` and `unblock` are described under [Reviewing skills](#reviewing-skills).

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

`--file` picks another text file. `--approved` reads the approved copy, which exists once the skill has been approved. `--diff` shows what changed since the last approval. The command reads long files in chunks until the end. A binary file cannot be shown.

### `mcpproxy skills refresh <server>`

```bash
mcpproxy skills refresh hf
```

```
hf: refreshed, 3 skills (2 pending, 1 unusable)
```

A refresh also runs by itself when a server connects, on the periodic discovery sweep, and when the configuration changes.

## REST API

All of them require administrator credentials; a scoped agent token gets `403`, and so does a user session in the server edition. The same shapes are in [REST API: Skills](../api/rest-api.md#skills).

| Endpoint | Purpose |
|----------|---------|
| `GET /api/v1/servers/{id}/skills` | Every skill record of the server |
| `GET /api/v1/servers/{id}/skills/content?skill=<path>&file=<relpath>&copy=current\|approved&offset=<n>&limit=<n>` | One text file, in chunks of at most 64 KiB |
| `POST /api/v1/servers/{id}/skills/refresh` | Read the server's skills again and return the listing |
| `POST /api/v1/servers/{id}/skills/approve` | Approve skills for the content named in `expected_hashes` |
| `POST /api/v1/servers/{id}/skills/block`, `.../unblock` | Set or clear the blocked mark |

## Troubleshooting

**The list is empty and `supported` is `false`.** The server does not announce the skills extension. `mcpproxy skills refresh <server>` answers `skills_not_supported` in that case. Check that the server is enabled and not quarantined, and that it is a server that serves skills.

**The command prints that skills are off.** Set `enable_skills: true` in the configuration. REST answers `{"enabled": false, "skills": []}`, and refresh answers `409 skills_disabled`.

**`truncated` is `true`.** The server listed more skills, or a larger listing, than the limits above allow, and MCPProxy stopped reading. The skills it did read are kept.

**A skill shows `server_bound_exceeded`.** The refresh read budget or the server's stored text (64 MiB) ran out before this skill was read. The skills read before the limit are kept; later ones are marked unusable. Removing large skills on the server, or excluding it with `expose_skills: false`, frees the budget.

**A skill I approved shows `changed`.** The server changed its content. Compare with `mcpproxy skills show <server>:<skill> --diff` and approve again if the change is fine. Agents keep being refused until then.

**`409 review_out_of_date` on approve.** The skill changed after you looked at it. Nothing was approved. List and read it again.

**A clean skill stays `pending` on a `scan` server.** Look at `scan.complete`. A scan that is not complete (`size_cap`, `time_limit`, `coverage`, `interrupted`) does not approve. A skill shown as "not scanned" is waiting for its turn: one refresh scans for about 45 seconds and the next one continues. Approve the skill yourself after reading it.

**`409 stored_copy_damaged` when reading a file.** The stored bytes no longer match their digest. Run `mcpproxy skills refresh <server>` to read the skill again.
