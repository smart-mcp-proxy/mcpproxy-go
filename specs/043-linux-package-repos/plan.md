# Implementation Plan: Linux Package Repositories (apt/yum)

**Branch**: `043-linux-package-repos` | **Date**: 2026-04-21 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/043-linux-package-repos/spec.md`

## Summary

Publish signed apt and yum repositories for MCPProxy on Cloudflare R2, reachable at `apt.mcpproxy.app` and `rpm.mcpproxy.app`. Each `v*` tag triggers a new CI job in `release.yml` that downloads the freshly-built `.deb` / `.rpm` artifacts, syncs the current bucket state down, prunes releases older than the last 10, signs metadata with a dedicated GPG key, and syncs back up. A smoke test in the same job installs from the new repo inside Debian and Fedora containers. User-facing docs (website install page, README, in-repo guide) are rewritten to lead with `apt install mcpproxy` and `dnf install mcpproxy`, relegating direct `.deb`/`.rpm` downloads to a fallback. The GPG private key is backed up in plain text at `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` (outside any git repo) with inline usage instructions.

## Technical Context

**Language/Version**: Bash / GitHub Actions YAML for the CI job; Astro 4.x for the website; Markdown for docs. No Go code changes required.
**Primary Dependencies**:
- `apt-ftparchive` (stateless Debian archive metadata generation — part of the `apt-utils` Debian package; pre-installed on ubuntu-latest runners)
- `createrepo_c` (yum repo metadata — available in `dnf` / Ubuntu `createrepo-c` package, installable via apt)
- `gpg` (signing; pre-installed on ubuntu-latest)
- `aws` CLI (S3-compatible sync against the Cloudflare R2 endpoint — batch upload/delete in one call, faster than `wrangler r2 object put` for many files)
- `wrangler` (used only for one-off bucket / custom-domain management during initial setup; the CI workflow uses the `aws` CLI for bulk ops because it supports `s3 sync`)
- Docker (for the in-CI smoke-test containers: `debian:stable-slim`, `fedora:latest`)

**Storage**:
- Cloudflare R2 bucket `mcpproxy-apt` bound to custom domain `apt.mcpproxy.app`
- Cloudflare R2 bucket `mcpproxy-rpm` bound to custom domain `rpm.mcpproxy.app`
- No database, no cache server, no stateful runner infrastructure.

**Testing**:
- CI smoke tests: run `apt install mcpproxy` in `debian:stable-slim` and `dnf install mcpproxy` in `fedora:latest` against the freshly-published repos; assert `mcpproxy --version` returns the tag.
- Local dry-run: a `scripts/publish-linux-repos.sh` wrapper supports a `--dry-run` flag that generates metadata locally without uploading, for development iteration.

**Target Platform**:
- Consumer targets: Debian 11+ / Ubuntu 20.04+ (amd64, arm64), Fedora 36+ / RHEL 8+ / Rocky 8+ / AlmaLinux 8+ (x86_64, aarch64).
- Runner target: GitHub Actions `ubuntu-latest`.

**Project Type**: Infrastructure / release automation. Touches `.github/workflows/release.yml`, new `contrib/linux-repos/` helper directory, new `contrib/signing/` for the public key, website + docs edits. No changes to Go source tree.

**Performance Goals**:
- Publish job wall-clock time < 4 minutes for a typical release (~66 MB of artifacts).
- Users receive new versions within 1 hour of `v*` tag push (SC-002), dominated by how soon they run `apt update`.
- Smoke-test step < 2 minutes.

**Constraints**:
- No paid services — use existing Cloudflare R2 account (free tier: 10 GB storage, 1 M Class A req/mo, 10 M Class B req/mo; well within quota).
- No new long-lived credentials beyond the five GitHub Actions secrets enumerated below.
- GPG private key never stored in the repository.
- Never leave the repos in a state where the signed manifest references a file that no longer exists in the pool.

**Scale/Scope**:
- At most 10 versions × 2 architectures × 2 package formats = 40 artifacts in the buckets at any time (~200 MB).
- One publish run per `v*` tag (historically ~1–3 releases per week).
- Consumer install volume: projected hundreds to low thousands per month in year 1.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

This feature is packaging / release automation and does not touch the Go server or tray code paths, so most of the application-focused principles do not apply. The relevant principles:

| Principle | Applies? | How this plan honors it |
|---|---|---|
| **I. Performance at Scale** | No — server-side performance is unaffected. | N/A |
| **II. Actor-Based Concurrency** | No — no new Go concurrency. | N/A |
| **III. Configuration-Driven Architecture** | Indirect — the publish job is parameterized via GitHub Actions secrets and environment variables, not hard-coded paths. | All bucket names, domains, and key IDs live in the workflow YAML as `env:` or repo/org variables, so rewiring (e.g., for a future prerelease channel) is config-only. |
| **IV. Security by Default** | Yes — trust infrastructure. | A dedicated GPG signing key is generated for this purpose only. Private half lives only as a GitHub Actions secret and in the maintainer's local backup file (outside any git repo). Repo metadata is always signed. Smoke-test in CI verifies signature-checked install works before a release is declared green. |
| **V. Test-Driven Development** | Partial — infra code has no unit tests, but is validated by a smoke test. | The CI job ends with a containerized install test in Debian and Fedora. If the smoke test fails, the workflow fails, blocking the release status from being marked successful. Dry-run mode (`--dry-run`) in the helper script lets developers verify changes locally before pushing. |
| **VI. Documentation Hygiene** | Yes. | Plan delivers four doc surfaces updated simultaneously: website install page, README, `docs/getting-started/installation.md`, and a new `docs/features/linux-package-repos.md`, plus an operations doc at `docs/operations/linux-package-repos-infrastructure.md`. The spec's FR-015 through FR-021 enforce this. |

**Gate verdict**: PASS — no constitution violations. No entries needed in the Complexity Tracking table.

## Project Structure

### Documentation (this feature)

```text
specs/043-linux-package-repos/
├── plan.md              # This file
├── research.md          # Phase 0 — tooling choice rationale + edge-case analysis
├── data-model.md        # Phase 1 — bucket layout, entity definitions
├── quickstart.md        # Phase 1 — initial setup and first publish walkthrough
├── contracts/           # Phase 1 — workflow job contract, R2 object-key contract
│   ├── publish-linux-repos.job.md
│   ├── apt-bucket-layout.md
│   └── rpm-bucket-layout.md
├── checklists/
│   └── requirements.md  # From /speckit.specify
└── tasks.md             # Phase 2 — generated by /speckit.tasks
```

### Source Code (repository root)

This feature adds new infrastructure and helper files rather than modifying the Go source tree. Paths that change or are added:

```text
mcpproxy-go/
├── .github/
│   └── workflows/
│       └── release.yml                         # MODIFIED: add publish-linux-repos job + smoke-test step
├── contrib/
│   ├── linux-repos/                            # NEW: helper scripts for CI + local dry-run
│   │   ├── publish.sh                          # NEW: top-level orchestrator (called from CI)
│   │   ├── apt-publish.sh                      # NEW: sync-down → prune → add → apt-ftparchive → sign → sync-up
│   │   ├── rpm-publish.sh                      # NEW: sync-down → prune → add → createrepo_c → sign → sync-up
│   │   ├── apt-ftparchive.conf                 # NEW: archive config (suite, components, arches, description)
│   │   ├── mcpproxy.repo.template              # NEW: dnf .repo file template (uploaded to rpm bucket)
│   │   ├── smoke-test-debian.sh                # NEW: container-based install test
│   │   └── smoke-test-fedora.sh                # NEW: container-based install test
│   └── signing/
│       └── mcpproxy-packages.asc               # NEW: public signing key (committed)
├── docs/
│   ├── getting-started/
│   │   └── installation.md                     # MODIFIED: lead with apt/dnf repo install
│   ├── features/
│   │   └── linux-package-repos.md              # NEW: user-facing feature guide (retention, pinning, mirror)
│   └── operations/
│       └── linux-package-repos-infrastructure.md # NEW: ops runbook (rotation, manual republish, purge)
└── README.md                                   # MODIFIED: update Linux install block

mcpproxy.app-website/
└── src/
    └── pages/
        └── docs/
            └── installation.astro              # MODIFIED: lead with apt/dnf repo blocks above direct downloads

# NOT in any repository (local-only):
~/repos/PACKAGES_GPG_PRIVATE_KEY.txt            # NEW: GPG private key backup + instructions
```

**Structure Decision**: Release automation, not Go code. Scripts live in `contrib/linux-repos/` because they are ancillary tooling (not compiled into the binary) and follow the existing pattern used by `contrib/` in many Go projects. The public signing key lives in `contrib/signing/` so it is versioned with the code but visibly separate from build artifacts. Docs follow the existing layout (`docs/getting-started/`, `docs/features/`, `docs/operations/`). Website changes touch the one install page in the separate `mcpproxy.app-website` repo.

## Complexity Tracking

None. Constitution gate passes without justified violations.
