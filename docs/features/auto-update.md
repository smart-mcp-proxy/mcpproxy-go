---
id: auto-update
title: Auto-Update
sidebar_label: Auto-Update
description: One-click updates in the macOS tray, the channel-aware mcpproxy update CLI, and every switch that turns them off
keywords: [update, auto-update, sparkle, appcast, upgrade, channel, rc]
---

# Auto-Update

MCPProxy can update itself. What "update itself" means depends on **how it was
installed** — a package manager owns its own files and must never be fought
over, so MCPProxy only ever replaces what it owns and prints the exact command
for everything else.

Related: [Version Updates](/features/version-updates) (how the *check* works),
[Prerelease Builds](/prerelease-builds) (the RC channel).

## Channel matrix

| Install channel | `mcpproxy update` (CLI) | macOS tray |
|---|---|---|
| **DMG / PKG** (`dmg`) | Points at the tray updater; never touches the app bundle or a staged copy of it | **One-click update** — download, verify, replace, relaunch |
| **tarball** | **Self-update**: downloads the release archive, verifies it against the signed `checksums.txt`, swaps the binary atomically | n/a |
| **Homebrew** | Prints `brew upgrade mcpproxy` | n/a (cask `auto_updates true`, see below) |
| **deb** | Prints the `apt` command | n/a |
| **rpm** | Prints the `dnf`/`yum` command | n/a |
| **go-install** | Prints the `go install` command | n/a |
| **Docker** | Prints the `docker pull` guidance | n/a |
| **windows-installer** | Prints download guidance | n/a |
| **unknown** | **Guidance only.** Writability does not establish ownership — AUR, MacPorts and Nix-like layouts all land here. `--self` is the explicit override, with a warning | n/a |

`mcpproxy update --check` reports the current version, the latest version and
the detected channel for **every** channel, with no side effects.

Self-update refuses a downgrade unless you pass **both** an explicit
`--version` and `--force`; it never escalates privileges, and it never writes
inside `MCPProxy.app`.

## The macOS one-click flow

1. A background check reads the update feed (an *appcast*) at most every four
   hours.
2. When something newer exists, the tray menu grows one gentle line:
   **"Update 0.55.0 — ready to restart?"**. No popup, no dock bounce, no
   interruption — the menu item *is* the notification.
3. Clicking it runs the whole thing: download → **verify** → stop the managed
   core → replace the installed app → relaunch on the new version.

### Verification (two independent checks)

Nothing is installed unless both pass:

- **EdDSA signature** over the downloaded archive, checked against the public
  key baked into the running app's `Info.plist` (`SUPublicEDKey`). The feed XML
  is not what carries the signature — each enclosure is signed individually.
- **macOS code-signature and notarization** of the replacement bundle.

A failure of either aborts with nothing replaced and an actionable error. The
running version keeps working.

### Why the core is stopped first

The updater stops the tray-managed core **before** the bundle is replaced, not
after. A core still executing from a replaced (deleted-inode) bundle is exactly
the failure reported in issue #957. In-flight tool calls fail visibly rather
than hanging: the stop is `SIGTERM`, a five-second grace period, then `SIGKILL`.

The Phase-0 supersede check stays active permanently as the safety net for the
paths the updater does not control — drag-installs, PKG runs, install-on-quit.

### When one-click cannot work

If the app is **translocated** (opened straight from a download or a mounted
disk image), on a **read-only volume**, or in a directory this user cannot
write, the menu says so and offers the fallback — it never shows an update item
that would quietly do nothing:

> *Can't update — move MCPProxy to Applications first*

Move `MCPProxy.app` into `/Applications` (or `~/Applications`) and open it from
there; updates work from then on.

### One item, one owner

Two mechanisms know about releases: the feed, and the daily GitHub check that
predates it. They never both nudge:

- feed offer present → the feed owns the item, and the GitHub check is silent
  for the same or an older version;
- GitHub only (feed unreachable, or it does not carry that version) → the item
  reads **"Update available: vX.Y.Z — Download"** and opens the browser, never
  a one-click action it cannot perform;
- equal versions → exactly one item.

## Kill switches

