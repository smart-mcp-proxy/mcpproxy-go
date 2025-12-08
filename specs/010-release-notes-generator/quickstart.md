# Quickstart: Release Notes Generator

**Feature**: 010-release-notes-generator

## Prerequisites

1. **Anthropic API Key**: Obtain from [console.anthropic.com](https://console.anthropic.com)
2. **GitHub Repository Access**: Admin access to add secrets

## Setup (One-Time)

### Step 1: Add API Key to GitHub Secrets

1. Go to repository **Settings** → **Secrets and variables** → **Actions**
2. Click **New repository secret**
3. Name: `ANTHROPIC_API_KEY`
4. Value: Your API key (starts with `sk-ant-api...`)
5. Click **Add secret**

### Step 2: Verify Workflow Changes

After merging this feature branch, the release workflow will automatically:
- Generate release notes on tag push
- Include notes in GitHub release body
- Include notes in DMG/Windows installers

## Usage

### Automatic (Default)

Push a version tag to trigger automated release notes:

```bash
git tag v1.2.0
git push origin v1.2.0
```

The release page will show AI-generated notes at the top.

### Manual Testing

Use workflow_dispatch to test without creating a release:

1. Go to **Actions** → **Release** workflow
2. Click **Run workflow**
3. Select branch and enter test tag
4. Check workflow logs for generated notes

## Customization

### Change Model

Edit `.github/workflows/release.yml`:

```yaml
env:
  CLAUDE_MODEL: claude-opus-4-5-20251101  # For complex releases
```

### Adjust Output Length

Modify the prompt in `generate-notes` job:

```yaml
# In the curl request body
"content": "Generate release notes (maximum 600 words)..."
```

### Skip Certain Commits

Update the commit filter:

```bash
# Exclude bot commits
git log --pretty=format:"- %s" --no-merges | grep -v "^\- \[bot\]"
```

## Troubleshooting

### "ANTHROPIC_API_KEY not configured"

- Verify secret name is exactly `ANTHROPIC_API_KEY`
- Check secret is in repository settings, not organization

### "Failed to generate release notes"

- Check API key is valid (not expired)
- Verify API key has sufficient quota
- Check Anthropic status page for outages

### Notes Not Appearing in Release

- Ensure `generate-notes` job completed successfully
- Check `release` job is using `needs.generate-notes.outputs.notes`
- Verify no YAML syntax errors in workflow

### Notes Missing from Installers

- Verify artifact upload succeeded in `generate-notes`
- Check artifact download step in `build` jobs
- Ensure installer scripts copy `RELEASE_NOTES.md`

## Local Testing

Test the generation script locally:

```bash
export ANTHROPIC_API_KEY="sk-ant-api..."
export VERSION="v1.2.0"
export PREV_TAG="v1.1.0"

# Collect commits
COMMITS=$(git log ${PREV_TAG}..HEAD --pretty=format:"- %s" --no-merges | head -200)

# Call API (simplified)
curl -s https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d "{
    \"model\": \"claude-sonnet-4-5-20250929\",
    \"max_tokens\": 1024,
    \"messages\": [{
      \"role\": \"user\",
      \"content\": \"Generate concise release notes for $VERSION.\\n\\nCommits:\\n$COMMITS\"
    }]
  }" | jq -r '.content[0].text'
```

## Cost Estimation

| Metric | Value |
|--------|-------|
| Input tokens (typical) | ~500-2000 |
| Output tokens (typical) | ~300-600 |
| Cost per release (Sonnet) | ~$0.01-0.02 |
| Cost per release (Opus) | ~$0.05-0.10 |

Monthly cost for weekly releases: ~$0.10-0.50

## Support

- **Feature Spec**: `specs/010-release-notes-generator/spec.md`
- **Implementation Plan**: `specs/010-release-notes-generator/plan.md`
- **Issues**: [GitHub Issues](https://github.com/smart-mcp-proxy/mcpproxy-go/issues)
