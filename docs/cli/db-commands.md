---
id: db-commands
title: Database Commands
sidebar_label: Database Commands
sidebar_position: 7
description: Offline maintenance commands for the MCPProxy BBolt database - compaction and size statistics
keywords: [db, compact, bbolt, database, disk space, maintenance, cli]
---

# Database Commands

The `mcpproxy db` command group performs **offline** maintenance on the MCPProxy
database (`~/.mcpproxy/config.db`).

## Why compaction is needed

MCPProxy stores configuration, the tool index and per-server tool-call history in
a single [BBolt](https://github.com/etcd-io/bbolt) file. BBolt returns freed
pages to its own internal freelist — **never to the operating system**.

The practical consequence: pruning tool-call history, deleting servers or
lowering retention bounds shrinks the database *logically*, but `config.db` stays
at its all-time high-water mark on disk. A user report ([#1176](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1176))
described a 940 MB `config.db` that stayed 940 MB after pruning.

`mcpproxy db compact` rewrites the live data into a fresh file, which is the only
way to hand those pages back to the filesystem.

## Overview

```
mcpproxy db
├── stats     Report file size, reclaimable space and the largest buckets
└── compact   Rewrite config.db to return freed pages to the filesystem
```

Both subcommands need the database lock, so **stop MCPProxy first** — quit the
tray app, or stop the `mcpproxy serve` process. If the database is locked, both
commands fail fast with exit code **3** and a message telling you what to stop;
they never retry, because the running core holds its lock for its whole lifetime.

Both subcommands support the standard output flags (`-o table|json|yaml`,
`--json`, `MCPPROXY_OUTPUT`). Human banners are written to **stderr**, so stdout
stays parseable in `json`/`yaml` mode.

## `mcpproxy db stats`

Read-only inspection: find out whether compaction is worth running before you
stop the proxy for it.

```bash
mcpproxy db stats
mcpproxy db stats --top 25
mcpproxy db stats -o json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--top <n>` | `10` | Number of buckets to report, ranked by key count |

Example:

```
Database: /Users/me/.mcpproxy/config.db
Size on disk: 16.0 MB | free pages: 755 (11.8 MB reclaimable) | page size: 16384

BUCKET                   KEYS  SUB-BUCKETS
config                   500   0
server_alpha_tool_calls  1     0
server_beta_tool_calls   1     0
```

Every `server_<id>_tool_calls` bucket is always listed, even when it falls
outside the `--top` cut — those are the buckets that grow without bound, so they
are the ones you are looking for.

`reclaimable_bytes` is `free_page_count × page_size`: roughly what a compaction
would give back. If it is a small fraction of `file_bytes`, compaction is not
worth the downtime.

:::note Read-only still needs the lock
`db stats` opens the database read-only, which takes a *shared* lock. Several
`db stats` runs can therefore proceed concurrently — but a running MCPProxy holds
an *exclusive* lock, which still shuts `db stats` out. Stop MCPProxy first.
:::

## `mcpproxy db compact`

```bash
mcpproxy db compact
mcpproxy db compact --keep-backup=false
mcpproxy db compact --tx-max-size 134217728
mcpproxy db compact -o json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--keep-backup` | `true` | Keep the pre-compaction database as `config.db.bak` |
| `--tx-max-size <bytes>` | `67108864` (64 MB) | Maximum size of a single write transaction during the rewrite (`0` = unbounded) |

Example:

```
Compacting /Users/me/.mcpproxy/config.db ...
METRIC     VALUE
before     16.0 MB
after      4.0 MB
reclaimed  12.0 MB
backup     /Users/me/.mcpproxy/config.db.bak
```

JSON output carries the raw byte counts so scripts do not have to parse a unit
suffix:

```json
{
  "path": "/Users/me/.mcpproxy/config.db",
  "before_bytes": 16777216,
  "after_bytes": 4194304,
  "reclaimed_bytes": 12582912,
  "backup_path": "/Users/me/.mcpproxy/config.db.bak"
}
```

### Safety properties

- The rewrite goes to a **temporary file in the same directory** and is moved
  into place with a single `rename`. An interrupted or failed compaction leaves
  the original `config.db` byte-for-byte untouched, and the temporary file is
  removed.
- The compacted file is **fsynced** before the rename and the parent directory
  after it, so the swap survives a power loss. (The directory fsync is
  best-effort: Windows cannot fsync a directory handle.)
- The source database is opened **read-only**, so nothing can write to it even
  in the failure paths.
- With `--keep-backup` (the default) the pre-compaction file is preserved as
  `config.db.bak` via a hard link, so `config.db` never stops existing during the
  operation. A previous `.bak` is replaced.
- File permissions are carried over from the original database.

### Disk space

Compaction peaks at **old size + new size**, because both files exist until the
rename. Make sure the filesystem holding `~/.mcpproxy` has at least as much free
space as the current `config.db` (see `db stats` for its size).

**The default run does not free disk on its own.** With `--keep-backup` (the
default) the pre-compaction database survives as `config.db.bak`, holding the
original inode — so `reclaimed` in the output describes the *file*, not the
filesystem. The command reports this directly:

```
before          940.2 MB
after           312.4 MB
reclaimed       627.8 MB
backup          /Users/you/.mcpproxy/config.db.bak
backup size     940.2 MB
disk freed now  -312.4 MB
```

Check that the new `config.db` is healthy (start mcpproxy, confirm your servers
and history are there), then delete the backup to actually get the space back:

```bash
rm ~/.mcpproxy/config.db.bak
```

If you are already out of disk, run `mcpproxy db compact --keep-backup=false` —
it frees the space immediately, at the cost of having no rollback.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General failure (I/O error, compaction error) |
| `3` | Database is locked — stop MCPProxy and retry |

## Related

- [Management Commands](management-commands.md)
- [Activity Commands](activity-commands.md) — pruning tool-call history, which is what creates the reclaimable space
