# Tasks: MCPProxy Repo Restructure (Personal + Teams Foundation)

**Input**: Design documents from `/specs/029-mcpproxy-teams/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, quickstart.md
**Scope**: Repository restructure and build infrastructure only. Teams feature logic (auth, workspaces, users) is out of scope — this creates the skeleton.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- Include exact file paths in descriptions

---

## Phase 1: Setup (Edition Detection & Build Tags)

**Purpose**: Establish the build tag architecture that separates personal and teams editions

- [x] T001 [P] Create edition detection file `cmd/mcpproxy/edition.go` with `var Edition = "personal"` and `GetEdition()` function
- [x] T002 [P] Create teams edition override file `cmd/mcpproxy/edition_teams.go` with `//go:build teams` tag that sets `Edition = "teams"`
- [x] T003 Update `cmd/mcpproxy/main.go` to log edition on startup and pass edition to server initialization
- [x] T004 Add edition field to `/api/v1/status` response in `internal/httpapi/server.go`
- [x] T005 Add edition to `mcpproxy version` CLI output in `cmd/mcpproxy/main.go`

**Checkpoint**: `go build ./cmd/mcpproxy && ./mcpproxy version` shows "personal"; `go build -tags teams ./cmd/mcpproxy && ./mcpproxy version` shows "teams"

---

## Phase 2: Teams Skeleton (internal/teams/)

**Purpose**: Create the teams feature registry and package skeleton with build tags

- [x] T006 Create `internal/teams/doc.go` with `//go:build teams` tag and package documentation
- [x] T007 Create `internal/teams/registry.go` with Feature struct, Register(), SetupAll() functions (build-tagged)
- [x] T008 Create `internal/teams/registry_test.go` with tests verifying registration and setup (build-tagged)
- [x] T009 Create teams registration entry point `cmd/mcpproxy/teams_register.go` with `//go:build teams` that imports `internal/teams` and calls `SetupAll()` during init
- [x] T010 Verify both builds compile: `go build ./cmd/mcpproxy` (no teams code) and `go build -tags teams ./cmd/mcpproxy` (with teams skeleton)

**Checkpoint**: `go test -tags teams ./internal/teams/...` passes; personal build has zero teams code compiled in

---

## Phase 3: Dockerfile & Build Targets

**Purpose**: Create Docker distribution for teams edition and extend Makefile

- [x] T011 [P] Create `Dockerfile` at repo root — multi-stage build with `golang:1.24` builder, `gcr.io/distroless/base` runtime, builds with `-tags teams`, embeds frontend, exposes 8080, entrypoint `mcpproxy serve --listen 0.0.0.0:8080`
- [x] T012 [P] Create `.dockerignore` excluding `.git`, `node_modules`, `native/`, `*.md`, test files
- [x] T013 Add Makefile targets: `build-teams` (Go binary with teams tag), `build-docker` (Docker image), `build-deb` (placeholder echoing "TODO")
- [x] T014 Verify `make build` still produces personal edition (no regression)
- [x] T015 Verify `make build-teams` produces teams binary and `make build-docker` builds Docker image

**Checkpoint**: `docker run mcpproxy-teams:dev mcpproxy version` shows "teams" edition

---

## Phase 4: Native Tray App Placeholders

**Purpose**: Create directory structure for future Swift (macOS) and C# (Windows) tray apps

- [x] T016 [P] Create `native/macos/README.md` with placeholder describing future Swift tray app, build requirements (Xcode 15+), and relationship to `cmd/mcpproxy-tray/`
- [x] T017 [P] Create `native/windows/README.md` with placeholder describing future C# tray app, build requirements (.NET 8+), and relationship to `cmd/mcpproxy-tray/`

**Checkpoint**: Directory structure exists, no build changes

---

## Phase 5: Frontend Teams Route Skeleton

**Purpose**: Create empty directory structure for future teams-specific Vue pages

- [x] T018 [P] Create `frontend/src/views/teams/.gitkeep` for future teams pages (login, admin panel, workspace)
- [x] T019 [P] Create `frontend/src/components/teams/.gitkeep` for future teams components

**Checkpoint**: Frontend still builds cleanly with `cd frontend && npm run build`

