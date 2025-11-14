# Tasks: Windows Installer for MCPProxy

**Input**: Design documents from `/specs/002-windows-installer/`
**Prerequisites**: plan.md (complete), spec.md (complete), research.md (complete), data-model.md (complete), contracts/ (complete), quickstart.md (complete)

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

This project follows Go repository structure:
- `cmd/` - Application entry points (mcpproxy, mcpproxy-tray)
- `scripts/` - Build and installer scripts
- `wix/` - WiX Toolset definitions (new)
- `.github/workflows/` - CI/CD workflows

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create directory structure and installer resources

- [X] T001 [P1] Create `scripts/installer-resources/windows/` directory for Windows-specific resources
- [X] T002 [P1] Create `wix/` directory at repository root for WiX Toolset definitions
- [X] T003 [P1] [P] Add WiX Toolset as documentation dependency in CLAUDE.md

**Checkpoint**: Directory structure created - ready for content generation

---

## Phase 2: Foundational (None Required)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**⚠️ SKIP THIS PHASE**: Windows installer is pure build tooling - no foundational runtime components required. All user stories can begin immediately after Phase 1.

---

## Phase 3: User Story 1 - Basic Windows Installation (Priority: P1) 🎯 MVP

**Goal**: Deliver functional Windows installer that installs both mcpproxy.exe and mcpproxy-tray.exe, creates Start Menu shortcuts, and registers uninstall entry

**Independent Test**: Download installer artifact, run on clean Windows 10/11 VM, verify binaries installed to Program Files, Start Menu shortcut exists, and tray launches successfully

### Implementation for User Story 1

- [X] T004 [P1] [US1] [P] Create Inno Setup installer script at `scripts/installer.iss` with multi-architecture support (amd64/arm64)
- [X] T005 [P1] [US1] [P] Create WiX installer definition at `wix/Package.wxs` for amd64 with component definitions for binaries and shortcuts (alternative to Inno Setup)
- [X] T006 [P1] [US1] [P] Create WiX installer definition at `wix/Package-arm64.wxs` for arm64 architecture (alternative to Inno Setup)
- [X] T007 [P1] [US1] Create PowerShell build script at `scripts/build-windows-installer.ps1` that builds Go binaries and generates installers for both architectures
- [X] T008 [P1] [US1] Add Start Menu shortcut component in Inno Setup script or WiX Package.wxs targeting mcpproxy-tray.exe
- [X] T009 [P1] [US1] Configure in-place upgrade logic in installer (Inno Setup: AppId; WiX: UpgradeCode) to replace binaries while preserving user data
- [X] T010 [P1] [US1] Add uninstall registry entries in installer definitions for "Add or Remove Programs" integration
- [ ] T011 [P1] [US1] Test installer locally on Windows 10 21H2+ VM (amd64) - verify installation, shortcuts, and uninstallation

**Checkpoint**: At this point, User Story 1 should be fully functional - installer installs binaries, creates shortcuts, and uninstalls cleanly

---

## Phase 4: User Story 2 - PATH Configuration for CLI Access (Priority: P1) 🎯 MVP

**Goal**: Automatically configure system-level PATH during installation so `mcpproxy` command works immediately in new Command Prompt windows

**Independent Test**: Install mcpproxy, open new Command Prompt, run `mcpproxy --version` without manual configuration - command should execute successfully

### Implementation for User Story 2

- [X] T012 [P1] [US2] Add system PATH modification to Inno Setup script using `[Registry]` section with `NeedsAddPath()` check function
- [X] T013 [P1] [US2] Add system PATH modification to WiX Package.wxs using `Environment` component with `Action="set"` and `Part="last"`
- [X] T014 [P1] [US2] Implement `NeedsAddPath()` function in Inno Setup `[Code]` section to prevent duplicate PATH entries on upgrades
- [X] T015 [P1] [US2] Add PATH validation in installer to ensure final length < 2047 characters (Windows limit)
- [ ] T016 [P1] [US2] Test PATH configuration on Windows 10 VM - verify `mcpproxy --version` works in new Command Prompt after installation
- [ ] T017 [P1] [US2] Test upgrade scenario - install v1, install v2, verify PATH contains single entry (no duplicates)

**Checkpoint**: At this point, User Stories 1 AND 2 should both work independently - installer installs binaries and configures PATH automatically

---

## Phase 5: User Story 3 - Post-Installation Launch Option (Priority: P2)

**Goal**: Provide checkbox on final installer screen to launch mcpproxy-tray immediately after installation completes

