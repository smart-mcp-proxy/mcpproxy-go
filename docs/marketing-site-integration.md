# Marketing Site Integration

This document describes how to set up automatic version updates on the marketing site (mcpproxy.app) when new releases are published.

## Overview

When a new release tag is pushed to the mcpproxy-go repository, the release workflow automatically triggers an update on the marketing site via GitHub's `repository_dispatch` event.

## Setup Requirements

### 1. Create Personal Access Token (PAT)

Create a GitHub Personal Access Token with `repo` scope:

1. Go to GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)
2. Click "Generate new token (classic)"
3. Name: `MARKETING_SITE_DISPATCH_TOKEN`
4. Expiration: Choose appropriate expiration (recommend 1 year)
5. Scopes: Select `repo` (Full control of private repositories)
6. Click "Generate token"
7. Copy the token immediately (it won't be shown again)

### 2. Add Secret to mcpproxy-go Repository

1. Go to mcpproxy-go repository → Settings → Secrets and variables → Actions
2. Click "New repository secret"
3. Name: `MARKETING_SITE_DISPATCH_TOKEN`
4. Value: Paste the PAT created above
5. Click "Add secret"

### 3. Create Workflow in Marketing Site Repository

Create `.github/workflows/update-version.yml` in the `mcpproxy.app-website` repository:

```yaml
name: Update Version

on:
  repository_dispatch:
    types: [update-version]

jobs:
  update-version:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Update version
        run: |
          VERSION="${{ github.event.client_payload.version }}"
          echo "Updating to version: $VERSION"

          # Update version in your site's configuration
          # Example for a JSON config:
          # jq --arg v "$VERSION" '.version = $v' config.json > tmp.json && mv tmp.json config.json

          # Example for environment file:
          # echo "MCPPROXY_VERSION=$VERSION" > .env.version

      - name: Commit changes
        run: |
          git config --local user.email "github-actions[bot]@users.noreply.github.com"
          git config --local user.name "github-actions[bot]"
          git add -A
          git diff --staged --quiet || git commit -m "chore: update mcpproxy version to ${{ github.event.client_payload.version }}"

      - name: Push changes
        run: git push
```

## How It Works

1. Developer pushes a tag like `v1.2.3` to mcpproxy-go
2. The release workflow in mcpproxy-go triggers
3. After successful release, `repository_dispatch` sends event to marketing site
4. Marketing site workflow receives the version and updates accordingly
5. Changes are committed and pushed automatically

## Payload Format

The dispatch event sends the following payload:

```json
{
  "version": "v1.2.3"
}
```

Access in workflow: `${{ github.event.client_payload.version }}`

## Troubleshooting

### Dispatch Not Triggering

1. Verify the PAT has `repo` scope
2. Check the secret name matches exactly: `MARKETING_SITE_DISPATCH_TOKEN`
3. Ensure the marketing site repo name is correct in release.yml
4. Check GitHub Actions is enabled on the marketing site repo

### Workflow Not Running

1. Verify `.github/workflows/update-version.yml` exists in marketing site repo
2. Check the `types` array includes `update-version`
3. Look at the Actions tab for any failed runs

## Security Notes

- The PAT should be created by a user with write access to the marketing site repo
- Consider using a dedicated service account for automation
- Rotate the token periodically (before expiration)
- The `continue-on-error: true` in release.yml ensures releases aren't blocked if dispatch fails
