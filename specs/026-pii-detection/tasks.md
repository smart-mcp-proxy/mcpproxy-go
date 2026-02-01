# Tasks: Sensitive Data Detection

**Input**: Design documents from `/specs/026-pii-detection/`
**Prerequisites**: plan.md ✓, spec.md ✓, research.md ✓, data-model.md ✓, contracts/ ✓

**Tests**: Comprehensive unit tests + E2E tests required
**Documentation**: Docusaurus documentation in `docs/` required

**Organization**: Tasks grouped by user story for independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Backend**: `internal/` at repository root
- **Frontend**: `frontend/src/` at repository root
- **CLI**: `cmd/mcpproxy/commands/` at repository root
- **Tests**: `*_test.go` in same package (Go convention)
- **E2E Tests**: `internal/server/e2e_test.go` and `scripts/test-api-e2e.sh`
- **Documentation**: `docs/` (Docusaurus format)

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the `internal/security/` package structure and base types

- [x] T001 Create `internal/security/` package directory structure
- [x] T002 [P] Define core types (Severity, Category, Detection, SensitiveDataResult) in `internal/security/types.go`
- [x] T003 [P] Define DetectionPattern type and interface in `internal/security/pattern.go`
- [x] T004 [P] Add SensitiveDataDetectionConfig to `internal/config/config.go`
- [x] T005 Add DefaultSensitiveDataConfig() function in `internal/config/config.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core detection engine that ALL user stories depend on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Core Implementation

- [x] T006 Implement SensitiveDataDetector struct with Scan() method in `internal/security/detector.go`
- [x] T007 [P] Implement Shannon entropy calculation in `internal/security/entropy.go`
- [x] T008 [P] Implement Luhn credit card validation in `internal/security/luhn.go`
- [x] T009 [P] Implement cross-platform path normalization in `internal/security/paths.go`
- [x] T010 Create patterns subdirectory structure `internal/security/patterns/`
- [x] T011 Inject Detector into ActivityService in `internal/runtime/activity_service.go`
- [x] T012 Add async detection hook to handleToolCallCompleted() in `internal/runtime/activity_service.go`
- [x] T013 Add updateActivityMetadata() method for async detection results in `internal/storage/activity.go`

### Comprehensive Unit Tests for Foundation

- [x] T014 [P] Write table-driven unit tests for Detector.Scan() with edge cases in `internal/security/detector_test.go`
- [x] T015 [P] Write unit tests for ShannonEntropy() with various character sets in `internal/security/entropy_test.go`
- [x] T016 [P] Write unit tests for LuhnValid() with all card types and separators in `internal/security/luhn_test.go`
- [x] T017 [P] Write unit tests for path expansion on Windows/Linux/macOS in `internal/security/paths_test.go`
- [x] T018 [P] Write unit tests for config loading and defaults in `internal/config/config_test.go`

**Checkpoint**: Foundation ready - detector integrated with ActivityService

---

## Phase 3: User Story 1 - Detect Secrets in Tool Call Data (Priority: P1) 🎯 MVP

**Goal**: Detect AWS keys, GitHub tokens, private keys, Stripe keys in tool call arguments and responses

**Independent Test**: Execute tool call with `AKIAIOSFODNN7EXAMPLE`, verify Activity Log shows "aws_access_key" detection

### Comprehensive Unit Tests for User Story 1

- [x] T019 [P] [US1] Write table-driven tests for AWS credential patterns (all prefix variants) in `internal/security/patterns/cloud_test.go`
- [x] T020 [P] [US1] Write table-driven tests for GCP API key patterns in `internal/security/patterns/cloud_test.go`
- [x] T021 [P] [US1] Write table-driven tests for Azure credential patterns in `internal/security/patterns/cloud_test.go`
- [x] T022 [P] [US1] Write table-driven tests for RSA/EC/DSA private key patterns in `internal/security/patterns/keys_test.go`
- [x] T023 [P] [US1] Write table-driven tests for OpenSSH/PGP key patterns in `internal/security/patterns/keys_test.go`
- [x] T024 [P] [US1] Write table-driven tests for GitHub token patterns (PAT/OAuth/App) in `internal/security/patterns/tokens_test.go`
- [x] T025 [P] [US1] Write table-driven tests for GitLab token patterns in `internal/security/patterns/tokens_test.go`
- [x] T026 [P] [US1] Write table-driven tests for Stripe/Slack/SendGrid patterns in `internal/security/patterns/tokens_test.go`
- [x] T027 [P] [US1] Write table-driven tests for JWT token patterns in `internal/security/patterns/tokens_test.go`
- [x] T028 [P] [US1] Write table-driven tests for database connection strings in `internal/security/patterns/database_test.go`
- [x] T029 [P] [US1] Write table-driven tests for high-entropy detection thresholds in `internal/security/patterns/entropy_test.go`
- [x] T030 [P] [US1] Write tests for known example detection (is_likely_example flag) in `internal/security/detector_test.go`
- [x] T031 [P] [US1] Write integration test for end-to-end secret detection in `internal/security/detector_integration_test.go`

### Implementation for User Story 1

- [x] T032 [P] [US1] Implement cloud credential patterns (AWS, GCP, Azure) in `internal/security/patterns/cloud.go`
- [x] T033 [P] [US1] Implement private key patterns (RSA, EC, DSA, OpenSSH, PGP, PKCS8) in `internal/security/patterns/keys.go`
- [x] T034 [P] [US1] Implement API token patterns (GitHub, GitLab, Stripe, Slack, OpenAI, Anthropic) in `internal/security/patterns/tokens.go`
- [x] T035 [P] [US1] Implement JWT and auth token patterns in `internal/security/patterns/tokens.go`
- [x] T036 [P] [US1] Implement database connection string patterns (MySQL, Postgres, MongoDB, Redis) in `internal/security/patterns/database.go`
- [x] T037 [US1] Implement high-entropy string detection in `internal/security/patterns/entropy.go`
- [x] T038 [US1] Load all built-in patterns in Detector.loadBuiltinPatterns() in `internal/security/detector.go`
- [x] T039 [US1] Add known example detection (AKIAIOSFODNN7EXAMPLE → is_likely_example) in `internal/security/detector.go`

**Checkpoint**: Secret detection works end-to-end, visible in Activity Log metadata

---

## Phase 4: User Story 2 - Detect Sensitive File Path Access (Priority: P1)

**Goal**: Detect access to SSH keys, AWS credentials, .env files across Windows/Linux/macOS

**Independent Test**: Execute tool call with `{"path": "~/.ssh/id_rsa"}`, verify "sensitive_file_path" detection

### Comprehensive Unit Tests for User Story 2

- [x] T040 [P] [US2] Write table-driven tests for SSH key paths (Linux) in `internal/security/patterns/files_test.go`
- [x] T041 [P] [US2] Write table-driven tests for SSH key paths (macOS) in `internal/security/patterns/files_test.go`
- [x] T042 [P] [US2] Write table-driven tests for SSH key paths (Windows) in `internal/security/patterns/files_test.go`
- [x] T043 [P] [US2] Write table-driven tests for cloud credential paths (AWS/GCP/Azure/Kube) in `internal/security/patterns/files_test.go`
- [x] T044 [P] [US2] Write table-driven tests for env file patterns (.env, .env.*) in `internal/security/patterns/files_test.go`
- [x] T045 [P] [US2] Write table-driven tests for auth token files (.npmrc, .pypirc, etc.) in `internal/security/patterns/files_test.go`
- [x] T046 [P] [US2] Write table-driven tests for system sensitive files (/etc/shadow, SAM) in `internal/security/patterns/files_test.go`
- [x] T047 [P] [US2] Write tests for path normalization with environment variables in `internal/security/paths_test.go`
- [x] T048 [P] [US2] Write tests for case sensitivity handling (Windows vs Linux) in `internal/security/paths_test.go`

### Implementation for User Story 2

- [x] T049 [P] [US2] Implement SSH key path patterns in `internal/security/patterns/files.go`
- [x] T050 [P] [US2] Implement cloud credential path patterns (AWS, GCP, Azure, Kube) in `internal/security/patterns/files.go`
- [x] T051 [P] [US2] Implement environment file patterns (.env, secrets.json, appsettings.json) in `internal/security/patterns/files.go`
- [x] T052 [P] [US2] Implement auth token file patterns (.npmrc, .pypirc, .docker/config.json) in `internal/security/patterns/files.go`
- [x] T053 [P] [US2] Implement system file patterns (/etc/shadow, SAM, Keychains) in `internal/security/patterns/files.go`
- [x] T054 [US2] Integrate file path patterns with Detector.scanFilePaths() in `internal/security/detector.go`
- [x] T055 [US2] Add platform detection for OS-specific path matching in `internal/security/paths.go`

**Checkpoint**: File path detection works on all platforms

---

## Phase 5: User Story 3 - View and Filter Detection Results (Priority: P1)

**Goal**: Filter Activity Log by sensitive data presence, type, and severity via REST API and Web UI

**Independent Test**: Filter Activity Log by "sensitive_data=true&severity=critical", verify only relevant records

### Comprehensive Unit Tests for User Story 3

- [x] T056 [P] [US3] Write API handler unit tests for sensitive_data filter in `internal/httpapi/activity_handlers_test.go`
- [x] T057 [P] [US3] Write API handler unit tests for detection_type filter in `internal/httpapi/activity_handlers_test.go`
- [x] T058 [P] [US3] Write API handler unit tests for severity filter in `internal/httpapi/activity_handlers_test.go`
- [x] T059 [P] [US3] Write API handler unit tests for combined filters in `internal/httpapi/activity_handlers_test.go`
- [x] T060 [P] [US3] Write unit tests for ActivityResponse extension fields in `internal/httpapi/activity_handlers_test.go`

### Implementation for User Story 3

- [x] T061 [US3] Add sensitive_data, detection_type, severity query params to ActivityQueryParams in `internal/httpapi/activity_handlers.go`
- [x] T062 [US3] Implement filter logic in listActivities handler in `internal/httpapi/activity_handlers.go`
- [x] T063 [US3] Add has_sensitive_data, detection_types, max_severity to ActivityResponse in `internal/httpapi/activity_handlers.go`
- [x] T064 [US3] Update OpenAPI spec with new query parameters in `oas/swagger.yaml`
- [x] T065 [P] [US3] Create ActivitySensitiveData.vue component in `frontend/src/components/ActivitySensitiveData.vue`
- [x] T066 [P] [US3] Add sensitive data indicator column to ActivityLogView in `frontend/src/views/ActivityLogView.vue`
- [x] T067 [US3] Add detection filter controls to ActivityLogView in `frontend/src/views/ActivityLogView.vue`
- [x] T068 [US3] Add detection details to activity expanded view in `frontend/src/views/ActivityLogView.vue`

**Checkpoint**: Web UI displays and filters sensitive data detections

---

## Phase 6: User Story 4 - CLI Sensitive Data Visibility (Priority: P2)

**Goal**: Show sensitive data detection in CLI activity list and show commands

**Independent Test**: Run `mcpproxy activity list --sensitive-data`, verify SENSITIVE indicator column

### Comprehensive Unit Tests for User Story 4

- [x] T069 [P] [US4] Write CLI unit tests for activity list table output with SENSITIVE column in `cmd/mcpproxy/commands/activity_test.go`
- [x] T070 [P] [US4] Write CLI unit tests for --sensitive-data flag parsing in `cmd/mcpproxy/commands/activity_test.go`
- [x] T071 [P] [US4] Write CLI unit tests for --detection-type flag parsing in `cmd/mcpproxy/commands/activity_test.go`
- [x] T072 [P] [US4] Write CLI unit tests for --severity flag parsing in `cmd/mcpproxy/commands/activity_test.go`
- [x] T073 [P] [US4] Write CLI unit tests for activity show detection details in `cmd/mcpproxy/commands/activity_test.go`
- [x] T074 [P] [US4] Write CLI unit tests for JSON/YAML output with detection data in `cmd/mcpproxy/commands/activity_test.go`

### Implementation for User Story 4

- [x] T075 [US4] Add SENSITIVE indicator column to activity list table output in `cmd/mcpproxy/commands/activity.go`
- [x] T076 [US4] Add --sensitive-data flag to activity list command in `cmd/mcpproxy/commands/activity.go`
- [x] T077 [US4] Add --detection-type and --severity flags in `cmd/mcpproxy/commands/activity.go`
- [x] T078 [US4] Display detection details in activity show command in `cmd/mcpproxy/commands/activity.go`
- [x] T079 [US4] Include detection data in JSON/YAML output modes in `cmd/mcpproxy/commands/activity.go`

**Checkpoint**: CLI shows sensitive data detection results

---

## Phase 7: User Story 5 - Configure Custom Detection Patterns (Priority: P3)

**Goal**: Allow users to add custom regex patterns and keywords via configuration

**Independent Test**: Add `{"name": "acme_key", "regex": "ACME-KEY-[a-f0-9]{32}"}` to config, verify detection

### Comprehensive Unit Tests for User Story 5

- [x] T080 [P] [US5] Write table-driven tests for custom pattern loading in `internal/security/patterns/custom_test.go`
- [x] T081 [P] [US5] Write tests for invalid regex validation and error messages in `internal/security/patterns/custom_test.go`
- [x] T082 [P] [US5] Write tests for keyword matching (case-sensitivity) in `internal/security/patterns/custom_test.go`
- [x] T083 [P] [US5] Write tests for custom pattern severity levels in `internal/security/patterns/custom_test.go`
- [x] T084 [P] [US5] Write tests for hot-reload of custom patterns in `internal/security/detector_test.go`

### Implementation for User Story 5

- [x] T085 [US5] Implement custom pattern loading from config in `internal/security/patterns/custom.go`
- [x] T086 [US5] Add regex validation with error reporting on startup in `internal/security/patterns/custom.go`
- [x] T087 [US5] Implement keyword pattern matching in `internal/security/patterns/custom.go`
- [x] T088 [US5] Integrate custom patterns with Detector in `internal/security/detector.go`
- [x] T089 [US5] Add hot-reload support for custom patterns on config change in `internal/security/detector.go`

**Checkpoint**: Custom patterns work and reload without restart

---

## Phase 8: User Story 6 - Detect Credit Card Numbers (Priority: P3)

**Goal**: Detect credit card numbers with Luhn validation

**Independent Test**: Execute tool call with `4111111111111111`, verify "credit_card" detection

### Comprehensive Unit Tests for User Story 6

- [x] T090 [P] [US6] Write table-driven tests for Visa card patterns in `internal/security/patterns/creditcard_test.go`
- [x] T091 [P] [US6] Write table-driven tests for Mastercard patterns in `internal/security/patterns/creditcard_test.go`
- [x] T092 [P] [US6] Write table-driven tests for Amex/Discover patterns in `internal/security/patterns/creditcard_test.go`
- [x] T093 [P] [US6] Write tests for card numbers with various separators in `internal/security/patterns/creditcard_test.go`
- [x] T094 [P] [US6] Write tests for invalid Luhn numbers (false positives) in `internal/security/patterns/creditcard_test.go`
- [x] T095 [P] [US6] Write tests for known test card detection in `internal/security/patterns/creditcard_test.go`

### Implementation for User Story 6

- [x] T096 [US6] Implement credit card pattern with Luhn validation in `internal/security/patterns/creditcard.go`
- [x] T097 [US6] Handle various separators (spaces, dashes) in `internal/security/patterns/creditcard.go`
- [x] T098 [US6] Add known test card detection (4111111111111111 → is_likely_example) in `internal/security/patterns/creditcard.go`

**Checkpoint**: Credit cards detected with <5% false positive rate

---

## Phase 9: E2E Tests

**Purpose**: End-to-end tests covering full detection flow through REST API and MCP protocol

### E2E Test Implementation

- [x] T099 [P] Add E2E test: secret detection via MCP tool call in `internal/server/e2e_test.go`
- [x] T100 [P] Add E2E test: file path detection via MCP tool call in `internal/server/e2e_test.go`
- [x] T101 [P] Add E2E test: REST API activity filter by sensitive_data in `internal/server/e2e_test.go`
- [x] T102 [P] Add E2E test: REST API activity filter by severity in `internal/server/e2e_test.go`
- [x] T103 [P] Add E2E test: detection metadata in activity response in `internal/server/e2e_test.go`
- [x] T104 [P] Add E2E test: custom pattern detection in `internal/server/e2e_test.go`
- [x] T105 [P] Add E2E test: credit card detection with Luhn validation in `internal/server/e2e_test.go`
- [x] T106 [P] Add E2E test: high-entropy string detection in `internal/server/e2e_test.go`
- [x] T107 [P] Add E2E test: is_likely_example flag for test values in `internal/server/e2e_test.go`
- [x] T108 Add E2E test scenarios to `scripts/test-api-e2e.sh` for sensitive data detection
- [x] T109 Add E2E test for SSE event emission on detection in `internal/server/e2e_test.go`

**Checkpoint**: All E2E tests pass with `./scripts/test-api-e2e.sh`

---

## Phase 10: Documentation (Docusaurus)

**Purpose**: Comprehensive documentation in Docusaurus format for `docs/` directory

### Feature Documentation

- [x] T110 Create main feature documentation in `docs/features/sensitive-data-detection.md` with:
  - Overview and security context
  - Supported detection types with examples
  - Detection categories and severities
  - Activity Log integration
  - Web UI usage guide
  - CLI usage guide
  - Performance considerations

- [x] T111 [P] Add configuration documentation in `docs/configuration/sensitive-data-detection.md` with:
  - Full config schema with examples
  - Category enable/disable options
  - Custom patterns configuration
  - Sensitive keywords configuration
  - Entropy threshold tuning

- [x] T112 [P] Add CLI reference documentation in `docs/cli/sensitive-data-commands.md` with:
  - `activity list --sensitive-data` usage
  - `activity list --detection-type` usage
  - `activity list --severity` usage
  - `activity show` detection details
  - JSON/YAML output examples

### API Documentation

- [x] T113 Update REST API documentation in `docs/api/rest-api.md` with:
  - New query parameters for `/api/v1/activity`
  - Detection metadata in responses
  - Filter examples

- [x] T114 Update MCP protocol documentation in `docs/api/mcp-protocol.md` with:
  - Detection in tool call metadata
  - sensitive_data.detected SSE event

### Cross-Platform Documentation

- [x] T115 [P] Add cross-platform file paths reference in `docs/features/sensitive-data-detection.md` with:
  - Windows paths table (%USERPROFILE%, %APPDATA%, etc.)
  - Linux paths table (~/.ssh/, /etc/, etc.)
  - macOS paths table (Library/, Keychains/, etc.)
  - Path normalization behavior

### Security Documentation

- [x] T116 [P] Add security best practices in `docs/features/sensitive-data-detection.md` with:
  - Tool Poisoning Attack detection use case
  - Exfiltration detection patterns
  - Compliance audit workflows
  - Simon Willison's "Lethal Trifecta" context

### Update Existing Documentation

- [x] T117 Update `docs/features/activity-log.md` with sensitive_data_detection metadata section
- [x] T118 Update `docs/web-ui/activity-log.md` with detection filter UI documentation
- [x] T119 Update `docs/intro.md` to mention sensitive data detection feature
- [x] T120 Update sidebar in `docs/` to include new pages

---

## Phase 11: Polish & Cross-Cutting Concerns

**Purpose**: Events, code quality, and final validation

### Event Integration

- [x] T121 [P] Add sensitive_data.detected event emission in `internal/runtime/activity_service.go`
- [x] T122 [P] Register event type in `internal/runtime/events.go`

### Code Quality & Documentation

- [x] T123 [P] Update CLAUDE.md with sensitive data detection section
- [x] T124 [P] Update README.md with configuration examples and feature overview

### Final Validation

- [x] T125 Run full unit test suite with `go test ./internal/... -v`
- [x] T126 Run E2E tests with `./scripts/test-api-e2e.sh`
- [x] T127 Run linter with `./scripts/run-linter.sh`
- [x] T128 Verify OpenAPI coverage with `./scripts/verify-oas-coverage.sh`
- [x] T129 Validate quickstart.md scenarios manually
- [x] T130 Review all documentation for completeness and accuracy

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-8)**: All depend on Foundational phase completion
  - US1 (Secrets) and US2 (File Paths) can proceed in parallel
  - US3 (Filtering) depends on US1 or US2 being complete (needs detection data)
  - US4 (CLI) depends on US3 (uses same filters)
  - US5 (Custom Patterns) can proceed independently after Foundation
  - US6 (Credit Cards) can proceed independently after Foundation
- **E2E Tests (Phase 9)**: Depends on US1-US4 completion (core functionality)
- **Documentation (Phase 10)**: Can start after US1, completed after all stories
- **Polish (Phase 11)**: Depends on all user stories and E2E tests being complete

### User Story Dependencies

```
Foundation (Phase 2)
    ├── US1 (Secrets) ─────┬── US3 (Filtering) ── US4 (CLI)
    ├── US2 (File Paths) ──┘
    ├── US5 (Custom Patterns) [Independent]
    └── US6 (Credit Cards) [Independent]
            │
            ▼
    E2E Tests (Phase 9)
            │
            ▼
    Documentation (Phase 10) ←── Can start partially after US1
            │
            ▼
    Polish (Phase 11)