**Independent Test**: Run installer, check "Launch MCPProxy Tray" checkbox at final screen, click Finish, verify tray application starts and appears in system tray

### Implementation for User Story 3

- [X] T018 [P2] [US3] Add `[Run]` section to Inno Setup script with `postinstall` flag to launch mcpproxy-tray.exe
- [X] T019 [P2] [US3] Add WiX custom action in Package.wxs to launch mcpproxy-tray.exe after installation with `Execute="immediate"` and UI condition
- [X] T020 [P2] [US3] Configure post-install action to skip launching if installer runs in silent mode (`/VERYSILENT` for Inno; `/qn` for WiX)
- [ ] T021 [P2] [US3] Test post-install launch on Windows VM - verify tray application starts and core server launches automatically
- [ ] T022 [P2] [US3] Test silent installation mode - verify tray does not launch when using `/VERYSILENT` (Inno) or `/qn` (WiX)

**Checkpoint**: All P1 and P2 user stories should now be independently functional - installer provides full user experience

---

## Phase 6: User Story 4 - Informational Screens (Priority: P3)

**Goal**: Display welcome and completion screens with Windows-specific information, system requirements, and quick start instructions

**Independent Test**: Run installer and read through welcome and conclusion screens to verify all information is accurate and uses Windows conventions

### Implementation for User Story 4

- [ ] T023 [P3] [US4] [P] Convert macOS `scripts/installer-resources/welcome_en.rtf` to Windows format at `scripts/installer-resources/windows/welcome.rtf` with Windows-specific paths and terminology
- [ ] T024 [P3] [US4] [P] Convert macOS `scripts/installer-resources/conclusion_en.rtf` to Windows format at `scripts/installer-resources/windows/conclusion.rtf` with Command Prompt examples
- [ ] T025 [P3] [US4] [P] Create optional installer banner image at `scripts/installer-resources/windows/banner.bmp` (493x58 pixels) for WiX UI customization
- [ ] T026 [P3] [US4] [P] Create optional installer dialog image at `scripts/installer-resources/windows/dialog.bmp` (493x312 pixels) for WiX UI customization
- [ ] T027 [P3] [US4] Configure Inno Setup to display welcome.rtf using `InfoBeforeFile` directive
- [ ] T028 [P3] [US4] Configure Inno Setup to display conclusion.rtf using `InfoAfterFile` directive
- [ ] T029 [P3] [US4] Configure WiX Package.wxs to display custom UI with welcome screen using `WixUI_Advanced` dialog set
- [ ] T030 [P3] [US4] Update RTF files to include system requirements: Windows 10 version 21H2 or Windows 11, 100 MB disk space, port 8080 available
- [ ] T031 [P3] [US4] Update conclusion.rtf to include Windows-specific quick start with `%USERPROFILE%\.mcpproxy` path conventions
- [ ] T032 [P3] [US4] Test installer screens on Windows VM - verify RTF rendering and Windows-specific paths display correctly

**Checkpoint**: All user stories including polish should now be independently functional - installer provides professional user experience

---

## Phase 7: User Story 5 - CI/CD Automation and Release Artifacts (Priority: P1) 🎯 MVP

**Goal**: Automate Windows installer builds in GitHub Actions workflows, uploading artifacts to releases page for every tagged release

**Independent Test**: Create test tag on feature branch, verify workflow runs, check installer artifacts are generated and available for download

### Implementation for User Story 5

- [ ] T033 [P1] [US5] Modify `.github/workflows/release.yml` to add Windows installer build job using `windows-latest` runner
- [ ] T034 [P1] [US5] Add WiX Toolset installation step in release.yml using `dotnet tool install --global wix` command
- [ ] T035 [P1] [US5] Add Inno Setup installation step in release.yml using `choco install innosetup -y` (if using Inno Setup)
- [ ] T036 [P1] [US5] Add Windows amd64 binary build step in release.yml with CGO configuration for both mcpproxy and mcpproxy-tray
- [ ] T037 [P1] [US5] Add Windows arm64 binary build step in release.yml with cross-compilation settings
- [ ] T038 [P1] [US5] Add installer generation step in release.yml calling build script with version from Git tag
- [ ] T039 [P1] [US5] Add artifact upload step in release.yml to attach installers to GitHub release with naming convention `mcpproxy-{version}-windows-{arch}-installer.{ext}`
- [ ] T040 [P1] [US5] Test release workflow by pushing test tag to feature branch and verifying artifact uploads
- [ ] T041 [P1] [US5] Verify installer artifacts appear on GitHub releases page with correct naming and are downloadable

