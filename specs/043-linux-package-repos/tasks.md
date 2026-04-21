---

description: "Task list for Linux Package Repositories (apt/yum) — feature 043"
---

# Tasks: Linux Package Repositories (apt/yum)

**Input**: Design documents from `/specs/043-linux-package-repos/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Smoke tests in CI containers are part of the feature's MVP (FR-014). No separate unit-test phase — the scripts are thin wrappers over well-tested external tools (`apt-ftparchive`, `createrepo_c`), so the smoke test is the authoritative check.

**Organization**: Grouped by user story so the publish infrastructure (US1+US2) can ship and be validated before doc surfaces (US4) and key-rotation polish (US3).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Maps to user stories from spec.md (US1, US2, US3, US4)

## Path Conventions

- Shell scripts: `contrib/linux-repos/*.sh`
- Public GPG key: `contrib/signing/mcpproxy-packages.asc`
- CI job: `.github/workflows/release.yml`
- User docs in mcpproxy-go: `docs/getting-started/installation.md`, `docs/features/linux-package-repos.md`, `docs/operations/linux-package-repos-infrastructure.md`
- Website: `~/repos/mcpproxy.app-website/src/pages/docs/installation.astro`
- Local-only backup (outside repos): `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Generate the signing key, create R2 buckets, bind custom domains, and register GitHub Actions secrets. All one-time operations.

- [ ] T001 Generate GPG signing key (RSA 4096, 5-year expiry, UID `MCPProxy Packages <mcpproxy-packages@mcpproxy.app>`) using the batch-file procedure in `specs/043-linux-package-repos/quickstart.md` Step 1. Record the fingerprint.
- [ ] T002 Export ASCII-armored public key to `/Users/user/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc`. Commit this file to the `043-linux-package-repos` branch.
- [ ] T003 Write the GPG private-key backup to `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` (outside any git repo) using the exact template in `specs/043-linux-package-repos/quickstart.md` Step 2 (metadata header + instructions + armored key). `chmod 600` the file.
- [ ] T004 [P] Create Cloudflare R2 bucket `mcpproxy-apt` via `wrangler r2 bucket create mcpproxy-apt`.
- [ ] T005 [P] Create Cloudflare R2 bucket `mcpproxy-rpm` via `wrangler r2 bucket create mcpproxy-rpm`.
- [ ] T006 Bind custom domain `apt.mcpproxy.app` to the `mcpproxy-apt` bucket via the Cloudflare dashboard (R2 → Buckets → Settings → Custom Domains). Confirm Cloudflare auto-creates the CNAME and issues a TLS certificate.
- [ ] T007 Bind custom domain `rpm.mcpproxy.app` to the `mcpproxy-rpm` bucket via the Cloudflare dashboard. Same confirmation as T006.
- [ ] T008 Create an R2 API token scoped to `mcpproxy-apt` and `mcpproxy-rpm` with read/write permissions. Capture the access key ID and secret.
- [ ] T009 [P] Register GitHub Actions secret `PACKAGES_GPG_PRIVATE_KEY` from the backup file: `gh secret set PACKAGES_GPG_PRIVATE_KEY --repo smart-mcp-proxy/mcpproxy-go < ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt`.
- [ ] T010 [P] Register GitHub Actions secret `PACKAGES_GPG_PASSPHRASE` with the passphrase captured during T001.
- [ ] T011 [P] Register GitHub Actions secret `R2_ACCOUNT_ID` with the Cloudflare account ID.
- [ ] T012 [P] Register GitHub Actions secrets `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY` from the token created in T008.
- [ ] T013 [P] Register non-secret GitHub Actions variable `PACKAGES_GPG_KEY_ID` with the full fingerprint from T001.
- [ ] T014 Upload the public signing key to both R2 buckets so that `https://apt.mcpproxy.app/mcpproxy.gpg` and `https://rpm.mcpproxy.app/mcpproxy.gpg` serve it. Use `aws s3 cp` with R2 endpoint (commands in `specs/043-linux-package-repos/quickstart.md` Step 5).
- [ ] T015 Verify the public key is fetchable over HTTPS: `curl -fsSL https://apt.mcpproxy.app/mcpproxy.gpg | gpg --show-keys` must print the fingerprint matching T001. Same for `rpm.mcpproxy.app`.

---

## Phase 2: Foundational (Shared Helper Scripts)

**Purpose**: Shell helpers used by both the CI job (US2) and the smoke tests (US1). MUST complete before any publish job can run end-to-end.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [ ] T016 Create `/Users/user/repos/mcpproxy-go/contrib/linux-repos/` directory and a short `README.md` listing the scripts and their purposes.
- [ ] T017 [P] Create `/Users/user/repos/mcpproxy-go/contrib/linux-repos/apt-ftparchive.conf` defining the suite layout (Suite `stable`, Codename `stable`, Architectures `amd64 arm64`, Components `main`, Origin/Label `MCPProxy`, Description, and `APT::FTPArchive::Release` section). Schema per `contracts/apt-bucket-layout.md`.
- [ ] T018 [P] Create `/Users/user/repos/mcpproxy-go/contrib/linux-repos/mcpproxy.repo.template` containing the dnf source definition from `contracts/rpm-bucket-layout.md`. Published to `rpm.mcpproxy.app/mcpproxy.repo` during publish.
- [ ] T019 Create `/Users/user/repos/mcpproxy-go/contrib/linux-repos/import-key.sh` that reads `PACKAGES_GPG_PRIVATE_KEY` env var (or stdin), imports into a scratch `GNUPGHOME`, sets `PACKAGES_GPG_PASSPHRASE` as the preset passphrase, and exports `GPG_KEY_ID` for downstream steps. Must be idempotent if run twice.
- [ ] T020 [P] Create `/Users/user/repos/mcpproxy-go/contrib/linux-repos/check-key-expiry.sh` that reads the imported key's expiry and emits a GitHub Actions warning (`::warning::`) if within 60 days. Non-fatal. Implements FR-023.

---

## Phase 3: User Story 2 - Automated publish on `v*` tag (Priority: P1) 🎯 MVP prerequisite

**Goal**: The `publish-linux-repos` job in `.github/workflows/release.yml` automatically signs and publishes the apt/yum repos on every stable tag, enforcing the 10-release retention window.

**Independent Test**: Tag a test release `vX.Y.Z` (can be the same as current version plus a suffix for dry runs via `workflow_dispatch`). The workflow job completes successfully, both R2 buckets contain the new artifacts and correctly-signed metadata, and older-than-10 versions are pruned.

**Why US2 comes first despite being P1 alongside US1**: US1's user-facing install commands depend on the repos existing. US2 creates the repos. Once US2 ships, US1 is automatically validated by the smoke-test step (which itself is part of US2's DoD).

### Implementation for User Story 2

- [ ] T021 [US2] Write `/Users/user/repos/mcpproxy-go/contrib/linux-repos/apt-publish.sh` implementing the apt publish sequence: sync down bucket → copy new `.deb` files into `pool/main/m/mcpproxy/` → prune versions beyond top 10 by semver → run `apt-ftparchive packages` and `apt-ftparchive release` → sign with `gpg --detach-sign --armor` (Release.gpg) and `gpg --clearsign` (InRelease) → sync up with `aws s3 sync --delete` → set cache-control per `contracts/apt-bucket-layout.md`.
- [ ] T022 [US2] Write `/Users/user/repos/mcpproxy-go/contrib/linux-repos/rpm-publish.sh` implementing the rpm publish sequence: sync down → copy new `.rpm` files into `{x86_64,aarch64}/` → prune versions beyond top 10 per arch → run `createrepo_c {x86_64,aarch64}/` → sign each `repodata/repomd.xml` with `gpg --detach-sign --armor` → upload `mcpproxy.repo` from template → sync up → set cache-control per `contracts/rpm-bucket-layout.md`.
- [ ] T023 [US2] Write `/Users/user/repos/mcpproxy-go/contrib/linux-repos/publish.sh` top-level orchestrator that sequences `import-key.sh` → `check-key-expiry.sh` → `apt-publish.sh` → `rpm-publish.sh`. Supports a `--dry-run` flag that runs metadata generation locally but skips the final sync-up.
- [ ] T024 [US2] Modify `/Users/user/repos/mcpproxy-go/.github/workflows/release.yml` to add the `publish-linux-repos` job per the contract in `contracts/publish-linux-repos.job.md`. Depends on `build` job, guarded by `startsWith(github.ref, 'refs/tags/v') && !contains(github.ref, '-')` (FR-022).
- [ ] T025 [US2] In the new job, wire in the `Install repo tooling` step: `sudo apt-get install -y apt-utils createrepo-c gnupg`. AWS CLI v2 is pre-installed on ubuntu-latest.
- [ ] T026 [US2] In the new job, wire in the `Download package artifacts` step using `actions/download-artifact@v4` with `pattern: linux-packages-*` and `merge-multiple: true`, landing files in `release-artifacts/`.
- [ ] T027 [US2] In the new job, invoke `contrib/linux-repos/publish.sh release-artifacts` with all the required env vars (secrets, variables, R2 endpoint URL). Implements FR-009 through FR-013.
- [ ] T028 [US2] Write `/Users/user/repos/mcpproxy-go/contrib/linux-repos/smoke-test-debian.sh VERSION` that runs a `debian:stable-slim` container, performs the documented install, asserts `mcpproxy --version` contains VERSION. Exits non-zero on mismatch. Implements half of FR-014.
- [ ] T029 [US2] Write `/Users/user/repos/mcpproxy-go/contrib/linux-repos/smoke-test-fedora.sh VERSION` — same for `fedora:latest` + dnf. Implements the other half of FR-014.
- [ ] T030 [US2] Add the two smoke-test steps to the `publish-linux-repos` job, each invoking its script with `"${GITHUB_REF_NAME#v}"` as the version argument. Both must succeed for the job to pass.

**Checkpoint**: At this point, a `v*` tag push publishes to `apt.mcpproxy.app` and `rpm.mcpproxy.app`, metadata is signed, retention is enforced, and smoke tests prove installability. User Story 1 is automatically exercised by the smoke test.

---

## Phase 4: User Story 1 - End-user install via apt/dnf (Priority: P1)

**Goal**: A Linux user can copy-paste the documented commands and install / upgrade MCPProxy via their native package manager.

**Independent Test**: Already covered by the smoke-test step in US2's publish job. Can additionally be re-verified by running `docker run --rm debian:stable-slim bash -c '<4 commands> && mcpproxy --version'` outside CI.

**Note**: The user-facing install path is infrastructure from US2 plus doc from US4. The only US1-specific task is verifying end-to-end (beyond CI) that the flow works on a fresh workstation. If the smoke test in US2 passes, US1 passes.

### Verification for User Story 1

- [ ] T031 [US1] Execute manual end-to-end test on a fresh Docker container outside CI following the commands in `specs/043-linux-package-repos/quickstart.md` Step 8 for both `debian:stable-slim` and `fedora:latest`. Confirm `mcpproxy --version` returns the expected version and the GPG fingerprint of the fetched key matches the fingerprint recorded in T001.

**Checkpoint**: US1 is validated.

---

## Phase 5: User Story 4 - Install page rewritten (Priority: P2)

**Goal**: Website, README, and in-repo docs lead with apt/dnf repo install commands. Direct-download links remain as a documented fallback.

**Independent Test**: Visit the updated website install page and scan-read: the apt/dnf blocks must be the first Linux option, above direct downloads. The signing key fingerprint and public-key URL are visible. Run the commands from each block in a container and confirm install.

### Implementation for User Story 4

- [ ] T032 [P] [US4] Update `/Users/user/repos/mcpproxy.app-website/src/pages/docs/installation.astro`: add a new "Debian / Ubuntu (apt)" section as the primary Linux option (above direct .deb / .rpm downloads). Include: 4-command copy-paste block, GPG fingerprint from T001, link to `https://apt.mcpproxy.app/mcpproxy.gpg`, link to the feature doc. Implements FR-015, FR-016.
- [ ] T033 [P] [US4] Update `/Users/user/repos/mcpproxy.app-website/src/pages/docs/installation.astro`: add a "Fedora / RHEL / Rocky / AlmaLinux (dnf)" section parallel to the apt section. Same trust-verification fields. Implements FR-015, FR-016.
- [ ] T034 [US4] In the same `installation.astro` file, move the existing "Linux Binary Downloads" section (direct `.deb`/`.rpm` download links) below the new apt/dnf sections and reword the preamble to frame it as a fallback for air-gapped/offline installs. Implements FR-017.
- [ ] T035 [P] [US4] Update `/Users/user/repos/mcpproxy-go/README.md` Linux install section: replace the current "download .deb / apt install ./foo.deb" snippet with the apt and dnf repo snippets. Link to the website install page. Implements FR-018.
- [ ] T036 [P] [US4] Update `/Users/user/repos/mcpproxy-go/docs/getting-started/installation.md` to mirror the README changes (apt block, dnf block, fingerprint, link to feature doc). Implements FR-019.
- [ ] T037 [US4] Create `/Users/user/repos/mcpproxy-go/docs/features/linux-package-repos.md` covering: how the repos are hosted (overview, not Cloudflare implementation details), the 10-release retention window, pinning a specific version with `apt install mcpproxy=X.Y.Z`, air-gapped mirror instructions, and common troubleshooting (GPG errors, 404, cache staleness). Implements FR-020.

**Checkpoint**: First-time visitors to the install page see apt/dnf as the obvious Linux option. US4 validated.

---

## Phase 6: User Story 3 - Key rotation procedure (Priority: P2)

**Goal**: The maintainer can complete an emergency or scheduled GPG key rotation end-to-end in under 30 minutes using a documented procedure.

**Independent Test**: On a scratch bucket (or with a dry-run flag), generate a fresh key, follow the runbook steps, and confirm that (1) the new public key is distributed to the repo within one publish run, (2) a fresh install container validates packages signed with the new key, (3) the backup file at `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` reflects the new key.

### Implementation for User Story 3

- [ ] T038 [US3] Create `/Users/user/repos/mcpproxy-go/docs/operations/linux-package-repos-infrastructure.md` covering: R2 bucket configuration, custom domain binding, GitHub Actions secret/variable inventory, the step-by-step GPG key rotation procedure (generate → backup → secrets → commit public key → release), how to manually republish a single release, and how to purge a bad version. Implements FR-021.
- [ ] T039 [US3] Cross-link the ops doc from the feature doc (T037) and from the backup file (`~/repos/PACKAGES_GPG_PRIVATE_KEY.txt`) so that a maintainer hitting the rotation path finds the runbook from either entry point.

**Checkpoint**: Rotation runbook exists, is cross-linked, and can be dry-run executed.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T040 [P] Run `./scripts/run-linter.sh` and `shellcheck contrib/linux-repos/*.sh` on the workstation; fix any reported issues.
- [ ] T041 [P] Tag a test release using `workflow_dispatch` (not a real `v*` push) to exercise the new job end-to-end without creating a public release artifact on GitHub. Verify smoke tests pass and buckets contain expected files.
- [ ] T042 After a successful dry-run, tag the next real release (e.g., `v0.24.7`) and watch the production workflow publish to the real buckets.
- [ ] T043 Run final verification: from a fresh Mac/Linux workstation, `curl https://apt.mcpproxy.app/mcpproxy.gpg` and `curl https://rpm.mcpproxy.app/mcpproxy.gpg` succeed with matching fingerprints; `docker run --rm debian:stable-slim` install flow completes end-to-end; same for `fedora:latest`.
- [ ] T044 Merge the `043-linux-package-repos` branch and, if needed, post a release note highlighting the new install method.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — must run first. Produces the signing key, buckets, and secrets needed downstream.
- **Phase 2 (Foundational)**: Depends on Phase 1 (needs the key fingerprint and bucket URLs for config files).
- **Phase 3 (US2 — Publish automation)**: Depends on Phase 2. Publishes to the buckets created in Phase 1.
- **Phase 4 (US1 — End-user install verification)**: Depends on Phase 3 (no repos = nothing to install from).
- **Phase 5 (US4 — Docs rewrite)**: Can start any time after Phase 1 completes (the key fingerprint is the only prerequisite from the repo side). In practice best run in parallel with or after Phase 3.
- **Phase 6 (US3 — Rotation runbook)**: Depends on Phase 5 (cross-links the feature doc).
- **Phase 7 (Polish)**: Depends on all earlier phases.

### Parallel Opportunities

- T004 + T005 (bucket creation) — independent buckets.
- T009–T013 (GitHub Actions secrets/variables) — independent secrets.
- T017 + T018 + T020 (Phase 2 config files + helper script that doesn't depend on `import-key.sh`).
- T032 + T033 + T035 + T036 (doc changes across website + README + in-repo) — parallel, different files.
- T040 + T041 — linter vs test tag, independent.

### Within Each User Story

- Helpers (Phase 2) before the CI job (Phase 3 / US2).
- US2 publish job before US1 smoke verification.
- Website and README doc edits (US4) before operations runbook (US3, because it links into them).

---

## Parallel Example: Phase 5 Docs Rewrite

```bash
# Four doc surfaces, four files, no shared state — can be edited in parallel.
Task: "T032 [P] [US4] Update installation.astro — add apt section"
Task: "T033 [P] [US4] Update installation.astro — add dnf section"   # same file, different section — serialize
Task: "T035 [P] [US4] Update README.md Linux install block"
Task: "T036 [P] [US4] Update docs/getting-started/installation.md"
```

Note: T032 and T033 touch the same `installation.astro` file, so they cannot truly run in parallel despite both being marked [P] — run them sequentially but they are independent of the other [P] tasks in this phase.

---

## Implementation Strategy

### MVP Scope

The MVP is Phase 1 → Phase 2 → Phase 3 (US2) → Phase 4 (US1 verification). Once US2's smoke tests pass, MCPProxy is installable via `apt install mcpproxy` and `dnf install mcpproxy`. That is the core value delivery.

### Incremental Delivery

1. Phases 1+2+3+4 → MVP: repos work, installable.
2. Phase 5 → Documentation lets users find the new method (essential for adoption).
3. Phase 6 → Rotation runbook protects against future incidents.
4. Phase 7 → Polish, real release, final verification.

### Solo Execution Order

This is a solo-maintainer feature with no parallel team. Sequential order:

1. T001–T015 (Setup, ~1 hour).
2. T016–T020 (Foundational helpers, ~30 min).
3. T021–T030 (Publish automation, ~2 hours).
4. T031 (US1 verification, ~10 min).
5. T032–T037 (Docs, ~1 hour).
6. T038–T039 (Ops runbook, ~30 min).
7. T040–T044 (Polish + real release verification).

Total: ~5–6 hours of focused work end-to-end, plus a real release cycle.

---

## Notes

- Most shell scripts are stateless orchestrators around well-tested external tools; no unit tests are generated for them.
- The smoke-test step is the authoritative functional test per FR-014.
- Backup file `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` MUST NOT be committed to any repository — `~/repos/` is the parent of all repos but not itself a repo.
- Commit after each task or logical group (e.g., after T020, after T030, after T037, after T039).
- Stop at any checkpoint to validate progress independently.
