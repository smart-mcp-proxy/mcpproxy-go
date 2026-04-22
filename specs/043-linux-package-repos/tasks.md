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

- [X] T001 Generate GPG signing key — fingerprint `3B6FA1AD5D5359DA51F18DDCE1B59B9BA1CB8A3B`, expires 2031-04-21.
- [X] T002 Export ASCII-armored public key to `contrib/signing/mcpproxy-packages.asc` (3216 bytes).
- [X] T003 Write GPG private-key backup to `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` (8472 bytes, mode 0600, outside all git repos).
- [X] T004 Created R2 bucket `mcpproxy-apt` (Eastern Europe, Standard class).
- [X] T005 Created R2 bucket `mcpproxy-rpm` (Eastern Europe, Standard class).
- [X] T006 Bound custom domain `apt.mcpproxy.app` — CNAME `apt` → `mcpproxy-apt`, TLS 1.0 min, status Initializing.
- [X] T007 Bound custom domain `rpm.mcpproxy.app` — CNAME `rpm` → `mcpproxy-rpm`, TLS 1.0 min, status Initializing.
- [X] T008 Created R2 API token "MCPProxy Packages CI" — Object Read & Write, scoped to both buckets, Forever TTL.
- [X] T009 Registered GH secret `PACKAGES_GPG_PRIVATE_KEY`.
- [X] T010 Registered GH secret `PACKAGES_GPG_PASSPHRASE`.
- [X] T011 Registered GH secret `R2_ACCOUNT_ID`.
- [X] T012 Registered GH secrets `R2_ACCESS_KEY_ID` + `R2_SECRET_ACCESS_KEY`.
- [X] T013 Registered GH variable `PACKAGES_GPG_KEY_ID` = `3B6FA1AD5D5359DA51F18DDCE1B59B9BA1CB8A3B`.
- [X] T014 Uploaded `mcpproxy.gpg` and `mcpproxy.gpg.asc` to both buckets via `wrangler r2 object put`.
- [X] T015 Verified: `curl https://apt.mcpproxy.app/mcpproxy.gpg` and same for rpm return HTTP 200 with public key matching fingerprint `3B6F A1AD 5D53 59DA 51F1 8DDC E1B5 9B9B A1CB 8A3B`. Root cause of earlier 404: wrangler r2 object put defaults to local storage; must use `--remote` flag.

---

## Phase 2: Foundational (Shared Helper Scripts)

**Purpose**: Shell helpers used by both the CI job (US2) and the smoke tests (US1). MUST complete before any publish job can run end-to-end.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T016 Created `contrib/linux-repos/` with README.md.
- [X] T017 Created `apt-ftparchive.conf`.
- [X] T018 Created `mcpproxy.repo.template`.
- [X] T019 Created `import-key.sh`.
- [X] T020 Created `check-key-expiry.sh`.

---

## Phase 3: User Story 2 - Automated publish on `v*` tag (Priority: P1) 🎯 MVP prerequisite

**Goal**: The `publish-linux-repos` job in `.github/workflows/release.yml` automatically signs and publishes the apt/yum repos on every stable tag, enforcing the 10-release retention window.

**Independent Test**: Tag a test release `vX.Y.Z` (can be the same as current version plus a suffix for dry runs via `workflow_dispatch`). The workflow job completes successfully, both R2 buckets contain the new artifacts and correctly-signed metadata, and older-than-10 versions are pruned.

**Why US2 comes first despite being P1 alongside US1**: US1's user-facing install commands depend on the repos existing. US2 creates the repos. Once US2 ships, US1 is automatically validated by the smoke-test step (which itself is part of US2's DoD).

### Implementation for User Story 2

- [X] T021 [US2] Wrote `apt-publish.sh` (5487 bytes).
- [X] T022 [US2] Wrote `rpm-publish.sh` (5572 bytes).
- [X] T023 [US2] Wrote `publish.sh` orchestrator (1171 bytes).
- [X] T024-T027 [US2] Added `publish-linux-repos` job to `.github/workflows/release.yml` (needs [build, release], guarded on non-prerelease v* tags, wires in all env vars and secrets, invokes `contrib/linux-repos/publish.sh release-artifacts`, followed by Debian + Fedora smoke tests). YAML validated.
- [X] T028 [US2] Wrote `smoke-test-debian.sh` (1291 bytes).
- [X] T029 [US2] Wrote `smoke-test-fedora.sh` (1264 bytes).
- [X] T030 [US2] Smoke-test steps wired into job (see T024-T027).

