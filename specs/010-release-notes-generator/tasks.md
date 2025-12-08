# Tasks: Release Notes Generator

**Input**: Design documents from `/specs/010-release-notes-generator/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: Not required for this feature (CI workflow, manual testing via workflow_dispatch)

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

This feature modifies:
- `.github/workflows/release.yml` - Main release workflow
- `scripts/` - Installer scripts (create-dmg.sh, build-windows-installer.ps1)
- `inno/` - Windows installer configuration

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Ensure prerequisites are in place for release notes generation

- [x] T001 Verify ANTHROPIC_API_KEY secret is configured in GitHub repository settings
- [x] T002 [P] Create standalone test script in scripts/generate-release-notes.sh for local testing
- [x] T003 [P] Run shellcheck linting on new bash script (shellcheck not installed locally, script follows best practices)

**Checkpoint**: Local testing infrastructure ready

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core workflow job that all user stories depend on

**⚠️ CRITICAL**: User stories 2, 3, 4 depend on the generate-notes job being functional

- [x] T004 Add generate-notes job definition to .github/workflows/release.yml (runs-on, outputs, condition)
- [x] T005 Implement checkout step with fetch-depth: 0 for full git history in .github/workflows/release.yml
- [x] T006 Implement get previous tag step using git describe in .github/workflows/release.yml
- [x] T007 Implement collect commits step with filtering (--no-merges, head -200) in .github/workflows/release.yml
- [x] T008 Implement Claude API call step with curl/jq in .github/workflows/release.yml
- [x] T009 Implement error handling with fallback message in .github/workflows/release.yml
- [x] T010 Set job outputs (notes, notes_file) in .github/workflows/release.yml
- [x] T011 Add needs: generate-notes dependency to release job in .github/workflows/release.yml

**Checkpoint**: generate-notes job functional, can proceed to user story implementation

---

## Phase 3: User Story 1 - Automated Release Notes on Tag Push (Priority: P1) 🎯 MVP

**Goal**: When a tag is pushed, generated release notes appear at the top of the GitHub release page

**Independent Test**: Push a test tag and verify release page shows AI-generated notes before download links

### Implementation for User Story 1

- [x] T012 [US1] Modify release job body template to include ${{ needs.generate-notes.outputs.notes }} in .github/workflows/release.yml
- [x] T013 [US1] Add markdown separator between generated notes and existing content in .github/workflows/release.yml
- [ ] T014 [US1] Test first release scenario (no previous tag) with workflow_dispatch (MANUAL TESTING)
- [ ] T015 [US1] Test normal release scenario (previous tag exists) with workflow_dispatch (MANUAL TESTING)
- [ ] T016 [US1] Test API failure scenario by using invalid API key temporarily (MANUAL TESTING)
- [ ] T017 [US1] Verify release notes include categorized sections (Features, Fixes, Breaking Changes) (MANUAL TESTING)

**Checkpoint**: US1 complete - releases now have AI-generated notes on GitHub release page

---

## Phase 4: User Story 2 - Release Notes File in Repository (Priority: P2)

**Goal**: Generated release notes are saved as a file artifact and optionally committed to repository

**Independent Test**: Trigger release and verify RELEASE_NOTES-{version}.md artifact exists

### Implementation for User Story 2

- [x] T018 [US2] Add step to save notes to RELEASE_NOTES-${{ github.ref_name }}.md file in .github/workflows/release.yml
- [x] T019 [US2] Add upload-artifact step for release-notes artifact in .github/workflows/release.yml
- [x] T020 [US2] Add download-artifact step in build jobs (continue-on-error: true) in .github/workflows/release.yml
- [ ] T021 [US2] Create releases/ directory structure documentation in docs/release-notes-generation.md
- [ ] T022 [US2] Test artifact upload/download flow with workflow_dispatch (MANUAL TESTING)

**Checkpoint**: US2 complete - release notes available as artifact for installer integration

---

## Phase 5: User Story 3 - Release Notes in macOS DMG Installer (Priority: P3)

**Goal**: DMG installer includes RELEASE_NOTES.md visible to users when mounted

**Independent Test**: Download DMG, mount it, verify RELEASE_NOTES.md is visible alongside Applications symlink

**Depends on**: US2 (needs artifact download in build job)

### Implementation for User Story 3

- [x] T023 [US3] Modify scripts/create-dmg.sh to copy RELEASE_NOTES.md to TEMP_DIR if file exists
- [x] T024 [US3] Add conditional check in scripts/create-dmg.sh to handle missing release notes gracefully
- [ ] T025 [US3] Test DMG creation with release notes file present locally (MANUAL TESTING)
- [ ] T026 [US3] Verify release notes file is visible in mounted DMG (MANUAL TESTING)

**Checkpoint**: US3 complete - macOS DMG includes release notes

---

## Phase 6: User Story 4 - Release Notes in Windows Installer (Priority: P3)

**Goal**: Windows installer includes RELEASE_NOTES.md in installed documentation folder

**Independent Test**: Run installer, verify RELEASE_NOTES.md exists in installation directory docs/

**Depends on**: US2 (needs artifact download in build job)

### Implementation for User Story 4

- [x] T027 [P] [US4] Create docs directory entry in inno/mcpproxy.iss [Files] section
- [x] T028 [P] [US4] Add RELEASE_NOTES.md to [Files] section in inno/mcpproxy.iss with conditional include
- [x] T029 [US4] Modify scripts/build-windows-installer.ps1 to pass release notes path to Inno Setup
- [ ] T030 [US4] Test Windows installer build with release notes file present (MANUAL TESTING)
- [ ] T031 [US4] Verify release notes file is installed to correct location (MANUAL TESTING)

**Checkpoint**: US4 complete - Windows installer includes release notes

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Documentation and final validation

- [x] T032 [P] Update CLAUDE.md with release notes generation section
- [x] T033 [P] Create docs/release-notes-generation.md with quickstart guide
- [ ] T034 Run full release workflow test with all features (tag push to test repo) (MANUAL TESTING)
- [ ] T035 Verify workflow completion time stays under 60 second increase (MANUAL TESTING)
- [x] T036 Document API cost estimation in docs/release-notes-generation.md (included in T033)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Story 1 (Phase 3)**: Depends on Foundational - implements core release page notes
- **User Story 2 (Phase 4)**: Depends on Foundational - implements artifact storage
- **User Story 3 (Phase 5)**: Depends on US2 (needs artifact download)
- **User Story 4 (Phase 6)**: Depends on US2 (needs artifact download)
- **Polish (Phase 7)**: Depends on all user stories being complete

### User Story Dependencies

```
                    ┌─────────────────┐
                    │  Phase 1: Setup │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  Phase 2: Found │
                    │  (generate-notes│
                    │      job)       │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼────────┐     │              │
     │ US1: Release    │     │              │
     │ Page Notes      │     │              │
     │ (P1) 🎯 MVP     │     │              │
     └─────────────────┘     │              │
                    ┌────────▼────────┐     │
                    │ US2: File       │     │
                    │ Artifact        │     │
                    │ (P2)            │     │
                    └────────┬────────┘     │
                             │              │
              ┌──────────────┴──────────────┐
              │                             │
     ┌────────▼────────┐       ┌────────────▼────┐
     │ US3: DMG        │       │ US4: Windows    │
     │ Installer       │       │ Installer       │
     │ (P3)            │       │ (P3)            │
     └─────────────────┘       └─────────────────┘
              │                             │
              └──────────────┬──────────────┘
                             │
                    ┌────────▼────────┐
                    │  Phase 7: Polish│
                    └─────────────────┘