| Switch | Effect |
|---|---|
| `update_check.enabled: false` | No automatic check anywhere: no core poll, no tray feed check, no nudge. Reported to the tray as `update_policy.enabled = false`. |
| `MCPPROXY_DISABLE_AUTO_UPDATE=true` | Same, and wins over the config value. Read by **both** the core and the tray, so one export silences the whole app. |
| `CI=true` / `CI=1` | Nudges are suppressed and the tray performs no unattended checks — a scheduled check exists only to produce a nudge nobody will read. Machine-readable fields keep reporting the facts. |
| `update_check.channel: "rc"` | Offers prereleases; maps to the Sparkle **beta** channel. |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES=true` | Forces the `rc` channel over the config value. |

**A user-initiated "Check for Updates" is always available**, including with
every switch above turned on. The switches govern what happens *unasked*.

### The policy contract

The tray does not guess. `GET /api/v1/info` always carries:

```json
"update_policy": { "enabled": true, "channel": "stable", "nudges_suppressed": false }
```

All three fields are always present. This exists because the optional `update`
object is absent both when checking is disabled **and** when no check has run
yet — its absence cannot tell a client whether it is allowed to check. The
policy is recomputed per request, so editing `update_check` in the config file
takes effect on the tray's next connect with no restart.

## Release channels

Stable users are never offered a release candidate. The mechanism is Sparkle
channels:

- stable releases are published with **no channel tag**, which Sparkle offers
  to everyone;
- RC releases are tagged `<sparkle:channel>beta</sparkle:channel>`, and Sparkle
  offers a tagged item only to clients that ask for that channel. The tray asks
  for `beta` only when the core reports `update_policy.channel == "rc"`.

See [Prerelease Builds](/prerelease-builds) for how to get on the RC channel.

## Release infrastructure

Generated by the release pipeline, per stable release and per RC:

| Artifact | What it is |
|---|---|
| `mcpproxy-<version>-darwin-<arch>.app.zip` | The update **enclosure**: the notarized, stapled app bundle archived with `ditto -c -k --sequesterRsrc --keepParent` (symlink-preserving — anything else breaks the signature seal and macOS answers `Killed: 9`). Listed in `checksums.txt`. |
| `appcast-arm64.xml` / `appcast-amd64.xml` | The stable feeds, EdDSA-signed. |
| `appcast-beta-arm64.xml` / `appcast-beta-amd64.xml` | The RC feeds, additionally tagged `sparkle:channel = beta`. |

**One feed per architecture.** A Sparkle appcast has no architecture selector,
and both macOS bundles report the same version — a single merged feed would
hand an Intel user the Apple-Silicon build. The tray rewrites the configured
`SUFeedURL` (`…/appcast.xml` → `…/appcast-arm64.xml`) to request its own; a feed
URL with any other file name is used verbatim, so an operator serving a
universal feed is not second-guessed.

### Signing keys

| Name | Kind | Used for |
|---|---|---|
| `SPARKLE_ED_PRIVATE_KEY` | repository **secret** | Signing enclosures and feeds in CI (`generate_appcast --ed-key-file -`; passed on stdin so it never lands on disk). |
| `SPARKLE_ED_PUBLIC_KEY` | repository **secret** | Stamped into the shipped `Info.plist` as `SUPublicEDKey`. |
| `SPARKLE_FEED_URL` | repository **variable** (optional) | Overrides the compiled-in feed URL. |

Generate the pair once with Sparkle's `generate_keys`. Every new CI step is
guarded on the private key being present, so forks and key-less builds skip
enclosure and appcast generation gracefully — and a build without
`SPARKLE_ED_PUBLIC_KEY` keeps the `SPARKLE_PUBLIC_KEY_PLACEHOLDER` value, which
makes the updater refuse to start and the tray fall back to browser downloads.
That is the correct failure direction: no key, no one-click, never an
unverified install.

:::warning Feed hosting is not yet decided
The shipped `Info.plist` points at `https://mcpproxy.app/appcast.xml`, which the
**website** repository would have to serve — this repository cannot publish
there. Until that is wired, each release attaches its feeds as release assets
and exports them as the `sparkle-appcast` (and `sparkle-appcast-beta`) workflow
artifact for the website repo to consume.

As an interim stable-channel feed URL,
`https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/appcast-<arch>.xml`
works today (GitHub resolves `latest` to the newest non-prerelease release). It
cannot serve the beta channel, because RC releases are prereleases and are never
`latest`.
:::

### Homebrew

The cask must be marked self-updating so `brew` does not fight the in-app
updater:

```ruby
auto_updates true
```

This lives in the **tap** repository (`smart-mcp-proxy/homebrew-mcpproxy`), not
here, and has to be set before the first Sparkle-capable release ships.

## Troubleshooting

**"One-click updates unavailable" in the log.** The updater could not start:
either the app is not running from a bundle (a `swift build` binary), or the
bundle carries the placeholder public key. The browser-download path still
works.

**The old core keeps answering after an upgrade.** That is the Phase-0
supersede path, not the updater — see the tray's "Old core vX running — Restart
into vY" item, and `mcpproxy status` (`Launched by:`) to see who started it.

**An RC was offered on a stable install.** Check `update_check.channel` and
`MCPPROXY_ALLOW_PRERELEASE_UPDATES`; `GET /api/v1/info` reports the effective
value under `update_policy.channel`.