---

## Phase 6: Release Workflow Extension

**Purpose**: Extend GitHub Actions release workflow to build teams assets alongside personal

- [x] T020 Add teams Linux matrix entries (amd64, arm64) to `.github/workflows/release.yml` build job — uses `-tags teams` flag, produces `mcpproxy-teams-*` archives
- [x] T021 Add `build-docker` job to `.github/workflows/release.yml` — builds and pushes `ghcr.io/smart-mcp-proxy/mcpproxy-teams:$VERSION` on tag push
- [x] T022 Update release notes prompt in `.github/workflows/release.yml` to mention both editions
- [x] T023 Update release asset upload to include teams archives with `mcpproxy-teams-` prefix

**Checkpoint**: CI workflow is valid YAML; teams matrix entries produce correctly named assets

---

## Phase 7: Documentation & Polish

**Purpose**: Update project documentation to reflect dual-edition architecture

- [x] T024 Update `CLAUDE.md` — add Build & Distribution section documenting `build-teams`, `build-docker`, edition detection, `internal/teams/` structure
- [x] T025 Update `Makefile` help target to include new build-teams, build-docker, build-deb targets
- [x] T026 Verify all existing tests pass: `go test ./internal/... -v` (personal build) — all pass except pre-existing `internal/server` timeout
- [x] T027 Verify teams build tests pass: `go test -tags teams ./internal/teams/... -v`
- [x] T028 Verify E2E tests pass: `./scripts/test-api-e2e.sh` — 61/71 pass, 10 failures are pre-existing (same on clean branch)
- [x] T029 Verify linter passes: `./scripts/run-linter.sh`

**Checkpoint**: All tests green, docs updated, both editions build cleanly

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1** (Setup): No dependencies — start immediately
- **Phase 2** (Teams Skeleton): Depends on T001, T002 from Phase 1
- **Phase 3** (Docker/Build): Depends on Phase 1 completion
- **Phase 4** (Native Placeholders): No dependencies — can run in parallel with any phase
- **Phase 5** (Frontend Skeleton): No dependencies — can run in parallel with any phase
- **Phase 6** (Release Workflow): Depends on Phase 3 (needs to know build commands)
- **Phase 7** (Documentation): Depends on all other phases

### Parallel Opportunities

```
Phase 1 (T001 ∥ T002) → T003 → (T004 ∥ T005)
                    ↓
Phase 4 (T016 ∥ T017)  ←── can run in parallel with everything
Phase 5 (T018 ∥ T019)  ←── can run in parallel with everything
                    ↓
Phase 2 (T006 ∥ T007) → T008 → T009 → T010
Phase 3 (T011 ∥ T012) → T013 → T014 → T015
                    ↓
Phase 6 (T020 ∥ T021 ∥ T022) → T023
                    ↓
Phase 7 (T024 ∥ T025) → T026 → T027 → T028 → T029
```

### Maximum Parallelism (with subagents)

**Wave 1** (independent files, no deps):
- Agent A: T001 + T002 (edition files)
- Agent B: T016 + T017 (native placeholders)
- Agent C: T018 + T019 (frontend skeleton)
- Agent D: T011 + T012 (Dockerfile + .dockerignore)

**Wave 2** (depends on Wave 1):
- Agent A: T003 + T004 + T005 (main.go, status, version)
- Agent B: T006 + T007 + T008 + T009 + T010 (teams skeleton)
- Agent C: T013 (Makefile targets)

**Wave 3** (depends on Wave 2):
- Agent A: T020 + T021 + T022 + T023 (release workflow)
- Agent B: T024 + T025 (documentation)

**Wave 4** (verification):
- T014, T015, T026, T027, T028, T029 (build & test verification)

---

## Implementation Strategy

### MVP First

1. Complete Phase 1 + Phase 2 → edition detection works, teams skeleton compiles
2. **STOP and VALIDATE**: Both builds work, tests pass
3. Complete Phase 3 → Docker image builds
4. Complete remaining phases

### Summary

| Metric | Value |
|--------|-------|
| Total tasks | 29 |
| Parallelizable tasks | 12 |
| Phases | 7 |
| Estimated waves (with subagents) | 4 |