**Checkpoint**: At this point, CI/CD automation should be fully functional - tagged releases automatically build and upload installers

---

## Phase 8: User Story 6 - Testing Without Main Branch Release (Priority: P2)

**Goal**: Enable developers to test installers locally or via prerelease workflow without creating production releases on main branch

**Independent Test**: Run local build script on development branch, copy installer to Windows VM, install and verify functionality with prerelease version string

### Implementation for User Story 6

- [X] T042 [P2] [US6] Enhance PowerShell build script `scripts/build-windows-installer.ps1` to accept `-Version` parameter for local versioning
- [ ] T043 [P2] [US6] Add local testing instructions to quickstart.md showing how to build installer with custom version string
- [ ] T044 [P2] [US6] Modify `.github/workflows/prerelease.yml` to add Windows installer build job (same steps as release.yml)
- [ ] T045 [P2] [US6] Configure prerelease workflow to use version format `{last_tag}-next.{commit_hash}` for prerelease installers
- [ ] T046 [P2] [US6] Add prerelease artifact upload step to attach installers to workflow runs (not releases page)
- [ ] T047 [P2] [US6] Test prerelease workflow by pushing to `next` branch and downloading installer from workflow artifacts
- [ ] T048 [P2] [US6] Test local build workflow on Windows VM - build installer, uninstall previous version, install new version, verify version string
- [ ] T049 [P2] [US6] Document uninstall/reinstall workflow in quickstart.md for iterative testing

**Checkpoint**: All user stories should now be independently functional - developers can iterate quickly without production releases

---

## Phase 9: Polish & Documentation

**Purpose**: Improvements that affect multiple user stories and documentation updates

- [X] T050 [P] Update `CLAUDE.md` with Windows installer build instructions including WiX/Inno Setup prerequisites
- [X] T051 [P] Update `CLAUDE.md` with local testing workflow for Windows VMs
- [X] T052 [P] Update repository README.md with Windows installation instructions referencing GitHub releases page
- [X] T053 [P] Add inline comments to Inno Setup script explaining multi-architecture logic and upgrade handling
- [X] T054 [P] Add inline comments to WiX Package.wxs explaining component IDs and upgrade codes
- [X] T055 [P] Add error handling in PowerShell build script for missing dependencies (Go, WiX, Inno Setup)
- [X] T056 [P] Add build time logging in PowerShell script to report installer generation success/failure
- [ ] T057 Run full quickstart.md validation on Windows VM - follow all steps and verify accuracy
- [ ] T058 Security review - verify installer does not modify security defaults (localhost binding, quarantine, API key)
- [ ] T059 Performance validation - verify installer build completes within 5 minutes in GitHub Actions
- [ ] T060 Final end-to-end test - install on clean Windows 10 VM, verify all acceptance scenarios from spec.md

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: SKIPPED - No blocking prerequisites for installer build tooling
- **User Stories (Phase 3-8)**: All can start immediately after Phase 1
  - User stories can proceed in parallel (if multiple developers available)
  - Or sequentially in priority order (P1 → P2 → P3)
