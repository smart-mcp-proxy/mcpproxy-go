# Release Runbook

Single source of truth for the six steps that, if broken, block an MCPProxy
release. Each section names the SPOF, points at the exact workflow job, lists
the secrets or external systems it depends on, and says what to do when it
fails.

This runbook is paired with:

- [`docs/releasing.md`](./releasing.md) — branch model (main / next / hotfix) and tag flow
- [`docs/prerelease-builds.md`](./prerelease-builds.md) — prerelease artifact distribution
- [`docs/release-notes-generation.md`](./release-notes-generation.md) — Claude-powered notes detail
- [`specs/043-linux-package-repos/`](../specs/043-linux-package-repos/) — apt/rpm repo spec + quickstart
- [`.github/workflows/release.yml`](../.github/workflows/release.yml) — the stable release pipeline
- [`.github/workflows/prerelease.yml`](../.github/workflows/prerelease.yml) — the `next` pipeline
- [`.github/workflows/retry-sign-release.yml`](../.github/workflows/retry-sign-release.yml) — SignPath retry

## Trigger summary

| Event                                   | Workflow                      | Produces                                        |
| --------------------------------------- | ----------------------------- | ----------------------------------------------- |
| Push to `next`                          | `prerelease.yml`              | Signed + notarized prerelease artifacts (no tag)|
| Push a `vX.Y.Z` tag on `main`           | `release.yml`                 | GitHub Release, Homebrew bump, apt/rpm republish|
| Push a `vX.Y.Z-rc*` / `-beta*` tag      | `release.yml` (partial)       | GitHub Release only (apt/rpm skipped)           |
| `workflow_dispatch` on Retry Sign       | `retry-sign-release.yml`      | Re-submit Windows EXE to SignPath, cut release  |

Everything below assumes a stable `vX.Y.Z` tag on `main` unless stated otherwise.

---

## SPOF 1 — macOS signing + notarization

**Goal:** DMG and PKG installers pass Gatekeeper on first-launch with no user
override (right-click → Open).

**Where:** `release.yml` → `build` job, steps `Import Apple Developer ID
certificate`, `Sign macOS binaries`, `Create DMG installer (macOS)`, `Create PKG
installer (macOS)`, `Submit for notarization (macOS)`. Prerelease runs the same
steps in `prerelease.yml`.

**What we use:**

- `codesign --force --sign "$CERT_IDENTITY" --timestamp --options runtime` for
  each Mach-O and the DMG.
- `xcrun notarytool submit … --wait` then `xcrun stapler staple` for both the
  PKG and the installer DMG.

**Required secrets (GitHub → Settings → Secrets → `production` environment):**

| Secret                                         | Purpose                                                    |
| ---------------------------------------------- | ---------------------------------------------------------- |
| `APPLE_DEVELOPER_ID_CERT`                      | base64-encoded `.p12` containing the Developer ID Application identity (and ideally the Installer identity too) |
| `APPLE_DEVELOPER_ID_CERT_PASSWORD`             | Password for the above `.p12`                              |
| `APPLE_DEVELOPER_ID_INSTALLER_CERT`            | Fallback `.p12` if the installer identity is separate      |
| `APPLE_DEVELOPER_ID_INSTALLER_CERT_PASSWORD`   | Password for the installer `.p12`                          |
| `APPLE_ID_USERNAME`                            | Apple ID email used for notarytool                         |
| `APPLE_ID_APP_PASSWORD`                        | App-specific password from appleid.apple.com               |
| `APPLE_TEAM_ID`                                | Apple Developer Team ID (fallback identity)                |

**Expiry windows to watch:**

- Developer ID certificate: 5 years. Renew at Apple Developer → Certificates
  _before_ the old one expires — once expired, already-shipped artifacts keep
  working (they are notarized by ticket, not cert), but new builds will fail.
- App-specific password: does not expire on a schedule, but is revoked if the
  Apple ID password is reset. If notarization starts 401-ing on otherwise-fine
  builds, that is the first suspect.

**Recovery:**

- If the `Import Apple Developer ID certificate` step fails, the cert was
  rotated or the secret is wrong. Re-export the `.p12` with the "Developer ID
  Application" and "Developer ID Installer" identities in the same bundle,
  `base64 -i cert.p12 | pbcopy`, and update both `*_CERT` and `*_CERT_PASSWORD`
  secrets in the `production` environment.