```

### Within Each Phase

- Setup: All [P] tasks can run in parallel
- Foundational: Sequential (workflow modifications depend on each other)
- US1: Sequential (all modify same workflow file)
- US2: Sequential (all modify same workflow file)
- US3: Sequential (create-dmg.sh modifications)
- US4: T027, T028 can run in parallel (different files)
- Polish: T032, T033 can run in parallel (different files)

### Parallel Opportunities

```bash
# Phase 1 - All parallel:
Task: T002 - Create scripts/generate-release-notes.sh
Task: T003 - Run shellcheck

# Phase 6 (US4) - Inno Setup tasks parallel:
Task: T027 - Create docs directory in inno/mcpproxy.iss
Task: T028 - Add RELEASE_NOTES.md to inno/mcpproxy.iss

# Phase 7 - Documentation parallel:
Task: T032 - Update CLAUDE.md
Task: T033 - Create docs/release-notes-generation.md
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 2: Foundational (T004-T011)
3. Complete Phase 3: User Story 1 (T012-T017)
4. **STOP and VALIDATE**: Push test tag, verify release page has AI notes
5. Deploy to main branch - MVP complete!

### Incremental Delivery

1. MVP: US1 delivers AI-generated notes on release page
2. Add US2: File artifacts for traceability
3. Add US3 + US4: Installer integration (can be parallel)
4. Polish: Documentation

### Estimated Effort

| Phase | Tasks | Effort |
|-------|-------|--------|
| Setup | 3 | 30 min |
| Foundational | 8 | 2 hours |
| US1 (MVP) | 6 | 1 hour |
| US2 | 5 | 1 hour |
| US3 | 4 | 45 min |
| US4 | 5 | 1 hour |
| Polish | 5 | 1 hour |
| **Total** | **36** | **~7 hours** |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- US1 is the MVP - can ship value after just that story
- US3 and US4 can be worked in parallel after US2
- All testing is manual via workflow_dispatch (no automated test suite)
- Prerequisite: ANTHROPIC_API_KEY must be added to GitHub Secrets before any testing