- **Polish (Phase 9)**: Depends on all desired user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Setup - No dependencies on other stories
- **User Story 2 (P1)**: Can start after Setup - No dependencies (PATH configuration is independent of shortcuts)
- **User Story 3 (P2)**: Can start after Setup - Optional enhancement to US1, but independently testable
- **User Story 4 (P3)**: Can start after Setup - No dependencies (informational screens don't affect functionality)
- **User Story 5 (P1)**: Should start after US1 + US2 implementation complete (needs working installer to automate)
- **User Story 6 (P2)**: Should start after US5 complete (extends CI/CD workflow for prerelease testing)

### Recommended Implementation Order

1. **Phase 1 (Setup)** → Directory structure ready
2. **Phase 3 (US1)** + **Phase 4 (US2)** → MVP installer with binaries + PATH (can parallelize T004-T011 and T012-T017)
3. **Phase 7 (US5)** → Automate CI/CD (depends on working installer from US1+US2)
4. **Phase 5 (US3)** → Add post-install launch (optional enhancement)
5. **Phase 8 (US6)** → Enable local testing (extends CI/CD from US5)
6. **Phase 6 (US4)** → Add informational screens (polish)
7. **Phase 9 (Polish)** → Documentation and final validation

### Within Each User Story

- **US1**: Tasks T004-T006 can run in parallel (Inno Setup vs WiX alternatives), then T007-T011 sequentially
- **US2**: Tasks T012-T013 can run in parallel (Inno Setup vs WiX), then T014-T017 sequentially
- **US3**: Tasks run sequentially (T018-T022)
- **US4**: Tasks T023-T026 can run in parallel (RTF conversion + images), then T027-T032 sequentially
- **US5**: Tasks run sequentially (T033-T041) - each step builds on previous
- **US6**: Tasks T042-T043 can run in parallel with T044-T046, then T047-T049 sequentially
- **Polish**: Most tasks (T050-T056) can run in parallel, then T057-T060 sequentially

### Parallel Opportunities

- **Setup Phase**: All tasks (T001-T003) can run in parallel
- **US1 Implementation**: T004-T006 can run in parallel (Inno Setup vs WiX choices)
- **US2 Implementation**: T012-T013 can run in parallel (installer framework choices)
- **US4 Implementation**: T023-T026 can run in parallel (RTF and image creation)
- **Polish Phase**: T050-T056 can run in parallel (documentation and script improvements)

---

## Parallel Example: User Story 1 + User Story 2 (MVP)

```bash
# These can be launched in parallel (different files):
Task T004: "Create Inno Setup installer script at scripts/installer.iss"
Task T005: "Create WiX installer definition at wix/Package.wxs"
Task T006: "Create WiX installer definition at wix/Package-arm64.wxs"
Task T012: "Add system PATH modification to Inno Setup script"
Task T013: "Add system PATH modification to WiX Package.wxs"

# Once installer scripts exist, build script can be created:
Task T007: "Create PowerShell build script at scripts/build-windows-installer.ps1"

# After build script exists, testing can proceed:
Task T011: "Test installer locally on Windows 10 VM"
Task T016: "Test PATH configuration on Windows 10 VM"
```

---

## Implementation Strategy

### MVP First (User Stories 1 + 2 Only)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 3: User Story 1 - Basic Installation (T004-T011)
3. Complete Phase 4: User Story 2 - PATH Configuration (T012-T017)
4. **STOP and VALIDATE**: Test installer on Windows VM - verify binaries install, shortcuts work, PATH configured
5. Deploy/demo if ready (this is MVP - P1 user stories complete)

### Incremental Delivery

1. Complete Setup → Directory structure ready
2. Add User Story 1 + User Story 2 → Test independently → **MVP!** (P1 complete)
3. Add User Story 5 → Test CI/CD automation → **Automated releases!**
4. Add User Story 3 → Test post-install launch → **Enhanced UX**
5. Add User Story 6 → Test prerelease workflow → **Faster iteration**
6. Add User Story 4 → Test informational screens → **Professional polish**
7. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup together (T001-T003)
2. Once Setup is done:
   - **Developer A**: User Story 1 - Basic Installation (T004-T011)
   - **Developer B**: User Story 2 - PATH Configuration (T012-T017)
   - **Developer C**: User Story 4 - Informational Screens (T023-T032)
3. After US1 + US2 complete:
   - **Developer A**: User Story 5 - CI/CD Automation (T033-T041)
   - **Developer B**: User Story 3 - Post-Install Launch (T018-T022)
   - **Developer C**: User Story 6 - Testing Workflow (T042-T049)
4. Stories complete and integrate independently

---

## Technology Choice Decision

Based on research.md findings, the project offers two installer implementations:

### Recommended: Inno Setup (Primary)
- **Tasks**: T004, T012, T018, T027-T028 (Inno Setup specific)
- **Pros**: Single multi-arch installer, simple script syntax, fast learning curve
- **Use case**: Best for initial implementation and open-source distribution

### Alternative: WiX Toolset 4.x
- **Tasks**: T005-T006, T013, T019, T029 (WiX specific)
- **Pros**: Industry-standard MSI format, enterprise IT support, Group Policy deployment
- **Use case**: Optional for enterprise deployments or future enhancement

**Implementation Strategy**: Implement Inno Setup first (T004, T012, T018, T027-T028) for MVP. Optionally implement WiX in parallel if team capacity allows. Both can coexist - users choose based on environment needs.

---

## Notes

- [P] tasks = different files, no dependencies, can run in parallel
- [Story] label maps task to specific user story for traceability (e.g., [US1], [US2])
- Each user story should be independently completable and testable
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- User Story 1 + User Story 2 = MVP (P1 priorities)
- User Story 5 is also P1 but depends on US1+US2 being functional first
- Windows VM testing required for validation - cannot be fully automated in v1
- Unsigned installers will trigger Windows Defender SmartScreen warnings (expected and documented)
- Future enhancement: Add automated E2E installer tests using PowerShell in GitHub Actions