- If notarization times out or returns `Invalid`, fetch the log with
  `xcrun notarytool log <submission-id> --apple-id … --password …`. The most
  common cause is an unsigned helper binary inside the bundle (the core binary,
  `mcpproxy-tray`, or a Swift app bundle pulled in by `create-pkg.sh`). Re-sign
  with `--options runtime` and resubmit.
- Notarization outage: Apple occasionally has multi-hour backlogs. The release
  job waits up to the per-file timeout and retries. If it still fails, cut the
  release without notarization is **not an option** — hold the tag until
  notarytool recovers, or use the Retry Sign & Release pattern to rerun just
  the macOS legs (see the retry workflow for precedent).

**Verify locally after the release is out:**

```bash
spctl --assess --type exec -vvv /Applications/MCPProxy.app
xcrun stapler validate mcpproxy-*-darwin-*.dmg
codesign --verify --verbose --deep --strict /Applications/MCPProxy.app
```

---

## SPOF 2 — Windows installer signing (SignPath today, EV-cert decision pending)

**Goal:** Windows SmartScreen reputation is inherited by each new installer —
no "Unknown publisher" Defender prompt for users on recent builds of the app.

**Where:** `release.yml` → `sign-windows` job (matrix: amd64, arm64). The
`build` job produces `dist/mcpproxy-setup-<tag>-<arch>.exe` via Inno Setup
(`scripts/build-windows-installer.ps1`), uploads it as
`unsigned-installer-windows-<arch>`, and the `sign-windows` job feeds it to
SignPath with `signpath/github-action-submit-signing-request@v1`.

