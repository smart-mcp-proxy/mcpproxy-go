# Feature Specification: Release Notes Generator

**Feature Branch**: `010-release-notes-generator`
**Created**: 2025-12-08
**Status**: Draft
**Input**: User description: "Add release notes generator into release CI workflow. Generator must fetch commit messages (diffs) starting from previous git tag and generate using LLM concise release notes, it must be placed on top of release page in github. If possible commit file with notes and include in dmg installer and windows installer. Requires research on how to use Claude SDK for notes generation with subscription API key."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Automated Release Notes on Tag Push (Priority: P1)

When a maintainer pushes a new version tag (e.g., `v1.2.0`), the release CI workflow automatically generates human-readable release notes by analyzing all commits since the previous tag and publishes them to the GitHub release page.

**Why this priority**: This is the core value proposition - eliminating manual release note writing while ensuring every release has professional, consistent documentation.

**Independent Test**: Can be fully tested by pushing a version tag to a test repository and verifying that the GitHub release page shows AI-generated release notes summarizing the changes.

**Acceptance Scenarios**:

1. **Given** a repository with tags `v1.0.0` and multiple commits after it, **When** maintainer pushes tag `v1.1.0`, **Then** the release page for `v1.1.0` displays generated release notes with categorized changes (features, fixes, breaking changes).

2. **Given** a first release with no previous tags, **When** maintainer pushes tag `v1.0.0`, **Then** the release page displays generated release notes summarizing all commits from repository inception.

3. **Given** a release workflow is triggered, **When** the AI service is unavailable or returns an error, **Then** the workflow continues with a fallback message indicating notes could not be generated, and the release is still created with standard download links.

---

### User Story 2 - Release Notes File in Repository (Priority: P2)

Generated release notes are committed as a file to the repository, creating a permanent record of changes that can be referenced, searched, and included in installers.

**Why this priority**: Provides traceability and enables inclusion in installers. Secondary to the core release page functionality.

**Independent Test**: Can be tested by triggering a release and verifying a `RELEASE_NOTES.md` file (or similar) is committed to the repository with the generated content.

**Acceptance Scenarios**:

1. **Given** a successful release notes generation, **When** the release workflow completes, **Then** a file containing the release notes is committed to a designated location in the repository.

2. **Given** a release notes file already exists for a previous version, **When** a new release is created, **Then** the new release notes are added (prepended or as a separate file per version) without overwriting previous notes.

---

### User Story 3 - Release Notes in macOS DMG Installer (Priority: P3)

The generated release notes file is included in the macOS DMG installer package, so users can view changes before or after installation.

**Why this priority**: Enhances user experience for macOS users but depends on P1 and P2 being complete first.

**Independent Test**: Can be tested by downloading the DMG from a release, mounting it, and verifying a release notes file is present alongside the application.

**Acceptance Scenarios**:

1. **Given** a release notes file has been generated and committed, **When** the DMG installer is created, **Then** the release notes file is included in the DMG contents visible to the user.

---

### User Story 4 - Release Notes in Windows Installer (Priority: P3)

The generated release notes file is included in the Windows installer package, accessible to users during or after installation.

**Why this priority**: Enhances user experience for Windows users but depends on P1 and P2 being complete first.

**Independent Test**: Can be tested by running the Windows installer and verifying the release notes are accessible (either shown during installation or installed to a documentation folder).

**Acceptance Scenarios**:

1. **Given** a release notes file has been generated and committed, **When** the Windows installer is built, **Then** the release notes file is included and accessible to the user post-installation.

---

### Edge Cases

- What happens when there are no commits between tags? System generates a note indicating "No changes since previous release."
- How does the system handle merge commits vs. regular commits? System analyzes all commit messages regardless of type, but filters out automated/CI commits (e.g., "Merge branch", "Bump version").
- What happens if the AI generates inappropriate or incorrect content? Release notes are added to the release page; maintainers can manually edit them via GitHub UI after the fact.
- What if a release is re-run (workflow_dispatch)? System regenerates notes based on current commit history between tags.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST fetch all commit messages between the current tag and the previous tag when a version tag is pushed.
- **FR-002**: System MUST send commit messages to the Claude API for release notes generation using the Anthropic Messages API.
- **FR-003**: System MUST use an API key stored as a GitHub repository secret for authentication with Claude API.
- **FR-004**: System MUST include the generated release notes in the GitHub release body, positioned before the existing download links and installation instructions.
- **FR-005**: System MUST handle first releases (no previous tag) by analyzing all commits from repository inception.
- **FR-006**: System MUST gracefully handle API failures by continuing the release with a fallback message and not blocking the release process.
- **FR-007**: System SHOULD commit the generated release notes to a file in the repository for permanent record-keeping.
- **FR-008**: System SHOULD include the release notes file in the macOS DMG installer package.
- **FR-009**: System SHOULD include the release notes file in the Windows installer package.
- **FR-010**: System MUST filter out automated/CI commit messages (merge commits, version bumps) from the analysis to focus on meaningful changes.
- **FR-011**: System MUST generate release notes in markdown format with categorized sections (Summary, New Features, Bug Fixes, Breaking Changes).

### Key Entities

