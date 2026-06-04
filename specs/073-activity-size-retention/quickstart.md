# Quickstart: Activity-Log Size Cap

## Configure

In `~/.mcpproxy/mcp_config.json` (or via the config the server loads):

```json
{
  "activity_max_size_mb": 256
}
```

- Default is `256` (applied even with no config).
- Set to `0` to disable size-based pruning (age/count caps still apply).
- Works alongside `activity_retention_days` (default 90) and `activity_max_records` (default 100000).

## What happens

On startup and every `activity_cleanup_interval_min` (default 60), retention runs:
age prune → count prune → **size prune** (deletes oldest activity records until the log is ≤ the byte budget, always keeping the newest record). When it removes records you'll see a log line like:

```
Pruned activity records to size budget  deleted=12345  max_size_mb=256
```

## Verify locally (TDD)

```bash
# Storage-level prune behaviour (size, oldest-first, keep-newest, disabled)
go test ./internal/storage/ -run TestPruneActivitiesToSize -v

# Retention wiring (size cap invoked in the cleanup cycle)
go test ./internal/runtime/ -run TestActivityRetention -v

# Full suites
go test ./internal/storage/ ./internal/runtime/
```

## Reclaiming disk after a big prune (note)

Deleting records frees BBolt pages for reuse but does **not** shrink `config.db` on disk (BBolt never returns pages to the OS). File compaction is a separate follow-up; this feature bounds *growth*, preventing the log from getting large in the first place.
