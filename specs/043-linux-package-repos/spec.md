# Feature Specification: Linux Package Repositories (apt/yum)

**Feature Branch**: `043-linux-package-repos`
**Created**: 2026-04-21
**Status**: Draft
**Input**: User description: "Publish Linux .deb and .rpm packages to public apt/yum repositories hosted on Cloudflare R2 (apt.mcpproxy.app + rpm.mcpproxy.app). Signed with a new dedicated GPG key. Keep last 10 releases. Stable channel only. CI job in release.yml runs on v* tags. Update website install page, README, in-repo docs to use new repo-based install instead of manual .deb download. Backup GPG private key to ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt with usage instructions."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Linux user installs and upgrades MCPProxy via native package manager (Priority: P1)

A Debian/Ubuntu user adds a signed MCPProxy repository to their system once, runs `sudo apt install mcpproxy`, and thereafter receives updates automatically via `sudo apt upgrade` like any other system package. Same flow for Fedora/RHEL/Rocky/Alma users with `dnf install mcpproxy`.

**Why this priority**: This is the core value of the feature. Today users must manually download a `.deb` or `.rpm` on every release and install it via file path. This workflow removes that friction, puts MCPProxy on equal footing with any first-class Linux package, and hands update management to the OS.

**Independent Test**: In a clean `debian:stable` Docker container, copy-paste the 4 commands from the installation page, then verify `mcpproxy --version` reports the expected version. Repeat in `fedora:latest`. Delivers full value on its own — the entire feature can be judged green/red by this test alone.

**Acceptance Scenarios**:

1. **Given** a fresh Debian/Ubuntu system, **When** the user runs the documented 4-command install block, **Then** `mcpproxy` appears in PATH and reports the latest released version.
2. **Given** a Debian/Ubuntu system with MCPProxy already installed from the repo, **When** a new release is published and the user runs `sudo apt update && sudo apt upgrade`, **Then** MCPProxy is upgraded to the new version without any manual download.
3. **Given** a fresh Fedora/RHEL system, **When** the user runs the documented `dnf config-manager --add-repo` + `dnf install` commands, **Then** `mcpproxy` is installed and registered for future `dnf upgrade` runs.
4. **Given** a user who wants to verify trust, **When** they compare the fingerprint of `https://apt.mcpproxy.app/mcpproxy.gpg` against the fingerprint published on the install page, **Then** the two match exactly.

---

### User Story 2 - Maintainer ships a release without extra manual steps (Priority: P1)

When the maintainer pushes a `v*` tag, the existing GitHub Actions release workflow also publishes the new `.deb` and `.rpm` artifacts to the apt and yum repositories automatically, prunes older releases beyond the 10-release retention window, and republishes signed repo metadata. No human intervention is needed between tagging and end-users being able to `apt upgrade`.

**Why this priority**: If the publish step is not automated, release cadence drops and human error creeps in. Continuous availability is what makes the repo trustworthy for users running `apt upgrade` weekly.

**Independent Test**: Tag a test release on a scratch branch, watch the workflow complete, then install from the repo in a container. Covers both maintainer experience and end-to-end repo correctness.

**Acceptance Scenarios**:

1. **Given** a tagged release `vX.Y.Z`, **When** the release workflow completes, **Then** both `apt.mcpproxy.app` and `rpm.mcpproxy.app` serve `X.Y.Z` as the installable version, and the workflow's smoke-test step confirms install-from-repo succeeds.
2. **Given** that 10 releases already exist in the repo, **When** release 11 is published, **Then** the oldest release's artifacts are removed from the repository and repo metadata no longer references them.
3. **Given** a transient failure during upload (e.g., R2 request throttled), **When** the workflow is re-run, **Then** it resumes cleanly and converges to the correct final state without manual cleanup.

---

### User Story 3 - Maintainer rotates the signing key in an emergency (Priority: P2)

When the signing key is lost, suspected compromised, or needs routine rotation (e.g., five-year expiry approaching), the maintainer can generate a new key, update one GitHub Actions secret, commit the new public key to the repository, tag a new release, and have the CI automatically distribute the new key to users via the repository.

