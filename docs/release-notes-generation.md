# Release Notes Generation

MCPProxy uses AI-powered release notes generation to create human-readable release summaries automatically when new versions are tagged.

## Overview

When a version tag is pushed (e.g., `v1.0.5`), the release workflow:
1. Collects commit messages since the previous tag
2. Sends them to Claude API for summarization
3. Generates categorized release notes (Features, Fixes, Breaking Changes, Improvements)
4. Adds the notes to the GitHub release page
5. Includes the notes file in DMG and Windows installers

## Prerequisites

### GitHub Secret

Add `ANTHROPIC_API_KEY` to your GitHub repository secrets:

1. Go to Repository Settings > Secrets and variables > Actions
2. Click "New repository secret"
3. Name: `ANTHROPIC_API_KEY`
4. Value: Your API key from https://console.anthropic.com/

**Cost**: Estimated ~$0.01-0.05 per release (claude-sonnet-4-5-20250929 model)

## How It Works

### Workflow Integration

The `generate-notes` job in `.github/workflows/release.yml`:

```yaml
generate-notes:
  runs-on: ubuntu-latest
  if: startsWith(github.ref, 'refs/tags/v')
  outputs:
    notes: ${{ steps.generate.outputs.notes }}
    notes_file: ${{ steps.generate.outputs.notes_file }}
  steps:
    - uses: actions/checkout@v4
      with:
        fetch-depth: 0  # Full history for git log
    # ... generate notes with Claude API
```

### Input Data

- **Source**: Git commit messages (not full diffs)
- **Format**: `git log --pretty=format:"- %s" --no-merges`
- **Filtering**: Merge commits excluded
- **Limit**: 200 most recent commits (prevents token overflow)

### Output Format

Generated notes follow this structure:

```markdown
## What's New in v1.0.5

Brief 1-2 sentence summary of this release.

### New Features
- Feature description with user benefit

### Bug Fixes
- Fix description

### Breaking Changes
- Change requiring user action

### Improvements
- Enhancement to existing features
```

## Local Testing

Test release notes generation before pushing tags:

```bash
# Set your API key
export ANTHROPIC_API_KEY="your-key-here"

# Generate notes for a specific version
./scripts/generate-release-notes.sh v1.0.5

# Generate notes for current HEAD (auto-detect version)
./scripts/generate-release-notes.sh

# Customize behavior
MAX_TOKENS=512 ./scripts/generate-release-notes.sh v1.0.5
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | (required) | Claude API key |
| `CLAUDE_MODEL` | `claude-sonnet-4-5-20250929` | Model to use |
| `MAX_TOKENS` | `1024` | Maximum output tokens |
| `MAX_COMMITS` | `200` | Maximum commits to include |
| `API_TIMEOUT` | `30` | API timeout in seconds |

## Installer Integration

### macOS DMG

Release notes are automatically included when available:
- File placed in DMG root: `RELEASE_NOTES.md`
- Visible when DMG is mounted alongside Applications symlink

### Windows Installer

Release notes are installed to documentation folder:
- Location: `{app}\docs\RELEASE_NOTES.md`
- Only included if release notes artifact exists

## Error Handling

### API Failure

If the Claude API call fails, the release continues with a fallback message:

```markdown
## What's New in v1.0.5

Release notes could not be generated automatically.
Please see the commit history for detailed changes.
```

**Releases are never blocked by API failures.**

### Common Issues

| Issue | Solution |
|-------|----------|
| "API key not set" | Add `ANTHROPIC_API_KEY` to GitHub Secrets |
| "Rate limit exceeded" | Wait and retry, or use a different model |
| "Timeout" | Increase `API_TIMEOUT` or reduce `MAX_COMMITS` |
| "Empty response" | Check API key validity at console.anthropic.com |

## Prompt Engineering

The prompt instructs Claude to:
- Keep notes under 400 words
- Group by: New Features, Bug Fixes, Breaking Changes, Improvements
- Skip internal changes (chore:, docs:, test:, ci:)
- Focus on user benefits, not implementation details
- Use bullet points for each change

### Customization

To modify the prompt, edit the `build_prompt()` function in `scripts/generate-release-notes.sh`.

## Workflow Timing

Release notes generation adds approximately:
- **10-20 seconds** to the release workflow
- Runs in parallel with build jobs
- Does not block artifact uploads

## Security Notes

- API key is stored as GitHub Secret (encrypted)
- Never logged or exposed in workflow output
- Token masking applied in error messages
- No sensitive code/data sent to API (only commit messages)

## Troubleshooting

### View Generation Logs

In GitHub Actions:
1. Go to Actions > Release workflow
2. Click on the failed/completed run
3. Expand "Generate release notes" step

### Test Locally

```bash
# Verbose output
set -x
./scripts/generate-release-notes.sh v1.0.5

# Check curl response
curl -v https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-5-20250929","max_tokens":100,"messages":[{"role":"user","content":"Hello"}]}'
```

## Related Documentation

- [Release Process](releasing.md) - Full release workflow
- [Prerelease Builds](prerelease-builds.md) - Next branch releases
- [GitHub Environments](github-environments.md) - Environment configuration
