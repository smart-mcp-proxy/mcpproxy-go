# Contract: GitHub Actions Workflow Interface

**Feature**: 010-release-notes-generator
**File**: `.github/workflows/release.yml`

## New Job: generate-notes

### Job Definition

```yaml
generate-notes:
  runs-on: ubuntu-latest
  if: startsWith(github.ref, 'refs/tags/v')
  outputs:
    notes: ${{ steps.generate.outputs.notes }}
    notes_file: ${{ steps.generate.outputs.notes_file }}
```

### Inputs (from workflow trigger)

| Input | Source | Example |
|-------|--------|---------|
| `github.ref` | Git tag | `refs/tags/v1.2.0` |
| `github.ref_name` | Tag name | `v1.2.0` |
| `github.repository` | Repo | `smart-mcp-proxy/mcpproxy-go` |

### Secrets Required

| Secret | Required | Description |
|--------|----------|-------------|
| `ANTHROPIC_API_KEY` | Yes | Claude API authentication key |

### Outputs

| Output | Type | Description |
|--------|------|-------------|
| `notes` | string | Generated release notes (markdown) |
| `notes_file` | string | Path to notes artifact file |

### Steps

```yaml
steps:
  - name: Checkout
    uses: actions/checkout@v4
    with:
      fetch-depth: 0  # Full history for git log

  - name: Get previous tag
    id: prev_tag
    run: |
      PREV_TAG=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")
      echo "tag=$PREV_TAG" >> $GITHUB_OUTPUT

  - name: Collect commits
    id: commits
    run: |
      # ... collect and format commits

  - name: Generate release notes
    id: generate
    env:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
    run: |
      # ... call Claude API and set outputs

  - name: Save notes artifact
    uses: actions/upload-artifact@v4
    with:
      name: release-notes
      path: RELEASE_NOTES-${{ github.ref_name }}.md
```

## Modified Job: build

### Changes

Add artifact download step for installer integration:

```yaml
build:
  needs: [generate-notes]  # ADD dependency
  # ... existing config ...

  steps:
    # ... existing steps ...

    - name: Download release notes
      uses: actions/download-artifact@v4
      with:
        name: release-notes
        path: .
      continue-on-error: true  # Don't fail if notes unavailable
```

## Modified Job: release

### Changes

Update `needs` and use generated notes in release body:

```yaml
release:
  needs: [build, generate-notes]  # ADD generate-notes
  # ... existing config ...

  steps:
    # ... existing steps ...

    - name: Create release with binaries
      uses: softprops/action-gh-release@v2
      with:
        files: release-files/*
        body: |
          ${{ needs.generate-notes.outputs.notes }}

          ---

          ## mcpproxy ${{ github.ref_name }}

          ... existing download links and instructions ...
```

## Artifact Flow

```
┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│  generate-notes  │────▶│      build       │────▶│     release      │
│                  │     │                  │     │                  │
│ outputs.notes ───┼─────┼──────────────────┼─────┼▶ release body    │
│                  │     │                  │     │                  │
│ artifact: ───────┼─────┼▶ download ───────┼─────┼▶ (optional)      │
│ release-notes    │     │  RELEASE_NOTES.md│     │                  │
└──────────────────┘     └──────────────────┘     └──────────────────┘
```

## Error Handling Contract

### API Key Missing

```yaml
- name: Check API key
  if: env.ANTHROPIC_API_KEY == ''
  run: |
    echo "::warning::ANTHROPIC_API_KEY not configured, skipping release notes generation"
    echo "notes=Release notes not available (API key not configured)" >> $GITHUB_OUTPUT
```

### API Call Failure

```yaml
- name: Generate release notes
  id: generate
  run: |
    RESPONSE=$(curl -s --max-time 30 ... || echo '{"error":"timeout"}')
    NOTES=$(echo "$RESPONSE" | jq -r '.content[0].text // empty')

    if [ -z "$NOTES" ]; then
      echo "::warning::Failed to generate release notes, using fallback"
      NOTES="Release notes could not be generated automatically. See commit history for changes."
    fi

    echo "notes<<EOF" >> $GITHUB_OUTPUT
    echo "$NOTES" >> $GITHUB_OUTPUT
    echo "EOF" >> $GITHUB_OUTPUT
```

### Artifact Download Failure

```yaml
- name: Download release notes
  uses: actions/download-artifact@v4
  with:
    name: release-notes
  continue-on-error: true  # Non-blocking
```

## Environment Variables

| Variable | Scope | Description |
|----------|-------|-------------|
| `ANTHROPIC_API_KEY` | generate-notes | API authentication |
| `GITHUB_TOKEN` | release | GitHub API access |
| `GITHUB_REF` | all | Current git ref |
| `GITHUB_REF_NAME` | all | Tag name without refs/tags/ |

## Concurrency

```yaml
# Existing workflow concurrency (unchanged)
concurrency:
  group: release-${{ github.ref }}
  cancel-in-progress: false
```

## Permissions

```yaml
permissions:
  contents: write  # Existing - for release creation
```

No additional permissions required.
