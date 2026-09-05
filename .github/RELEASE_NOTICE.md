## 📉 If your `config.db` has grown large, this release needs one manual step

A user reported `~/.mcpproxy/config.db` reaching **940MB** ([#1173](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1173)). This release fixes both causes and adds a command to reclaim the space — but **the space is not reclaimed automatically**, because BBolt never returns freed pages to the operating system.

### Check whether you are affected

```bash
ls -lh ~/.mcpproxy/config.db
```

A few MB is normal. Hundreds of MB is not.

### Reclaim the space

Compaction needs exclusive access, so **stop mcpproxy first** (quit the tray app, or stop `mcpproxy serve`). Then:

```bash
mcpproxy db stats     # how much is reclaimable
mcpproxy db compact   # rewrite the file
```

`db compact` keeps the pre-compaction database as `config.db.bak` by default. **That backup holds the old file, so nothing is freed on disk until you delete it.** The command says so in its output. Start mcpproxy, confirm your servers and history are intact, then:

```bash
rm ~/.mcpproxy/config.db.bak
```

If you are already out of disk space, run `mcpproxy db compact --keep-backup=false` instead — it frees the space immediately, with no rollback.

### What was wrong

**Activity records were never truncated** ([#1173](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1173)) — `activity_max_response_size` was declared with a 64KB default and never read, so a single 10.75MB response was stored whole.

**The per-server tool-call history was unbounded** ([#1176](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1176)) — no size cap, no count cap, no age prune, and nothing ever deleted a server's history, even after the server was removed from the config. This was ~432MB of that 940MB. Responses and arguments are now capped per record, each server keeps a bounded recent window, and removing a server drops its history with it.

## ⚙️ Configuration changes worth knowing

**An explicit `0` is no longer silently deleted** ([#1175](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1175)). `max_result_size_chars`, `activity_max_size_mb` and `observability.tracing.sample_rate` document `0` as a meaningful value ("disable"), but saving the config erased it and the field reverted to its default. If you set one of these to `0` and it kept coming back, that is why — it now persists.

**`activity_retention_days: 0` and `activity_max_records: 0` changed meaning.** They previously fell through to much smaller internal defaults (7 days / 10,000 records) rather than the documented ones. Both now resolve to the documented defaults (90 days / 100,000 records). If you relied on `0` to keep the log small, set the value you actually want.

**New settings**, both defaulting to what the fix applies anyway:

| Setting | Default | Meaning |
|---|---|---|
| `tool_call_max_response_size` | `65536` | Cap on the response stored per tool-call record, in bytes |
| `tool_call_max_records_per_server` | `1000` | Tool calls retained per server |

A record shortened for storage is marked `response_truncated` / `arguments_truncated`; the caller always received the full response, and the upstream server always received the full arguments. Replaying a call whose stored arguments were shortened is refused rather than re-sending the placeholder — pass arguments explicitly to replay it.