```

### Within Each User Story

- Tests written FIRST (TDD per constitution)
- Pattern implementations in parallel
- Integration tasks after patterns complete
- Story checkpoint before moving on

### Parallel Opportunities

**Phase 1 (Setup)**: T002, T003, T004 can run in parallel
**Phase 2 (Foundation)**: T007-T009 (entropy, luhn, paths) + T014-T018 (tests) in parallel
**US1**: T019-T031 (tests) in parallel, then T032-T036 (patterns) in parallel
**US2**: T040-T048 (tests) in parallel, then T049-T053 (patterns) in parallel
**US3**: T056-T060 (API tests) in parallel, T065-T066 (UI components) in parallel
**US5-US6**: Can run in parallel as independent stories
**E2E**: T099-T107 can run in parallel (different test files)
**Docs**: T111-T116 can run in parallel (different files)

---

## Parallel Example: User Story 1

```bash
# Launch all tests for User Story 1 together (13 test tasks):
Task: "Write table-driven tests for AWS credential patterns in internal/security/patterns/cloud_test.go"
Task: "Write table-driven tests for GCP API key patterns in internal/security/patterns/cloud_test.go"
Task: "Write table-driven tests for Azure credential patterns in internal/security/patterns/cloud_test.go"
Task: "Write table-driven tests for RSA/EC/DSA private key patterns in internal/security/patterns/keys_test.go"
# ... etc.

