---
id: MCPX_HTTP_CANCELED
title: MCPX_HTTP_CANCELED
sidebar_label: CANCELED
description: The connection attempt was canceled by mcpproxy itself — shutdown, config reload, or a manual disconnect.
---

# `MCPX_HTTP_CANCELED`

**Severity:** info
**Domain:** HTTP

## What happened

The attempt did not fail — it was **canceled**. mcpproxy stopped waiting for the
upstream on purpose, so the error you are looking at describes mcpproxy's own
decision rather than anything the server did wrong.

It is recorded at `info` severity for exactly that reason: it is a lifecycle
event, not a fault, and it should never send you looking for a bug.

## Common causes

- mcpproxy is shutting down, and in-flight connection attempts were canceled.
- The config file changed and the file watcher rebuilt this server's client.
- You disabled, restarted, or removed the server (from the tray, the web UI, or
  `mcpproxy upstream disable|restart`).
- A parent request the connection was serving was itself canceled.

## How to fix

Usually nothing. Check the server's current state:

```bash
mcpproxy upstream list
```

If it settled into *Ready*, the cancellation was routine.

### If it repeats and the server never reaches Ready

Something is cancelling the attempt before it can finish. Look for a config file
being rewritten in a loop (each write triggers a reload) and check the log
around the cancellations:

```bash
mcpproxy upstream logs <server> --follow
```

## Related

- [`MCPX_HTTP_CONN_RESET`](MCPX_HTTP_CONN_RESET.md) — the *server* dropped the connection instead
- [`MCPX_STDIO_EXIT_BEFORE_INITIALIZE`](MCPX_STDIO_EXIT_BEFORE_INITIALIZE.md) — the stdio equivalent of a connection ending early
