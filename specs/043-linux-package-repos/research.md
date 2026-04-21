# Phase 0 Research — Linux Package Repositories

This document records the decisions made during brainstorming and backfills rationale / alternatives considered.

## Decision 1 — Storage backend

**Decision**: Cloudflare R2 with S3-compatible API, one bucket per package format, bound to custom domains.

**Rationale**:
- Cloudflare is already the DNS/CDN provider for `mcpproxy.app`, reducing setup friction; wrangler is already authenticated.
- R2 has zero egress cost, which matters when the same `Packages` file is fetched on every user's `apt update`.
- The free tier (10 GB storage, 1 M Class A + 10 M Class B requests/month) comfortably fits the projected ~200 MB and projected usage.
- Standard apt/yum pool layout maps naturally onto S3-style object keys.

**Alternatives considered**:
- **GitHub Releases only**: artifacts are already uploaded there, but apt's `Packages` file requires the `Filename:` path to be relative to the repo base URL, and GitHub Releases URLs are versioned (`/releases/download/vX.Y.Z/...`), which doesn't fit the pool layout. Would require a Cloudflare Worker redirect layer — extra moving parts for no real benefit over just storing on R2.
- **Cloudflare Pages**: 25 MiB per-file limit — artifacts are ~15–17 MB so they fit today, but Pages asset deploys are tied to site builds, coupling the website deploy cadence to the package release cadence. Bad coupling.
- **Packagecloud.io / Cloudsmith / Fury.io**: hosted services that would Just Work™, but all are paid/freemium with limits that are awkward at scale, and they reintroduce the very thing R2 lets us avoid (vendor for a commodity service that we can self-serve on infra we already pay for).

## Decision 2 — Signing key lifecycle

**Decision**: New dedicated GPG key, RSA 4096, 5-year expiry, UID `MCPProxy Packages <mcpproxy-packages@mcpproxy.app>`.

**Rationale**:
- Separating concerns: the repo signing key should be rotatable independently of code-signing, notarization, or git commit signing keys.
- 4096-bit RSA is the conservative modern recommendation for package-signing use (slower than Ed25519 but universally supported by `apt` and `dnf` across the target distribution matrix).
- 5-year expiry is long enough that rotations are rare events, short enough to force renewal discipline and limit blast radius if the private key is ever leaked undetected.

**Alternatives considered**:
- **Ed25519 key**: faster, shorter keys, but older `apt` versions on LTS distributions still have inconsistent Ed25519 support. Not worth the compatibility risk for a release-automation feature.
- **Reuse an existing project key**: would conflate this key's lifecycle with others, so a compromise or rotation would ripple across unrelated systems.
- **No expiry**: discouraged by best practice; provides no rotation pressure.

**Storage**:
- Private key: GitHub Actions encrypted secret `PACKAGES_GPG_PRIVATE_KEY` + passphrase `PACKAGES_GPG_PASSPHRASE`.
- Local backup: `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` (outside any git repository).
- Public key: committed at `contrib/signing/mcpproxy-packages.asc` and mirrored to both buckets at `/mcpproxy.gpg`.

## Decision 3 — Metadata generation tool (Debian)

**Decision**: `apt-ftparchive`, stateless run from `pool/`.

**Rationale**:
- Stateless — no persistent database to maintain or restore between CI runs, so the job is safely re-runnable.
- Part of the `apt-utils` package; pre-installed on `ubuntu-latest` runners.
- Well-documented, widely-used tool; the generated `Packages`, `Packages.gz`, and `Release` files are the exact format apt expects.

**Alternatives considered**:
- **reprepro**: nicer single-command UX, but maintains a local Berkeley DB of state. Restoring that DB across CI runs (either by syncing it down from R2, or by rebuilding from scratch) adds a failure mode for no real benefit on a small single-suite repo.
- **aptly**: similar to reprepro, also stateful; additionally has heavier runtime dependencies.

## Decision 4 — Metadata generation tool (RPM)

**Decision**: `createrepo_c` per-architecture directory, stateless run from the directory containing the `.rpm` files.

**Rationale**:
- Standard tool; generates `repodata/repomd.xml` plus the primary/filelists/other XML files.
- Available on ubuntu-latest via `apt install createrepo-c` (the C rewrite; significantly faster than the original Python `createrepo`).
- Stateless scan matches our design principle.

**Alternatives considered**:
- **createrepo (original Python)**: slower; no behavioral reason to prefer it.

## Decision 5 — R2 I/O tooling in CI

**Decision**: `aws s3 sync` / `aws s3 cp` against the R2 S3-compatible endpoint for bulk operations in the publish job. `wrangler` reserved for one-off bucket management during initial setup.

