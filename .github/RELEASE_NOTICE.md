## 🔒 Rotate your API key if anyone else can read your mcpproxy logs

Earlier releases wrote the admin API key to the log files in plain text. It appeared in the access log whenever the key arrived as `?apikey=`, which is how the Web UI and the tray open the `/events` stream and the `/ui/` page, and in `main.log` when the key was auto-generated. This release redacts it on the access, authentication and startup log lines that carried it (SEC-01).

**What to do:** if your logs have been shared, collected by a log shipper, attached to an issue, or can be read by other users, treat the key as exposed.

1. Set a new value for `api_key` in `~/.mcpproxy/mcp_config.json`, or remove the field and one will be generated.
2. Restart mcpproxy.
3. Update every client, script and scraper that sends the old key. For clients set up with the Web UI's **Connect** dialog, click Disconnect and then Connect for each one, so its config gets the new key.
4. Delete the old log files: `~/Library/Logs/mcpproxy/` on macOS, `~/.local/state/mcpproxy/logs/` on Linux (or `$XDG_STATE_HOME/mcpproxy/logs/`, and `/var/log/mcpproxy/` when run as root), `%LOCALAPPDATA%\mcpproxy\logs\` on Windows.

The Web UI also stopped printing the key to the browser console.

## `/metrics` now requires the API key

If you enabled the Prometheus exporter (`observability.metrics.enabled`, off by default), `/metrics` now returns `401` without credentials. Agent tokens get `403`, because the endpoint exposes fleet-wide data. Send the admin key as `Authorization: Bearer <key>` (Prometheus `authorization.credentials`) or as `X-API-Key`. The health probes (`/healthz`, `/livez`, `/health`, `/readyz`, `/ready`) stay unauthenticated. See [Observability](https://docs.mcpproxy.app/features/observability).

## The REST API no longer allows any origin (CORS)

`/api/v1/*` and `/events` used to send `Access-Control-Allow-Origin: *`. They now allow loopback origins and the hosts listed in `trusted_hosts` only. The bundled Web UI and non-browser clients are unaffected. If a separate web app calls the REST API from another domain, add that domain to `trusted_hosts`. `MCPPROXY_TRUSTED_HOSTS` replaces the list rather than adding to it, and the same list also governs Host validation on the MCP endpoint.

## Tray self-update on Windows and on macOS tarball installs

The tray's built-in updater now checks every download against the release's `checksums.txt` and refuses to install on any mismatch. It also installs the tray binary. Earlier versions put the core binary in its place, so after a "successful" update the tray app turned into a headless core. If your tray stopped showing its menu after an earlier self-update, reinstall it from this release. DMG app bundles (Sparkle) and Homebrew installs were never affected.

## Also in this release

- `config.db`, which holds OAuth tokens, is now created owner-only (`0600`). Existing databases are tightened automatically on startup.
- The admin API key is compared in constant time.