**Why this priority**: Trust infrastructure must survive key lifecycle events. Without a documented rotation path, a lost key makes the whole repo unusable. Ranked below P1 because it is an infrequent operation but essential to have designed.

**Independent Test**: Perform a dry-run rotation on a test key in a sandbox R2 bucket — generate a fresh key, follow the rotation steps, and verify that a fresh install container picks up the new key fingerprint and validates packages signed with the new key.

**Acceptance Scenarios**:

1. **Given** the documented rotation procedure and the local backup file, **When** the maintainer follows the procedure, **Then** the next published release is signed with the new key and the public key at the repository URL matches the new fingerprint.
2. **Given** a compromised key that has been revoked, **When** an attacker attempts to sign fake metadata, **Then** clients fail the signature check because the stored GitHub Actions secret no longer grants access.

---

### User Story 4 - Visitor arriving at the install page picks a path in under 30 seconds (Priority: P2)

A developer lands on the installation page, sees their distro (Debian/Ubuntu or Fedora/RHEL) at the top of the Linux section, copies a handful of commands, and runs them. They do not have to read about DMGs, installers, build-from-source, or reason about which `.deb` filename to pick.

**Why this priority**: The new install method only realizes its value when users can find it. Ranked P2 because the technical plumbing (P1) is prerequisite — but the doc update is not optional if we want uptake.

**Independent Test**: Show the updated installation page to a Linux developer unfamiliar with the project, time how long until they have a plausible install command in their terminal. Target: under 30 seconds.

**Acceptance Scenarios**:

1. **Given** the updated installation page, **When** a first-time Linux visitor reads it, **Then** the apt/dnf repo snippets are the primary, most-visible Linux install path.
2. **Given** existing direct-download users, **When** they visit the page, **Then** the `.deb` and `.rpm` direct-download links are still available as a documented fallback for air-gapped or offline installs.
3. **Given** a user who wants to know how the repo works, **When** they follow a link from the install page, **Then** they reach a doc explaining signing, retention window, and verification steps.

---

### Edge Cases

- **Empty repository (first-ever run)**: The publish step must succeed on a completely empty R2 bucket with no pre-existing metadata, pool, or state.
- **Re-running the same release**: Re-running the workflow for the same tag (e.g., after manual retry) must be idempotent and not corrupt existing state.
- **Partial upload interruption**: If the upload step is interrupted between uploading new artifacts and removing pruned ones, a user running `apt update` at that moment must either get the old stable state or the new stable state — never a stale manifest referencing a deleted artifact. The published order and sync semantics must minimize the broken-state window.
- **Repo metadata served stale via CDN**: If a CDN edge caches old metadata, users see outdated versions. The publish step must either invalidate cache or rely on cache TTLs short enough that staleness clears within one hour.
- **GPG key near expiry**: Publishing must warn (not fail) when the signing key is within 60 days of expiry, reminding the maintainer to rotate.
- **User running `apt-get` on an unsupported architecture** (e.g., ppc64le, s390x): The repo advertises only the supported architectures (amd64, arm64 for deb; x86_64, aarch64 for rpm). Other architectures fail cleanly with "not found", not with a corrupt metadata error.
- **User downgrade scenario**: A user wanting to pin an older version can pass `apt install mcpproxy=X.Y.Z`, provided that version is still within the 10-release retention window. Older versions are unavailable from the repo but remain downloadable from GitHub Releases.
- **Version with pre-release suffix** (e.g., `1.0.0-rc1`): Out of scope for this feature (stable channel only). If such a tag is ever processed, the publish job must skip it cleanly rather than corrupt the repo.

## Requirements *(mandatory)*

### Functional Requirements

**Repository content and format**
- **FR-001**: System MUST publish signed apt repository metadata at `https://apt.mcpproxy.app/` conforming to the standard Debian archive layout, covering the `stable` suite and `main` component for architectures `amd64` and `arm64`.
- **FR-002**: System MUST publish signed yum/dnf repository metadata at `https://rpm.mcpproxy.app/` conforming to the standard RPM repository layout, with separate trees for architectures `x86_64` and `aarch64`.
- **FR-003**: System MUST serve the public signing key at `https://apt.mcpproxy.app/mcpproxy.gpg` and `https://rpm.mcpproxy.app/mcpproxy.gpg`, in a format suitable for direct consumption by `apt` (binary/armored GPG) and `dnf` (ASCII-armored).
- **FR-004**: System MUST publish a ready-to-use `.repo` definition file at `https://rpm.mcpproxy.app/mcpproxy.repo` so that users can set up the yum/dnf source with a single `dnf config-manager --add-repo` command.

