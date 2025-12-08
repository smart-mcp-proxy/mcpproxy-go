# Data Model: Release Notes Generator

**Date**: 2025-12-08
**Feature**: 010-release-notes-generator

## Overview

This feature has minimal data modeling requirements as it operates within GitHub Actions workflows with ephemeral storage. No persistent database or state management is needed.

## Entities

### 1. Release Notes (Ephemeral)

Generated text content that flows through the workflow as outputs and artifacts.

**Attributes**:
| Field | Type | Description |
|-------|------|-------------|
| version | string | Git tag (e.g., "v1.2.0") |
| content | string | Markdown-formatted release notes |
| generated_at | timestamp | ISO 8601 timestamp |
| model | string | Claude model used (e.g., "claude-sonnet-4-5-20250929") |
| commit_count | integer | Number of commits analyzed |
| truncated | boolean | Whether commits were truncated (>200) |

**Lifecycle**:
1. Created in `generate-notes` job
2. Passed via GitHub Actions outputs to `release` job
3. Optionally saved as artifact for installer jobs
4. Optionally committed to repository

### 2. Commit Message (Input)

Raw commit data extracted from git history.

**Attributes**:
| Field | Type | Description |
|-------|------|-------------|
| subject | string | First line of commit message |
| hash | string | Short commit hash (optional) |
| type | string | Conventional commit type (feat/fix/chore/etc.) |

**Collection**: `git log --pretty=format:"- %s" --no-merges`

### 3. API Request (Transient)

Request payload sent to Claude API.

**Structure**:
```json
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": "Generate release notes for {version}...\n\nCommits:\n{commits}"
    }
  ]
}
```

### 4. API Response (Transient)

Response from Claude API.

**Structure**:
```json
{
  "id": "msg_...",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "## What's New in v1.2.0\n\n..."
    }
  ],
  "model": "claude-sonnet-4-5-20250929",
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 1234,
    "output_tokens": 567
  }
}
```

## Data Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│                        GitHub Actions Workflow                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────────┐ │
│  │  Git Tags   │───▶│ Commit Msgs │───▶│ Claude API Request      │ │
│  │ (v1.1→v1.2) │    │ (filtered)  │    │ (JSON payload)          │ │
│  └─────────────┘    └─────────────┘    └───────────┬─────────────┘ │
│                                                    │                │
│                                                    ▼                │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────────┐ │
│  │ GitHub      │◀───│ Release     │◀───│ Claude API Response     │ │
│  │ Release     │    │ Notes       │    │ (markdown text)         │ │
│  └─────────────┘    └──────┬──────┘    └─────────────────────────┘ │
│                            │                                        │
│                            ▼                                        │
│              ┌─────────────────────────────┐                       │
│              │ Artifacts (optional)        │                       │
│              │ - RELEASE_NOTES-v1.2.0.md   │                       │
│              │ - Included in DMG/EXE       │                       │
│              └─────────────────────────────┘                       │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

## Storage

### GitHub Actions Outputs (Primary)

```yaml
jobs:
  generate-notes:
    outputs:
      notes: ${{ steps.generate.outputs.notes }}
      version: ${{ steps.generate.outputs.version }}
```

### GitHub Actions Artifacts (Optional)

```yaml
- uses: actions/upload-artifact@v4
  with:
    name: release-notes
    path: RELEASE_NOTES-${{ github.ref_name }}.md
```

### Repository File (Optional)

```
releases/
├── RELEASE_NOTES-v1.0.0.md
├── RELEASE_NOTES-v1.1.0.md
└── RELEASE_NOTES-v1.2.0.md
```

## State Transitions

This feature has no persistent state. Each workflow run is stateless:

```
[Tag Push] → [Generate Notes] → [Create Release] → [Done]
                   │
                   └── On failure: [Fallback Message] → [Create Release] → [Done]
```

## Validation Rules

### Input Validation

| Rule | Implementation |
|------|----------------|
| Version format | Must match `v*` pattern (enforced by workflow trigger) |
| Commit count | Max 200 commits (truncation with warning) |
| API key present | Required secret, fail-fast if missing |

### Output Validation

| Rule | Implementation |
|------|----------------|
| Non-empty notes | Fallback message if empty |
| Max length | Truncate to 4000 chars (safety) |
| Valid markdown | Trust Claude output (manual edit if needed) |