**Rationale**:
- `aws s3 sync --delete` does the right thing in one pass: upload new files, delete removed ones, skip unchanged. Exactly what we want for a repo refresh.
- `wrangler r2 object put/delete` is per-object and slow at scale; 40 artifacts × multiple metadata files would make for a chatty workflow.
- AWS CLI v2 is pre-installed on ubuntu-latest runners.

**Alternatives considered**:
- **wrangler r2 object put in a loop**: works, but slower and more code in the workflow for bulk ops. Fine for manual single-file uploads, so we keep it for occasional operator use.
- **rclone**: another capable option, but requires installing and configuring a new tool on the runner. Zero benefit over AWS CLI for this case.

## Decision 6 — Retention policy

**Decision**: Keep last 10 versions per repo. Prune older artifacts on every publish run.

**Rationale**:
- Fits comfortably within the R2 free tier (~200 MB at 10 releases × 2 arches × 2 formats × ~16 MB each).
- Users who need an older version can still fetch the `.deb`/`.rpm` from the corresponding GitHub Release — older versions are not deleted from GitHub, only from the apt/yum repo.
- 10 versions covers a reasonable rollback window (historically, mcpproxy ships 1–3 releases/week, so ~1–10 weeks of history).

**Alternatives considered**:
- **Keep all versions forever**: storage grows unbounded, makes historical debugging easier but blows past the free tier over time.
- **Keep last 3**: too aggressive; a user pinning an older version has minutes, not days, to fetch it before it's pruned.

## Decision 7 — Channels

**Decision**: Stable channel only for this feature. Reserve the `prerelease` suite name for future work.

**Rationale**:
- Matches the spec's explicit scope decision.
- Using `stable` as the suite name now (rather than leaving the suite unnamed or calling it `main`) means a future `prerelease` suite can be added by creating parallel `dists/prerelease/...` trees without changing the URL structure or migrating existing users.

**Alternatives considered**:
- **Ship both channels now**: doubles CI time and bucket size for speculative future demand; not justified until we see the ask.

## Decision 8 — Atomic publish order

**Decision**: Publish sequence is: (1) upload new `.deb`/`.rpm` artifacts to pool → (2) regenerate metadata locally → (3) upload metadata → (4) delete pruned pool artifacts.

**Rationale**:
- Clients that `apt update` between steps 1 and 2 see the old `Packages` file → they install the old version (safe). A client that lands mid-way through step 3 could see a partial metadata tree, but `aws s3 sync` uploads files individually and the `Release`/`InRelease` files are signed and uploaded last, so an unsigned-partial-upload is invalidated by the missing signature file until the final upload completes.
- The delete step (4) runs last so that if it's interrupted, the only consequence is that pruned artifacts temporarily linger — harmless; they're re-pruned on the next run.

**Alternatives considered**:
- **Two-phase commit with a staging bucket**: over-engineering for this scale.
- **Single atomic S3-like multipart operation**: not supported by R2 across a multi-file transaction.

## Decision 9 — Smoke testing

**Decision**: Containerized install test in `debian:stable-slim` and `fedora:latest` at the end of the publish job; fail the workflow if either install fails.

**Rationale**:
- Catches real integration problems (signature mismatch, wrong architecture listed, missing `Packages.gz`) before users do.
- Ubuntu-latest runners have Docker available, no extra setup needed.
- Adds ~1–2 min to the job but is the best-possible signal that the published repo is usable.

**Alternatives considered**:
- **Just validate signatures locally**: catches trivial breakage but misses things like wrong URL paths, CDN caching quirks, and distribution-specific trust-store interactions.
- **Run smoke tests in a separate workflow**: would give faster publish feedback but decouple "publish succeeded" from "publish is actually usable." Better to keep them tied.

## Decision 10 — First-time infrastructure setup path

**Decision**: Maintainer runs a one-time bootstrap process that creates the R2 buckets, binds the custom domains, and registers all GitHub Actions secrets. Automated where wrangler CLI supports it; the custom domain binding step falls back to the Cloudflare dashboard (via Chrome extension) because wrangler's `r2 bucket domain` command is gated behind an interactive TTY on some bucket types.

**Rationale**:
- Bootstrap is a one-time operation; the complexity of fully automating a workflow that runs once doesn't pay back.
- wrangler CLI handles: bucket creation, public-access toggle (where supported), and object upload.
- Cloudflare dashboard handles: custom-domain binding with zone validation. This is the one step that requires the dashboard UI today; documented in the quickstart.

**Alternatives considered**:
- **Fully Terraform-managed infrastructure**: worth it for a team-scale project with many buckets to manage; overkill for two buckets created once.