**External system:** [SignPath.io](https://signpath.io) organisation
`84efd51b-c11c-4a85-82e6-7c3b1157d7ca`, project `mcpproxy-go`, policy
`release-signing`. Signing is **manual-approval** with a 1-hour wait
(`wait-for-completion-timeout-in-seconds: 3600`). Missing the approval window
fails the whole release job.

**Required secrets:**

| Secret                | Purpose                                        |
| --------------------- | ---------------------------------------------- |
| `SIGNPATH_API_TOKEN`  | SignPath API token for the release policy      |

**Recovery when signing times out (most common failure):**

The release workflow fails in `sign-windows` because the SignPath approver did
not click "Approve" inside 60 minutes. All other artifacts (macOS DMGs, Linux
tarballs, release notes) are already built. Do **not** re-tag — use the retry
workflow:

1. Open the failed Release run, copy the run ID from the URL.
2. Actions → **Retry Sign & Release** → Run workflow with:
   - `tag`: `vX.Y.Z`
   - `run_id`: the failed run's ID
3. This workflow (`retry-sign-release.yml`) re-downloads the unsigned EXEs,
   resubmits to SignPath, and creates the GitHub Release from the original
   artifacts + the freshly signed installers.
4. Click **Approve** in SignPath as soon as the resubmission email arrives.

**Watch items:**

- SignPath API token: rotate yearly and on any suspected leak. Token comes from
  SignPath → Users → API tokens. Update `SIGNPATH_API_TOKEN` in the
  `production` environment.
- Approver list: keep at least two humans on the SignPath `release-signing`
  policy so a single vacation doesn't freeze releases.
- The signed EXE inherits SmartScreen reputation from SignPath's EV
  certificate. **Do not** switch signing providers without re-establishing
  reputation — new users will hit the "rarely downloaded" prompt for weeks.

**Decision doc pending (D30-6, GH [#45](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/45)):**

Whether to keep SignPath-hosted signing, move to a self-managed EV cert, or
migrate to Azure Trusted Signing is tracked in [MCP-7](/MCP/issues/MCP-7) as
D30-6. The decision doc will land at `docs/decisions/windows-signing.md`. Until
that lands, **keep using the current SignPath flow** — rotating to a different
signing backend mid-sprint risks resetting SmartScreen reputation.

**Verify locally:**

```powershell
# On a Windows host
Get-AuthenticodeSignature mcpproxy-setup-vX.Y.Z-amd64.exe
# Status should be "Valid", signer should contain "SignPath" or the EV CN
```

---

## SPOF 3 — Claude release notes generation

**Goal:** Each GitHub Release page has categorised, human-readable notes ("New
Features", "Bug Fixes", "Breaking Changes", "Improvements") instead of a raw
commit dump. The notes are also bundled into the DMG (`RELEASE_NOTES.md`) and
the Windows installer's docs folder.

**Where:** `release.yml` → `generate-notes` job → `scripts/generate-release-notes.sh`.
Runs in parallel with the `build` job; its output is consumed by the `release`
job when assembling the GitHub Release body.

**External system:** Anthropic Messages API, model
`claude-sonnet-4-5-20250929` (override with `CLAUDE_MODEL`).

**Required secrets:**

| Secret               | Purpose                                    |
| -------------------- | ------------------------------------------ |
| `ANTHROPIC_API_KEY`  | Claude API key (console.anthropic.com)     |

**Cost:** ~ $0.01–0.05 per release.

**Failure behaviour:** Non-blocking. If the API call fails, the script writes
the fallback "Release notes could not be generated automatically" stub and the
release still ships. See [release-notes-generation.md](./release-notes-generation.md).

**Cadence knobs (env vars on `generate-notes` step):**

| Variable              | Default                       | When to change                                     |
| --------------------- | ----------------------------- | -------------------------------------------------- |
| `CLAUDE_MODEL`        | `claude-sonnet-4-5-20250929`  | Migrate model on Anthropic deprecation notices     |
| `MAX_TOKENS`          | `1024`                        | Raise for big sprints (>100 commits behind)        |
| `MAX_COMMITS`         | `200`                         | Lower only if we hit the context limit             |
| `API_TIMEOUT`         | `30`                          | Raise on Anthropic incidents                       |

**Recovery:**

- Fallback notes landed on the release — re-run locally and edit the release
  body manually:
  ```bash
  export ANTHROPIC_API_KEY="..."
  ./scripts/generate-release-notes.sh vX.Y.Z
  gh release edit vX.Y.Z --notes-file release-notes-vX.Y.Z.md
  ```
- Anthropic model deprecation notice: bump `CLAUDE_MODEL` env var on the
  `generate-notes` job in `release.yml` (and keep it in sync in
  `prerelease.yml` if that job ever gains notes generation). Model IDs are in
  `~/.claude/CLAUDE.md` / `generate-release-notes.sh`.

---

## SPOF 4 — Cloudflare R2 apt/yum repository publish (spec 043)

**Goal:** `apt-get install mcpproxy` / `dnf install mcpproxy` work after a
release, backed by `apt.mcpproxy.app` and `rpm.mcpproxy.app` — both Cloudflare
R2 buckets with custom domains. Metadata is GPG-signed with the MCPProxy
Packages key.

**Where:** `release.yml` → `publish-linux-repos` job. Runs only on **stable**
tags — `if: startsWith(github.ref, 'refs/tags/v') && !contains(github.ref_name,
'-')`. Pre-release tags (`v0.24.0-rc1`, `v0.24.0-beta`) intentionally skip this
step so repository metadata never publishes unstable versions.

**What it does (per spec [`043-linux-package-repos`](../specs/043-linux-package-repos/)):**

1. Downloads `linux-packages-*` artifacts (the `.deb` and `.rpm` files
   produced by `build`).
2. Syncs existing R2 bucket contents to the runner, adds the new packages,
   prunes to `RETAIN_N=10` versions.
3. Regenerates metadata with `apt-ftparchive` (Debian) and `createrepo_c`
   (Fedora), signs `Release` / `repomd.xml` with the imported GPG key.
4. Syncs back to R2.
5. Smoke-tests `apt-get install` in `debian:stable-slim` and
   `dnf install` in `fedora:latest` containers.

**External systems:**

- Cloudflare R2 buckets `mcpproxy-apt` and `mcpproxy-rpm` (custom domains
  `apt.mcpproxy.app` / `rpm.mcpproxy.app`, account-scoped R2 token).
- MCPProxy Packages GPG key (RSA 4096, UID `MCPProxy Packages
  <mcpproxy-packages@mcpproxy.app>`, 5-year expiry). Fingerprint is in the
  `PACKAGES_GPG_KEY_ID` GitHub variable.

**Required secrets + variables (`production` environment):**

| Name                         | Kind     | Purpose                                            |
| ---------------------------- | -------- | -------------------------------------------------- |
| `R2_ACCOUNT_ID`              | secret   | Cloudflare account ID (endpoint URL)               |
| `R2_ACCESS_KEY_ID`           | secret   | R2 token scoped to both buckets                    |
| `R2_SECRET_ACCESS_KEY`       | secret   | R2 token secret                                    |
| `PACKAGES_GPG_PRIVATE_KEY`   | secret   | ASCII-armored private key + metadata header        |
| `PACKAGES_GPG_PASSPHRASE`    | secret   | Passphrase (stored in 1Password too)               |
| `PACKAGES_GPG_KEY_ID`        | variable | GPG key fingerprint (selects the right key at sign time) |

One-time setup (generate key, create buckets, bind custom domains, upload
public key) is in
[`specs/043-linux-package-repos/quickstart.md`](../specs/043-linux-package-repos/quickstart.md).

**Recovery matrix:**

| Symptom                                        | Likely cause                                 | Fix                                                                                                  |
| ---------------------------------------------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `gpg: signing failed: No secret key`           | `PACKAGES_GPG_KEY_ID` variable out of sync   | Reset with `gh variable set PACKAGES_GPG_KEY_ID --body "<FPR>"`                                      |
| `InvalidAccessKeyId` from S3 client            | R2 token rotated/expired                     | Mint new R2 token (scoped to both buckets), update both secrets                                      |
| `Release file is not valid`                    | Partial sync; bucket in mid-upload state     | Re-run the job. `publish.sh` is idempotent.                                                          |
| Smoke test fails on `apt-get install mcpproxy` | Metadata signed but propagated to edge slowly| Wait 5 min (Cloudflare cache) and re-run smoke test; if still failing, inspect R2 object listing     |
| Customers report "repository not signed" after rotation | Public key on R2 not refreshed      | Re-upload `mcpproxy.gpg` / `.gpg.asc` per quickstart step 5                                         |

**Key rotation (annual, or on suspected leak):**

Follow the script embedded at the top of `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt`
(steps also in the quickstart doc). After rotation, tag a patch release so the
next CI run republishes metadata signed with the new key, then announce the
rotation on the install page.

---

## SPOF 5 — Homebrew tap bump

**Goal:** `brew install smart-mcp-proxy/mcpproxy/mcpproxy` (formula) and
`brew install --cask smart-mcp-proxy/mcpproxy/mcpproxy` (cask, for the DMG
installer) resolve to the latest tag within minutes of a stable release.

**Where:** `release.yml` → `update-homebrew` job, `needs: release`. Fires only
on `startsWith(github.ref, 'refs/tags/v')`.

**External system:** tap repository
[`smart-mcp-proxy/homebrew-mcpproxy`](https://github.com/smart-mcp-proxy/homebrew-mcpproxy).
The job checks it out, downloads each platform tarball + installer DMG from the
fresh GitHub Release, calculates SHA256s, regenerates `Formula/mcpproxy.rb`,
patches `Casks/mcpproxy.rb` in place, commits, pushes.

**Required secrets:**

| Secret                 | Purpose                                               |
| ---------------------- | ----------------------------------------------------- |
| `HOMEBREW_TAP_TOKEN`   | Fine-scoped PAT with `contents:write` on the tap repo |

**Retention:** The tap carries only one version (Homebrew expects "latest"),
so no purge logic is needed.

**Common failures:**

- `curl -fsSL … -o mcpproxy-<version>-darwin-arm64.tar.gz` returns 404. GitHub
  Release assets propagate a few seconds after the release is published; the
  job already sleeps 15s and retries 5× with 10s backoff. If it still fails,
  asset upload from the `release` job is incomplete — inspect the earlier job
  first; re-running `update-homebrew` alone will succeed once assets are there.
- `git push` rejected: the token expired or someone force-pushed to the tap.
  Regenerate the PAT (`smart-mcp-proxy/homebrew-mcpproxy` → Settings → PATs →
  or org-level fine-scoped PAT), update `HOMEBREW_TAP_TOKEN`.
- Cask update silently skipped: the job skips the cask step if
  `Casks/mcpproxy.rb` is missing on the tap. Restore it from git history —
  the formula bump is not a replacement for the cask bump.

**Local sanity check:**

```bash
brew update
brew info smart-mcp-proxy/mcpproxy/mcpproxy       # expect new version
brew info --cask smart-mcp-proxy/mcpproxy/mcpproxy
brew install smart-mcp-proxy/mcpproxy/mcpproxy    # ensure SHAs resolve
```

---

## SPOF 6 — `next` branch hygiene (prerelease pipeline)

**Goal:** Every push to `next` produces a fully signed, notarized set of
prerelease artifacts (DMG, Windows installer, Linux tarballs) with versions
like `v0.24.0-next.5b63e2d`. This lets us dogfood sprint work **without**
tagging `main`.

**Where:** `prerelease.yml` — triggered on `push: branches: [next]`. Mirrors
the macOS signing + notarization and Windows signing legs of `release.yml` so
the stable release path is never the first place a signing regression lands.

**What `next` does and does NOT do:**

| Action                                          | `main` (`release.yml`) | `next` (`prerelease.yml`) |
| ----------------------------------------------- | ---------------------- | ------------------------- |
| Build all platform artifacts                    | ✅                     | ✅                        |
| Sign + notarize macOS DMG / PKG                 | ✅                     | ✅                        |
| Sign Windows installer via SignPath             | ✅                     | ✅                        |
| Publish GitHub Release                          | ✅                     | ❌ (artifacts only)       |
| Bump Homebrew tap                               | ✅                     | ❌                        |
| Publish to apt/rpm repos                        | ✅ (stable only)       | ❌                        |
| Deploy Docusaurus docs                          | ✅                     | ❌                        |
| Trigger marketing-site version bump             | ✅                     | ❌                        |
| Publish to the MCP Registry                     | ✅                     | ❌                        |

**Branching rules ([see `docs/releasing.md`](./releasing.md)):**

- Feature branches → PR into `next`.
- `next` accumulates the sprint until we cut a release from `main`.
- Hotfixes: branch from the last `vX.Y.Z` tag, land in `main`, tag
  `vX.Y.Z+1`, **and immediately merge the hotfix back into `next`**. Skipping
  the backport silently reintroduces the bug on the next release cut.
- `main` only receives hotfixes and vetted releases from `next`. Never push
  feature commits directly to `main`.

**Common regressions caught on `next` (keep it that way):**

- Unsigned helper binary in the Swift app bundle — surfaces as a
  notarization failure on prerelease, weeks before it would have broken a
  stable tag.
- SignPath reputation hiccups after a policy change — visible on
  prerelease EXEs.
- New macOS deployment target (we target `MACOSX_DEPLOYMENT_TARGET=13.0`);
  a bump here breaks codesign + notarization in subtle ways, and `next`
  catches it.

**When prerelease breaks but stable just shipped clean:**

The prerelease pipeline shares secrets with stable. If `next` fails while a
recent stable release succeeded, the delta is code — not infra:

1. `git log --oneline main..next` to see what landed since the last release.
2. Diff `prerelease.yml` vs `release.yml` — they should stay structurally
   similar; if one of them was edited in isolation, rebase the fix.
3. Do not merge `next` → `main` until the prerelease build is green.

**Weekly hygiene:**

- `git log --oneline main..next` should not grow silently beyond one sprint.
  If it does, cut a release or prune stale branches — stale `next` makes the
  eventual merge high-risk.
- Confirm the last prerelease build on GitHub Actions is green before tagging
  a stable release from `main`. A red `next` build is the earliest warning
  that a stable release will fail too.

---

## Tag-cutting cheat sheet

Bundled for reference; full detail in [`docs/releasing.md`](./releasing.md).

```bash
# From a clean main that matches next
git checkout main
git pull
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
# Watch https://github.com/smart-mcp-proxy/mcpproxy-go/actions
```

Expected green jobs on tag push:

1. `generate-notes` (Claude)
2. `build` (all platforms)
3. `sign-windows` (SignPath — **approve within 60 min**)
4. `release` (GitHub Release + DMGs + EXEs + tarballs)
5. `update-homebrew` (tap bump)
6. `publish-linux-repos` (apt + rpm to R2)
7. `deploy-docs` (Docusaurus → Cloudflare Pages, non-blocking)
8. `trigger-marketing-update` (non-blocking)
9. `mcp-registry` (non-blocking)

If any of 1–6 go red, consult the matching SPOF section above. 7–9 are marked
`continue-on-error: true` — they do not block the release but should be
back-filled the same week.
