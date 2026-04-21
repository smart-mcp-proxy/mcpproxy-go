# Execution Log — Feature 043 Linux Package Repositories

Running per CLAUDE.md Autonomous Operation Constraints. Logging every completed step.

## Branch
`043-linux-package-repos`

## Tool verification (start)
- gh: OK (logged in as Dumbris, repo/workflow scopes)
- wrangler: OK (account `d2fa289033a2f6f28c550834d0fe43c5`, a.dumbris@gmail.com)
- gpg: 2.4.9 OK
- aws CLI: NOT installed locally — fine for CI (ubuntu-latest has it pre-installed); for local one-time uploads we use `wrangler r2 object put`.

## Phase 1: Setup (Shared Infrastructure)
Status: in_progress

### T001 — Generate GPG signing key — DONE
- Fingerprint: `3B6FA1AD5D5359DA51F18DDCE1B59B9BA1CB8A3B`
- UID: `MCPProxy Packages (Linux repository signing key) <mcpproxy-packages@mcpproxy.app>`
- Created: 2026-04-21
- Expires: 2031-04-21
- Keys stored in user's GnuPG keyring (`~/.gnupg/`). Batch file shredded.

### T002 — Export public key — DONE
- Written to `/Users/user/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc` (3216 bytes)

### T003 — Write backup file — DONE
- Path: `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` (outside any git repo, 0600)
- Contains: metadata header, passphrase (flagged for user to move to 1Password), full usage/rotation instructions, ASCII-armored private key.
- Size: 8472 bytes, 141 lines.

### T004-T015 — R2 and credentials — DONE
- R2 subscription activated (user-authorized click).
- Buckets `mcpproxy-apt` and `mcpproxy-rpm` created in EEUR region.
- Custom domains `apt.mcpproxy.app` + `rpm.mcpproxy.app` bound, both Active + Enabled.
- R2 API token "MCPProxy Packages CI" created, Object Read&Write, scoped to both buckets.
- 5 GitHub Actions secrets + 1 variable registered.
- Public signing key uploaded to both buckets (note: needed `--remote` flag on wrangler).
- HTTPS fetch of public key verified, fingerprint `3B6F A1AD 5D53 59DA 51F1 8DDC E1B5 9B9B A1CB 8A3B` matches.

## Phase 2: Foundational — DONE
Helper scripts and config files created under `contrib/linux-repos/`.

## Phase 3: US2 — Publish automation — DONE
- `apt-publish.sh`, `rpm-publish.sh`, `publish.sh` written.
- Smoke tests `smoke-test-debian.sh` + `smoke-test-fedora.sh` written.
- `publish-linux-repos` job added to `.github/workflows/release.yml`.

Bugs found and fixed during local e2e test:
1. `wrangler r2 object put` defaulted to local storage — must use `--remote`. (Only affected initial setup, not CI.)
2. `import-key.sh` writing `GNUPGHOME=...` to `$GITHUB_ENV` doesn't help in Docker/local runs. Refactored to export a stable `GNUPGHOME` before invoking.
3. AWS CLI v2.23+ sends CRC32 checksums by default → R2 `SignatureDoesNotMatch`. Added `AWS_REQUEST_CHECKSUM_CALCULATION=when_required` and `AWS_RESPONSE_CHECKSUM_VALIDATION=when_required` to publish.sh.
4. RPM packages lacked embedded GPG signatures, failing `dnf install` with `gpgcheck=1`. Added `rpmsign --addsign` step to rpm-publish.sh (requires `rpm` package in CI image).
5. Cache TTL of 300s on metadata produced hash-mismatch windows across releases. Shortened to 60s + `must-revalidate`.

## Phase 4: US1 verification — DONE
- debian:stable-slim `apt install mcpproxy` → 0.24.6 installed successfully.
- fedora:latest `dnf install mcpproxy` → 0.24.6 installed successfully.
- GPG key imported from `https://rpm.mcpproxy.app/mcpproxy.gpg`, fingerprint verified.

## Phase 5: Docs — DONE
- Website `installation.astro` updated with apt + dnf sections.
- README.md Linux install replaced with repo-based install.
- `docs/getting-started/installation.md` updated.
- `docs/features/linux-package-repos.md` created.

## Phase 6: Ops runbook — DONE
- `docs/operations/linux-package-repos-infrastructure.md` created with rotation, manual republish, purge procedures.

## Phase 7: Polish — in_progress
- bash -n passes on all scripts.
- Local e2e smoke test passes (Debian + Fedora).
- Remaining: commit fixes, push branch, open PR, let user review.
