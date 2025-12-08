# Research: Release Notes Generator

**Date**: 2025-12-08
**Feature**: 010-release-notes-generator

## Research Questions

### 1. Claude API Authentication for CI/CD

**Question**: Can CLAUDE_CODE_OAUTH_TOKEN be used with the Claude API in GitHub Actions?

**Decision**: NO - Use ANTHROPIC_API_KEY only

**Rationale**:
- `CLAUDE_CODE_OAUTH_TOKEN` (sk-ant-...) is restricted to Claude Code CLI only
- Attempting to use it with the API returns: "This credential is only authorized for use with Claude Code"
- `ANTHROPIC_API_KEY` from Anthropic Console is required for programmatic API access

**Alternatives Considered**:
| Option | Feasibility | Notes |
|--------|-------------|-------|
| CLAUDE_CODE_OAUTH_TOKEN | NOT POSSIBLE | CLI-only, returns auth error |
| ANTHROPIC_API_KEY | SELECTED | Works with all API endpoints |
| Claude Agent SDK | Overkill | Adds unnecessary dependencies |

**Action Required**: User must obtain ANTHROPIC_API_KEY from console.anthropic.com and add as GitHub secret.

---

### 2. API Call Method

**Question**: What's the best way to call Claude API from GitHub Actions?

**Decision**: curl + jq (no SDK dependencies)

**Rationale**:
- GitHub Actions runners have curl and jq pre-installed
- No Python/Node setup step required
- Faster workflow execution (no dependency installation)
- Simpler error handling with bash

**Alternatives Considered**:
| Option | Pros | Cons | Decision |
|--------|------|------|----------|
| curl + jq | No deps, fast | Manual JSON handling | SELECTED |
| Anthropic Python SDK | Type safety | Requires pip install step | Rejected |
| Anthropic Node SDK | Type safety | Requires npm install step | Rejected |
| anthropics/claude-code-action | Official GH Action | Designed for code review, not text gen | Rejected |

**Implementation**:
```bash
curl -s https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{...}'
```

---

### 3. Input Data Collection

**Question**: What data should be sent to Claude for release notes generation?

**Decision**: Commit messages only (not full diffs)

**Rationale**:
- Commit messages contain "what" and "why" - sufficient for release notes
- Full diffs can be megabytes for large releases
- Commit messages: ~50-200 chars each, 500 commits ≈ 50KB (~12K tokens)
- Claude's 200K token context easily handles hundreds of commits

**Git Commands**:
```bash
# Get previous tag
PREV_TAG=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")

# Collect commit messages (exclude merge commits)
if [ -z "$PREV_TAG" ]; then
  COMMITS=$(git log --pretty=format:"- %s" --no-merges | head -200)
else
  COMMITS=$(git log ${PREV_TAG}..HEAD --pretty=format:"- %s" --no-merges | head -200)
fi
```

**Filtering Strategy**:
- `--no-merges`: Exclude merge commits
- `head -200`: Truncate to prevent context overflow
- Post-filter in prompt: Skip chore/docs/test commits

---

### 4. Model Selection

**Question**: Which Claude model should be used?

**Decision**: claude-sonnet-4-5-20250929 (default)

**Rationale**:
- Sufficient quality for release notes (structured text generation)
- Faster response time (~5-10 seconds vs ~15-30 for Opus)
- Lower cost per request
- Opus available as configurable option for complex changelogs

| Model | Speed | Cost | Quality | Recommendation |
|-------|-------|------|---------|----------------|
| claude-sonnet-4-5-20250929 | Fast | Lower | Good | DEFAULT |
| claude-opus-4-5-20251101 | Slow | Higher | Excellent | OPTIONAL |
| claude-haiku-4-5-20251001 | Very Fast | Lowest | Basic | NOT RECOMMENDED |

---

### 5. Output Length Control

**Question**: How to ensure concise release notes?

**Decision**: Three-layer approach

**Implementation**:

1. **API Parameter** (hard limit):
   ```json
   { "max_tokens": 1024 }
   ```
   Caps output at ~750 words.

