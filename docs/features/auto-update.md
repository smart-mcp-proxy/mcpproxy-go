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
[Prerelease Builds](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/prerelease-builds.md) (the RC channel).

:::warning macOS one-click is built but not switched on
The updater ships in every macOS build and does nothing until a maintainer
publishes the update feed. Until then the tray falls back to browser downloads.
See the [activation checklist](#activation-checklist) for the exact remaining
steps.
:::

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

Honest click count: **two**. Ours, and Sparkle's own "Install and Relaunch"
confirmation, which its public API gives no way to skip.

Background checks deliberately do **not** pre-download, even though that would
make the second click instant. In Sparkle the pre-download flag also selects
the update driver, and the pre-downloading one arms a silent *install-on-quit*:
the bundle gets replaced by an external tool once MCPProxy exits, on a path
where no delegate can call it off. That would replace the app underneath a core
nobody had confirmed was stopped — the bug this feature exists to fix. A slower
click is the better trade.

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
than hanging: the stop is `SIGTERM`, a five-second grace period, then `SIGKILL`,
and then a bounded wait to *confirm* the process is actually gone.

If it is not gone — it outlived `SIGKILL`, or its pid can no longer be
identified as an mcpproxy process — the installation is **postponed**. The
downloaded update stays downloaded, the menu item stays where it was, the
running version keeps working, and the menu reports why. Replacing the bundle
under a live core is the bug, so "we could not confirm it stopped" is a reason
not to install, not a line in a log.

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
predates it. There is never more than one item:

- feed offer present → the feed owns the item and the GitHub check is silent,
  whatever version it found. Even when GitHub is a release ahead: what the feed
  offers is real and installable in one click, and the next check picks the
  newer version up. Two items would ask the user to choose between an install
  the tray can perform and a download it cannot verify;
- feed has nothing (unreachable, 404, or genuinely up to date) → the GitHub
  result renders as **"Update available: vX.Y.Z — Download"** and opens the
  browser, never a one-click action it cannot perform.

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

**The tray performs no unattended check until this contract arrives.** Before
any core has answered, automatic checks are off, not on: a permissive default
would let the launch check fire under the previous policy, checking for updates
for someone who had just switched them off. A core too old to report
`update_policy` is a different case — the tray recognises it on connect and
keeps it on the pre-092 behaviour. "Check for Updates" from the menu works
throughout.

## Release channels

Stable users are never offered a release candidate. The mechanism is Sparkle
channels:

- stable releases are published with **no channel tag**, which Sparkle offers
  to everyone;
- RC releases are tagged `<sparkle:channel>beta</sparkle:channel>`, and Sparkle
  offers a tagged item only to clients that ask for that channel. The tray asks
  for `beta` only when the core reports `update_policy.channel == "rc"`.

See [Prerelease Builds](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/prerelease-builds.md) for how to get on the RC channel.

## Release infrastructure

Generated by the release pipeline, per stable release and per RC:

| Artifact | What it is |
|---|---|
| `mcpproxy-<version>-darwin-<arch>.app.zip` | The update **enclosure**: the notarized, stapled app bundle archived with `ditto -c -k --sequesterRsrc --keepParent` (symlink-preserving — anything else breaks the signature seal and macOS answers `Killed: 9`). Listed in `checksums.txt`. |
| `appcast-arm64.xml` / `appcast-amd64.xml` | The stable feeds, EdDSA-signed. |
| `appcast-beta-arm64.xml` / `appcast-beta-amd64.xml` | The RC feeds, additionally tagged `sparkle:channel = beta`. |

**One feed per architecture, and one per channel.** A Sparkle appcast has no
architecture selector, and both macOS bundles report the same version — a single
merged feed would hand an Intel user the Apple-Silicon build. The tray rewrites
the configured `SUFeedURL` to request its own:

| Channel | arm64 | amd64 |
|---|---|---|
| stable | `…/appcast-arm64.xml` | `…/appcast-amd64.xml` |
| rc | `…/appcast-beta-arm64.xml` | `…/appcast-beta-amd64.xml` |

Only the exact file name `appcast.xml` is rewritten; a feed URL with any other
name is used verbatim, so an operator serving a universal or merged feed is not
second-guessed.

Each RC release also carries `checksums.txt` and its `checksums.txt.cosign.bundle`,
so `mcpproxy update` can verify a prerelease the same way it verifies a stable
one.

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

## Activation checklist

**One-click update is built but not switched on.** Everything below is in the
repository and runs on every release; what is missing is a server answering the
feed URL. Until step 3 is done, `SPUUpdater.start()` fails or the feed 404s, the
tray logs *"One-click updates unavailable"*, and every install falls back to the
browser-download item. That is the designed failure direction — no key and no
feed can never mean an unverified install — but it does mean nobody gets
one-click until a maintainer completes these steps.

| # | Step | Where | Done? |
|---|---|---|---|
| 1 | Generate an EdDSA key pair with Sparkle's `generate_keys` | local, once | ☐ |
| 2 | Add `SPARKLE_ED_PRIVATE_KEY` and `SPARKLE_ED_PUBLIC_KEY` as repository **secrets** | repo settings | ☐ |
| 3 | Serve the four feed files at `https://mcpproxy.app/` (see below) | **website repo** | ☐ |
| 4 | Set `auto_updates true` in the Homebrew cask | `smart-mcp-proxy/homebrew-mcpproxy` | ☐ |
| 5 | Cut a release and confirm the tray logs `Sparkle updater started` | — | ☐ |

Steps 1, 2 and 5 are ordinary release work. Step 3 is the blocker, and step 4
must land **before** the first Sparkle-capable release or `brew` and the in-app
updater will fight over the same install.

### What the website repository must serve

The shipped `Info.plist` sets `SUFeedURL` to `https://mcpproxy.app/appcast.xml`,
and this repository cannot publish there directly. Publishing is automated as a
hand-off: after uploading the signed feeds as release assets, `release.yml`
(stable) and `prerelease.yml` (beta) fire a `publish-appcast`
repository dispatch (via `MARKETING_SITE_DISPATCH_TOKEN`, same pattern as the
existing marketing version bump) at the website repo, whose
`publish-appcast.yml` workflow downloads the feeds from the public release,
verifies them (XML + RSS + `sparkle:edSignature`), and commits them into
`public/` for Cloudflare Pages. Manual backfill: run that workflow via
`workflow_dispatch` with a tag + channel. The feeds are also exported as
workflow artifacts for inspection:

| Workflow artifact | Files | Served at |
|---|---|---|
| `sparkle-appcast` (from `release.yml`) | `appcast-arm64.xml`, `appcast-amd64.xml` | `https://mcpproxy.app/appcast-arm64.xml`, `https://mcpproxy.app/appcast-amd64.xml` |
| `sparkle-appcast-beta` (from `prerelease.yml`) | `appcast-beta-arm64.xml`, `appcast-beta-amd64.xml` | `https://mcpproxy.app/appcast-beta-arm64.xml`, `https://mcpproxy.app/appcast-beta-amd64.xml` |

Three things the serving side must get right:

- **The file names are the contract.** The tray derives them from `SUFeedURL` by
  substitution (`SparkleFeedURL.archSpecific`); nothing at
  `https://mcpproxy.app/appcast.xml` itself is ever fetched. Serving only
  `appcast.xml` activates nothing.
- **The enclosure URLs inside the feeds already point at GitHub release assets**
  (`generate_appcast --download-url-prefix`), so the website only serves four
  small XML files — not the ~40 MB archives.
- **Serve as `application/xml` over HTTPS**, and do not rewrite the bodies: the
  EdDSA signature covers the enclosure, and any proxy that alters the XML breaks
  the feed parse.

### Interim URL without the website

For the **stable** channel only,
`https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/appcast-<arch>.xml`
works today — GitHub resolves `latest` to the newest non-prerelease release.
Point the `SPARKLE_FEED_URL` repository variable at
`https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest/download/appcast.xml`
and the per-architecture rewrite does the rest.

This cannot serve the beta channel: RC releases are prereleases and are never
`latest`, so the RC feed needs a real host either way.

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

**No update item at all, and the log shows a feed error.** Expected until the
[activation checklist](#activation-checklist) is complete — nothing serves
`appcast-<arch>.xml` yet, so the feed 404s. A 404 reads as "the feed has
nothing", not as an error that hides the offer: the GitHub check still produces
the browser-download item.

**"The update was not installed: the MCPProxy core is still running."** The
managed core did not die within the stop ladder, so the installation was
postponed rather than performed over a live process. Quit the core
(`mcpproxy upstream list` will show whether one is answering) and click the
update item again.

**The old core keeps answering after an upgrade.** That is the Phase-0
supersede path, not the updater — see the tray's "Old core vX running — Restart
into vY" item, and `mcpproxy status` (`Launched by:`) to see who started it.

**An RC was offered on a stable install.** Check `update_check.channel` and
`MCPPROXY_ALLOW_PRERELEASE_UPDATES`; `GET /api/v1/info` reports the effective
value under `update_policy.channel`.
