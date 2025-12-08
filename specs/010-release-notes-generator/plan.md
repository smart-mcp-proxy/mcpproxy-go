# Implementation Plan: Release Notes Generator

**Branch**: `010-release-notes-generator` | **Date**: 2025-12-08 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/010-release-notes-generator/spec.md`

## Summary

Add automated AI-generated release notes to the existing GitHub Actions release workflow. When a version tag is pushed, the workflow fetches commit messages since the previous tag, sends them to Claude API via curl, and prepends the generated notes to the GitHub release body. The feature includes graceful fallback on API errors and optional inclusion of release notes in macOS DMG and Windows installers.

## Technical Context

**Language/Version**: Bash (GitHub Actions), Go 1.25 (existing project)
**Primary Dependencies**: curl, jq, GitHub Actions, Anthropic Messages API
**Storage**: N/A (ephemeral workflow artifacts only)
**Testing**: Manual testing via workflow_dispatch, E2E validation via tag push
**Target Platform**: GitHub Actions runners (ubuntu-latest, macos-14, windows-latest)
**Project Type**: CI/CD workflow modification (existing `.github/workflows/release.yml`)
**Performance Goals**: Release notes generation < 30 seconds, total workflow impact < 60 seconds
**Constraints**: No external Python/Node dependencies (curl-only), graceful degradation required
**Scale/Scope**: Single workflow file modification, 2 installer script updates

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Compliance | Notes |
|-----------|------------|-------|
| I. Performance at Scale | N/A | Not applicable to CI workflow |
| II. Actor-Based Concurrency | N/A | Not applicable to CI workflow |
| III. Configuration-Driven Architecture | PASS | API key stored in GitHub Secrets, model configurable |
| IV. Security by Default | PASS | API key in secrets, no credentials in code |
| V. Test-Driven Development | PARTIAL | Manual workflow testing (no unit tests for bash) |
| VI. Documentation Hygiene | PASS | Will update CLAUDE.md with release notes process |

**Architecture Constraints:**
| Constraint | Compliance | Notes |
|------------|------------|-------|
| Core + Tray Split | N/A | CI workflow, not core application |
| Event-Driven Updates | N/A | CI workflow, not core application |
| DDD Layering | N/A | CI workflow, not core application |
| Upstream Client Modularity | N/A | CI workflow, not core application |

**Development Workflow:**
| Rule | Compliance | Notes |
|------|------------|-------|
| Pre-Commit Quality Gates | PASS | Linting for bash scripts via shellcheck |
| Error Handling Standards | PASS | Graceful fallback on API errors |
| Git Commit Discipline | PASS | Conventional commits |
| Branch Strategy | PASS | Feature branch → main |

**GATE RESULT**: PASS - No constitution violations. Proceed to Phase 0.

## Project Structure

### Documentation (this feature)

```text
specs/010-release-notes-generator/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output (minimal - no persistent data)
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (API contract)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
.github/workflows/
└── release.yml          # MODIFY: Add generate-notes job

scripts/
├── create-dmg.sh        # MODIFY: Include RELEASE_NOTES.md in DMG
├── build-windows-installer.ps1  # MODIFY: Include RELEASE_NOTES.md
└── generate-release-notes.sh    # NEW: Standalone script for local testing

docs/
└── release-notes-generation.md  # NEW: Document the feature (optional)
```

**Structure Decision**: Minimal changes to existing workflow structure. Single new job in release.yml, minor modifications to existing installer scripts.

## Complexity Tracking

> No constitution violations requiring justification.

| Item | Complexity | Justification |
|------|------------|---------------|
| curl + jq approach | LOW | No external dependencies, runs on all GH Actions runners |
| Fallback handling | LOW | Simple if-else with default message |
| Installer integration | MEDIUM | Requires coordination between jobs via artifacts |