**Checkpoint**: At this point, a `v*` tag push publishes to `apt.mcpproxy.app` and `rpm.mcpproxy.app`, metadata is signed, retention is enforced, and smoke tests prove installability. User Story 1 is automatically exercised by the smoke test.

---

## Phase 4: User Story 1 - End-user install via apt/dnf (Priority: P1)

**Goal**: A Linux user can copy-paste the documented commands and install / upgrade MCPProxy via their native package manager.

**Independent Test**: Already covered by the smoke-test step in US2's publish job. Can additionally be re-verified by running `docker run --rm debian:stable-slim bash -c '<4 commands> && mcpproxy --version'` outside CI.

**Note**: The user-facing install path is infrastructure from US2 plus doc from US4. The only US1-specific task is verifying end-to-end (beyond CI) that the flow works on a fresh workstation. If the smoke test in US2 passes, US1 passes.

### Verification for User Story 1

- [X] T031 [US1] End-to-end test: ran `publish.sh` locally against real R2 with v0.24.6 artifacts; apt install in debian:stable-slim and dnf install in fedora:latest both succeed, `mcpproxy --version` returns `0.24.6`, key fingerprint matches T001.

**Checkpoint**: US1 is validated.

---

## Phase 5: User Story 4 - Install page rewritten (Priority: P2)

**Goal**: Website, README, and in-repo docs lead with apt/dnf repo install commands. Direct-download links remain as a documented fallback.

**Independent Test**: Visit the updated website install page and scan-read: the apt/dnf blocks must be the first Linux option, above direct downloads. The signing key fingerprint and public-key URL are visible. Run the commands from each block in a container and confirm install.

### Implementation for User Story 4

- [X] T032 [US4] Added apt section to `mcpproxy.app-website/src/pages/docs/installation.astro`.
- [X] T033 [US4] Added dnf section to same file (paired with apt section above Homebrew block).
- [X] T034 [US4] Existing "Linux Binary Downloads" remains below new sections; default flow now visits repo install first.
- [X] T035 [US4] Updated `README.md` Linux install block.
- [X] T036 [US4] Updated `docs/getting-started/installation.md` — apt/dnf repo sections promoted above direct-download fallback.
- [X] T037 [US4] Created `docs/features/linux-package-repos.md`.

**Checkpoint**: First-time visitors to the install page see apt/dnf as the obvious Linux option. US4 validated.

---

## Phase 6: User Story 3 - Key rotation procedure (Priority: P2)

**Goal**: The maintainer can complete an emergency or scheduled GPG key rotation end-to-end in under 30 minutes using a documented procedure.

**Independent Test**: On a scratch bucket (or with a dry-run flag), generate a fresh key, follow the runbook steps, and confirm that (1) the new public key is distributed to the repo within one publish run, (2) a fresh install container validates packages signed with the new key, (3) the backup file at `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` reflects the new key.

### Implementation for User Story 3

- [X] T038 [US3] Created `docs/operations/linux-package-repos-infrastructure.md`.
- [X] T039 [US3] Cross-linked ops doc from feature doc; backup file already references ops runbook path.

**Checkpoint**: Rotation runbook exists, is cross-linked, and can be dry-run executed.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T040 bash -n passes on all 7 scripts. shellcheck not installed locally (skipped; CI runs pass syntax).
- [X] T041 Local end-to-end dry-run via Docker container (not `workflow_dispatch` — CI job is gated on tag refs). Results: full publish flow runs green against real R2, buckets populated with current v0.24.6 artifacts + signed metadata, both Debian and Fedora containers install mcpproxy from the repos.
- [ ] T042 Tag the next real release (`v0.24.7`) — deferred to user. The `publish-linux-repos` job will run on the next stable tag automatically. Already-tested scripts mean this should succeed on first run.
- [X] T043 Final live verification: `curl https://apt.mcpproxy.app/mcpproxy.gpg` → fingerprint matches. Same for rpm. `docker run --rm debian:stable-slim` apt install succeeds. `docker run --rm fedora:latest` dnf install succeeds.
- [ ] T044 Merge branch — deferred to user.

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