- **Release**: A versioned software release triggered by a git tag, containing binaries, installers, and documentation.
- **Commit Message**: A text description of a code change, categorized by convention (feat:, fix:, chore:, etc.).
- **Release Notes**: A human-readable summary of changes in a release, categorized by type and formatted in markdown.
- **API Secret**: A secure credential stored in GitHub repository settings, used for authenticating with external services.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every tagged release has release notes visible on the GitHub release page within the normal release workflow duration.
- **SC-002**: Generated release notes accurately reflect at least 90% of significant changes (features and fixes) based on commit messages.
- **SC-003**: Release workflow completion time increases by no more than 60 seconds due to release notes generation.
- **SC-004**: Zero release failures are caused by the release notes generation feature (graceful degradation on errors).
- **SC-005**: Maintainers spend zero time manually writing release notes for standard releases.

## Assumptions

- The repository uses conventional commit message format (feat:, fix:, chore:, etc.) for most commits, enabling accurate categorization.
- The Anthropic API (Claude) will be available and responsive during release workflows.
- An ANTHROPIC_API_KEY (not CLAUDE_CODE_OAUTH_TOKEN) will be configured as a GitHub repository secret, as subscription OAuth tokens cannot be used with the Claude API.
- The existing release workflow structure (jobs, artifacts, signing) remains largely unchanged.
- Commit messages contain sufficient context for meaningful release note generation.
- The claude-sonnet-4-5-20250929 or claude-opus-4-5-20251101 model will be used for generation (cost-effective yet capable).

## Out of Scope

- Interactive editing or approval workflow for release notes before publishing.
- Multi-language release notes generation.
- Integration with external changelog management tools (e.g., semantic-release, conventional-changelog).
- Automatic version number determination based on commit types.
- Release notes for pre-release/alpha/beta tags (can be added later).

## Implementation Notes

### Approach: Simple API Call (Not Agentic)

This feature uses a **single-pass API call** to Claude, not an agentic loop:

| Option | Use Case | Decision |
|--------|----------|----------|
| **curl + Messages API** | Single-pass text generation | **Selected** |
| Anthropic Python/Node SDK | Same as curl, typed bindings | Alternative |
| Claude Agent SDK | Multi-step reasoning, tool use | Overkill for this task |
| Claude Code binary | Interactive coding sessions | Wrong tool |

**Rationale**: Release notes generation is a straightforward transform: commits → prompt → API → text. No iteration, tool use, or complex reasoning required.

### Input Data Collection

**Use commit messages, NOT full diffs:**

```bash
# Get previous tag
PREV_TAG=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")

# Collect commit messages (exclude merge commits)
if [ -z "$PREV_TAG" ]; then
  COMMITS=$(git log --pretty=format:"- %s" --no-merges)
else
  COMMITS=$(git log ${PREV_TAG}..HEAD --pretty=format:"- %s" --no-merges)
fi
```

**Why commit messages only:**
- Commit messages: ~50-200 chars each, 500 commits ≈ 50KB (~12K tokens)
- Full diffs: Can be megabytes for large releases
- Commit messages contain the "what" and "why" - sufficient for release notes
- Claude's context (200K tokens) easily handles hundreds of commits

### Handling Large Input (Edge Case)

If a release has an unusually large number of commits:

```bash
# Truncate to most recent 200 commits (safety limit)
COMMITS=$(git log ${PREV_TAG}..HEAD --pretty=format:"- %s" --no-merges | head -200)

# Note truncation in output if needed
TOTAL=$(git rev-list ${PREV_TAG}..HEAD --count)
if [ "$TOTAL" -gt 200 ]; then
  COMMITS="$COMMITS\n\n(Showing 200 most recent of $TOTAL commits)"
fi
```

**Typical sizes:**
- 50 commits × 100 chars = 5KB (trivial)
- 500 commits × 100 chars = 50KB (comfortable)
- Truncation threshold: 200 commits (conservative safety margin)

### Controlling Output Length

Three mechanisms ensure concise output:

1. **`max_tokens` parameter** (hard limit):
   ```json
   {
     "model": "claude-sonnet-4-5-20250929",
     "max_tokens": 1024
   }
   ```
   Caps output at ~750 words.

2. **Prompt engineering** (soft guidance):
   ```
   Generate CONCISE release notes (maximum 400 words).
   Focus only on user-facing changes.
   Skip internal refactoring and dependency updates.
   Group by: New Features, Bug Fixes, Breaking Changes.
   ```

3. **Post-processing** (safety net):
   ```bash
   # Truncate if somehow too long
   NOTES=$(echo "$RESPONSE" | jq -r '.content[0].text' | head -c 4000)
   ```

### API Call Structure

```bash
curl -s https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-5-20250929",
    "max_tokens": 1024,
    "messages": [{
      "role": "user",
      "content": "Generate concise release notes for VERSION...\n\nCommits:\n- feat: ...\n- fix: ..."
    }]
  }'
```

### Model Selection

| Model | Speed | Cost | Recommendation |
|-------|-------|------|----------------|
| `claude-sonnet-4-5-20250929` | Fast | Lower | **Default choice** |
| `claude-opus-4-5-20251101` | Slower | Higher | For complex changelogs |

**Recommended**: `claude-sonnet-4-5-20250929` - sufficient quality for release notes at lower cost and faster response.

### Error Handling Strategy

```bash
RESPONSE=$(curl -s ... || echo '{"error": "network"}')
NOTES=$(echo "$RESPONSE" | jq -r '.content[0].text // empty')

if [ -z "$NOTES" ]; then
  # Fallback - don't block release
  NOTES="Release notes could not be generated automatically. See commit history for changes."
  echo "::warning::Claude API call failed, using fallback"
fi
```

**Principle**: Never block a release due to notes generation failure.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