# After tests exist, launch all patterns in parallel:
Task: "Implement cloud credential patterns in internal/security/patterns/cloud.go"
Task: "Implement private key patterns in internal/security/patterns/keys.go"
Task: "Implement API token patterns in internal/security/patterns/tokens.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL - blocks all stories)
3. Complete Phase 3: User Story 1 (Secret Detection)
4. **STOP and VALIDATE**: Test secret detection independently
5. Deploy/demo if ready - users can see secrets in Activity Log metadata

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 (Secrets) → Test → Deploy/Demo (MVP!)
3. Add User Story 2 (File Paths) → Test → Deploy/Demo
4. Add User Story 3 (Filtering) → Test → Deploy/Demo (major UX improvement)
5. Add User Story 4 (CLI) → Test → Deploy/Demo
6. Add User Story 5 (Custom Patterns) → Test → Deploy/Demo
7. Add User Story 6 (Credit Cards) → Test → Deploy/Demo (PCI compliance)
8. Complete E2E Tests → Validate full integration
9. Complete Documentation → Publish to docs site
10. Each story adds value without breaking previous stories

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: User Story 1 (Secrets)
   - Developer B: User Story 2 (File Paths)
   - Developer C: User Story 5 or 6 (independent)
3. After US1 or US2 complete:
   - Developer A or B: User Story 3 (Filtering)
   - Developer C: Start Documentation (Phase 10)