**Signing and trust**
- **FR-005**: System MUST sign repository metadata (Debian `Release`, YUM `repomd.xml`) using a dedicated GPG signing key that is distinct from any other project keys.
- **FR-006**: System MUST support generating `InRelease` (inline-signed) and detached `Release.gpg` for the apt repo, as modern apt clients expect both.
- **FR-007**: System MUST keep the signing key private: the private key is stored only as a GitHub Actions encrypted secret and in a local backup file at `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` on the maintainer's workstation, outside any git repository.
- **FR-008**: System MUST ship the backup file with inline documentation covering: key metadata (fingerprint, creation date, expiry date, UID), import commands, re-upload commands for rotating the GitHub Actions secret, and the full rotation procedure.

**Release lifecycle and automation**
- **FR-009**: System MUST trigger repository publication automatically on every `v*` tag push, as a new job within the existing `release.yml` workflow, gated by successful `.deb` and `.rpm` artifact builds.
- **FR-010**: System MUST retain at most 10 versions per repository at any time. On publication of the 11th release, the oldest release's artifacts and metadata references MUST be removed.
- **FR-011**: System MUST be stateless in the sense that the publish job reconstructs repository metadata from whatever artifacts are present in the bucket at publish time, without depending on any out-of-band database, lockfile, or cached state.
- **FR-012**: System MUST be idempotent — re-running the publish job for the same tag produces the same final repository state.
- **FR-013**: System MUST fail the CI job (not just warn) if signing, upload, or metadata generation fails, preventing half-updated repositories from reaching users.
- **FR-014**: System MUST include an automated post-publish smoke test: in clean Debian and Fedora containers, the publish job performs the documented install and asserts that `mcpproxy --version` returns the expected version.

**User-facing documentation**
- **FR-015**: Website installation page MUST present the apt and dnf repository setup instructions as the primary Linux install path, above the direct-download links.
- **FR-016**: Website installation page MUST display the signing key fingerprint and link to the public key URL so users can verify trust.
- **FR-017**: Website installation page MUST retain direct-download links to `.deb` and `.rpm` files as a documented fallback (e.g., for air-gapped installs), without requiring them to be the default.
- **FR-018**: Project README MUST use the same apt/dnf repo instructions as the website, with a link to the website for additional detail.
- **FR-019**: In-repo documentation (getting-started/installation) MUST mirror the README changes so that the Docusaurus-rendered docs stay in sync.
- **FR-020**: A dedicated documentation page MUST describe: how the repos are hosted, the 10-release retention window, how to pin a specific older version while it is still retained, how to mirror the repo for air-gapped setups, and troubleshooting for common failure modes.
- **FR-021**: An internal operations document MUST describe: bucket configuration, custom domain binding, GPG key rotation procedure, how to manually republish a release, and how to purge a bad release from the repos.

**Operations and resilience**
- **FR-022**: System MUST reject publishing releases with pre-release tags (e.g., `rc1`, `beta`, `alpha`) since the stable channel only accepts stable versions; such tags skip the publish job without error.
- **FR-023**: System MUST warn maintainers when the signing key is within 60 days of expiry by emitting a non-fatal warning annotation in the workflow run.
- **FR-024**: System MUST use only existing Cloudflare infrastructure (R2 buckets + custom domains on the already-registered `mcpproxy.app` domain) and the existing GitHub Actions workflow pipeline — no new paid services.

### Key Entities *(include if feature involves data)*