2. **Prompt Engineering** (soft guidance):
   ```
   Generate CONCISE release notes (maximum 400 words).
   Focus only on user-facing changes.
   Skip internal refactoring and dependency updates.
   Group by: New Features, Bug Fixes, Breaking Changes.
   ```

3. **Post-Processing** (safety net):
   ```bash
   NOTES=$(echo "$RESPONSE" | jq -r '.content[0].text' | head -c 4000)
   ```

---

### 6. Error Handling Strategy

**Question**: How to handle API failures without blocking releases?

**Decision**: Graceful fallback with warning

**Implementation**:
```bash
RESPONSE=$(curl -s --max-time 30 ... || echo '{"error": "timeout"}')
NOTES=$(echo "$RESPONSE" | jq -r '.content[0].text // empty')

if [ -z "$NOTES" ]; then
  NOTES="Release notes could not be generated automatically. See commit history for changes."
  echo "::warning::Claude API call failed, using fallback message"
fi
```

**Principle**: Never block a release due to notes generation failure.

---

### 7. Workflow Integration

**Question**: Where in the release workflow should notes generation happen?

**Decision**: New job before `release` job

**Rationale**:
- Generate notes early while builds are running (parallel)
- Pass notes to release job via outputs
- Allows notes to be available for installer jobs

**Workflow Structure**:
```yaml
jobs:
  generate-notes:     # NEW - runs immediately
    runs-on: ubuntu-latest
    outputs:
      notes: ${{ steps.generate.outputs.notes }}

  build:              # EXISTING - runs in parallel with generate-notes
    ...

  release:            # EXISTING - depends on both
    needs: [build, generate-notes]
    ...
```

---

### 8. Release Notes File Persistence

**Question**: How to store release notes for installer inclusion?

**Decision**: Save as artifact, commit to releases/ directory

**Implementation**:
1. Generate notes in `generate-notes` job
2. Save to `RELEASE_NOTES-{version}.md`
3. Upload as artifact
4. Download in build jobs for installer inclusion
5. Optionally commit to repository (separate step)

**File Location Options**:
| Option | Pros | Cons | Decision |
|--------|------|------|----------|
| `releases/RELEASE_NOTES-v1.0.0.md` | Per-version files, easy history | Many files over time | SELECTED |
| `CHANGELOG.md` (prepend) | Single file, standard | Complex merge handling | Alternative |
| `docs/releases/` | Organized | Non-standard location | Rejected |

---

### 9. DMG Installer Integration

**Question**: How to include release notes in macOS DMG?

**Decision**: Add file to DMG temp directory before creation

**Implementation** (modify `scripts/create-dmg.sh`):
```bash
# Add after creating TEMP_DIR
if [ -f "RELEASE_NOTES.md" ]; then
  cp "RELEASE_NOTES.md" "$TEMP_DIR/"
  echo "✅ Release notes included in DMG"
fi
```

**Location in DMG**: Root level alongside Applications symlink.

---

### 10. Windows Installer Integration

**Question**: How to include release notes in Windows installer?

**Decision**: Install to documentation folder via Inno Setup

**Implementation** (modify `inno/mcpproxy.iss`):
```iss
[Files]
Source: "{#SourceDir}\RELEASE_NOTES.md"; DestDir: "{app}\docs"; Flags: ignoreversion
```

**Post-install location**: `C:\Program Files\MCPProxy\docs\RELEASE_NOTES.md`

---

## Summary of Decisions

| Question | Decision |
|----------|----------|
| API Authentication | ANTHROPIC_API_KEY (not CLAUDE_CODE_OAUTH_TOKEN) |
| API Call Method | curl + jq |
| Input Data | Commit messages only (max 200) |
| Model | claude-sonnet-4-5-20250929 |
| Output Control | max_tokens + prompt + post-process |
| Error Handling | Graceful fallback, never block |
| Workflow Position | New job before release |
| File Storage | Per-version files in releases/ |
| DMG Integration | Copy to temp dir |
| Windows Integration | Inno Setup [Files] section |

## Prerequisites for Implementation

1. **Required**: Add `ANTHROPIC_API_KEY` as GitHub repository secret
2. **Optional**: Configure model via workflow input (default: sonnet)
3. **Testing**: Use `workflow_dispatch` to test without pushing tag