4. After US3 complete:
   - Developer: User Story 4 (CLI)
5. After US1-US4 complete:
   - All: E2E Tests and Documentation finalization

---

## Test Coverage Requirements

### Unit Test Coverage Targets

| Package | Target | Notes |
|---------|--------|-------|
| `internal/security` | 90% | Core detection logic |
| `internal/security/patterns` | 95% | All patterns must have tests |
| `internal/httpapi` (activity handlers) | 85% | Filter logic |
| `cmd/mcpproxy/commands` (activity) | 80% | CLI flags and output |

### E2E Test Scenarios

| Scenario | Description |
|----------|-------------|
| Secret Detection | AWS key → detected in activity |
| File Path Detection | ~/.ssh/id_rsa → detected |
| REST API Filtering | ?sensitive_data=true works |
| CLI Filtering | --sensitive-data flag works |
| Custom Patterns | User-defined regex detected |
| Credit Cards | Luhn validation works |
| SSE Events | Detection triggers event |

---

## Documentation Checklist

| Document | Status | Owner |
|----------|--------|-------|
| `docs/features/sensitive-data-detection.md` | New | T110 |
| `docs/configuration/sensitive-data-detection.md` | New | T111 |
| `docs/cli/sensitive-data-commands.md` | New | T112 |
| `docs/api/rest-api.md` | Update | T113 |
| `docs/api/mcp-protocol.md` | Update | T114 |
| `docs/features/activity-log.md` | Update | T117 |
| `docs/web-ui/activity-log.md` | Update | T118 |
| `docs/intro.md` | Update | T119 |
| `CLAUDE.md` | Update | T123 |
| `README.md` | Update | T124 |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing (TDD)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Pattern files can be developed in parallel within a story
- E2E tests validate full integration before documentation
- Documentation uses Docusaurus format with frontmatter
- Avoid: vague tasks, same file conflicts, cross-story dependencies that break independence