- **Package Artifact**: A built, signed `.deb` or `.rpm` file produced by the release workflow. Attributes: architecture, version, SHA-256 checksum, file size. Lives in the repository's pool area and is referenced by the repository metadata.
- **Repository Metadata**: The signed index files describing what artifacts exist and their checksums. For apt: `Release`, `Release.gpg`, `InRelease`, `Packages`, `Packages.gz`. For yum: `repomd.xml`, `repomd.xml.asc`, primary/filelists/other XML files. Regenerated on every publish.
- **Signing Key**: A single GPG keypair dedicated to this purpose. Attributes: fingerprint, UID, creation date, expiry date. Private half stored in GitHub Actions secret and in the maintainer's local backup file. Public half embedded in the repo tree and referenced from the install page.
- **Repository Channel**: For this feature, only `stable` exists. Future-friendly name so that a `prerelease` channel can be added later without restructuring URLs.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A Linux user on a supported distribution can install MCPProxy in under 1 minute from landing on the install page, copy-pasting four or fewer commands.
- **SC-002**: Installed users receive new releases via the normal OS update command (`apt upgrade`, `dnf upgrade`) within 1 hour of the corresponding `v*` tag being pushed, with no human intervention between tag push and user availability.
- **SC-003**: On every `v*` tag, the automated publish succeeds on the first CI run at least 95% of the time; manual intervention needed no more than once per quarter.
- **SC-004**: The repositories remain within the 10-release retention window at all times; inspection of the pool area shows at most 10 versions of each architecture.
- **SC-005**: Direct-download usage of `.deb`/`.rpm` from GitHub Releases drops by at least 50% within 90 days of the repos going live (indicating users migrated to the repo-based flow).
- **SC-006**: A key rotation can be completed by the maintainer end-to-end in under 30 minutes, following only the documented procedure, without reading the implementation source.
- **SC-007**: The apt/dnf repositories stay publicly accessible with 99.5% uptime or better, inherited from Cloudflare R2's SLA for the custom-domain endpoints.
- **SC-008**: Zero incidents within the first 90 days where a user's `apt update` or `dnf makecache` fails due to malformed or unsigned metadata published by the CI pipeline.

## Assumptions

- Cloudflare R2 is available on the account with sufficient quota for ~200 MB of storage per repo (10 releases × ~66 MB per release, well within the free tier).
- Cloudflare R2 supports custom-domain public access on `apt.mcpproxy.app` and `rpm.mcpproxy.app` subdomains via the standard R2 → custom domain binding flow.
- The `mcpproxy.app` zone is managed in Cloudflare and the maintainer has sufficient permissions to add the two subdomain DNS records.
- The existing `release.yml` workflow already produces 4 package artifacts per release (`.deb` amd64, `.deb` arm64, `.rpm` x86_64, `.rpm` aarch64). No changes to the build job are required.
- The maintainer has `wrangler` CLI configured and authenticated against the correct Cloudflare account.
- GitHub Actions runners have network access to R2 over the S3-compatible endpoint.
- The stable channel is the only channel needed for the foreseeable future. If a prerelease channel is added later, it will use a separate suite name (`prerelease`) and not require URL or bucket restructuring.
- Users are on distributions that ship a recent-enough `apt` (>= 1.1) or `dnf` (>= 4.x) to support modern signed metadata and `signed-by` keyring syntax.
- The new GPG key is single-use (repo signing only). It does not replace or interact with any existing code-signing key used for Windows installers or macOS binaries.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` — Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` — These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(release): publish linux apt/yum repos to Cloudflare R2

Related #<issue>

Add publish-linux-repos job to release.yml that pushes signed apt and
yum repository metadata to apt.mcpproxy.app and rpm.mcpproxy.app on
every v* tag, with a 10-release retention window.

## Changes
- New CI job in .github/workflows/release.yml
- contrib/linux-repos/ helper scripts and apt-ftparchive config
- New public signing key at contrib/signing/mcpproxy-packages.asc
- Website, README, and in-repo docs updated to lead with apt/dnf install
- New docs/features/linux-package-repos.md user guide
- New docs/operations/linux-package-repos-infrastructure.md ops guide

## Testing
- Smoke test in CI using debian:stable and fedora:latest containers
- Manual verification: install in a fresh Docker container, apt upgrade
  after a second test tag, key-fingerprint match against published doc
```
