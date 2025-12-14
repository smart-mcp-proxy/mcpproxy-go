# Research: Docusaurus Documentation Site

**Feature**: 012-docusaurus-docs-site | **Date**: 2025-12-14 | **Status**: Complete

## Summary

This research resolves all technical decisions for implementing docs.mcpproxy.app using Docusaurus 3 with Cloudflare Pages deployment, CLAUDE.md size enforcement, and cross-repo marketing site link updates.

## Technical Decisions

### 1. Docusaurus Version and Configuration

**Decision**: Docusaurus 3.7.x (latest stable)

**Rationale**:
- React 18 support for better performance
- Native TypeScript configuration support
- Improved MDX 3 compatibility
- Active maintenance with regular updates

**Configuration Approach**:
```javascript
// docusaurus.config.js - key settings
module.exports = {
  title: 'MCPProxy Documentation',
  tagline: 'Smart MCP Proxy for AI Agents',
  url: 'https://docs.mcpproxy.app',
  baseUrl: '/',
  organizationName: 'smart-mcp-proxy',
  projectName: 'mcpproxy-go',

  presets: [['@docusaurus/preset-classic', {
    docs: {
      routeBasePath: '/', // docs at root
      sidebarPath: './sidebars.js',
      editUrl: 'https://github.com/smart-mcp-proxy/mcpproxy-go/edit/main/website/',
    },
    blog: false, // no blog needed
    theme: { customCss: './src/css/custom.css' },
  }]],
};
```

### 2. Search Implementation

**Decision**: `@easyops-cn/docusaurus-search-local`

**Alternatives Considered**:
| Option | Pros | Cons | Decision |
|--------|------|------|----------|
| Algolia DocSearch | Best search quality, instant | Requires application, external dependency | Rejected (setup complexity) |
| `docusaurus-search-local` | Offline, fast, no external deps | Slightly larger bundle | **Selected** |
| Built-in search (Docusaurus) | No setup | Basic functionality only | Rejected |

**Configuration**:
```javascript
themes: [
  ['@easyops-cn/docusaurus-search-local', {
    hashed: true,
    language: ['en'],
    indexDocs: true,
    indexBlog: false,
    docsRouteBasePath: '/',
  }],
],
```

### 3. Cloudflare Pages Deployment

**Decision**: Direct Cloudflare Pages with wrangler-action in GitHub Actions

**Deployment Configuration**:
```yaml
# In release.yml (docs deployment job)
- uses: cloudflare/wrangler-action@v3
  with:
    apiToken: ${{ secrets.CLOUDFLARE_API_TOKEN }}
    accountId: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}
    command: pages deploy website/build --project-name=mcpproxy-docs
```

**DNS Setup** (manual, one-time):
- CNAME record: `docs.mcpproxy.app` → `mcpproxy-docs.pages.dev`
- SSL: Automatic via Cloudflare

**Build Settings**:
- Build command: `npm run build`
- Build output directory: `build`
- Root directory: `website`
- Node version: 20

### 4. Cross-Repository Link Updates

**Decision**: `repository_dispatch` event with Personal Access Token (PAT)

**Mechanism**:
1. mcpproxy-go release workflow completes
2. Triggers dispatch to `smart-mcp-proxy/mcpproxy.app-website`
3. Marketing site workflow receives version, updates files, commits, deploys

**Implementation**:
```yaml
# In mcpproxy-go release.yml
- name: Trigger marketing site update
  uses: peter-evans/repository-dispatch@v2
  with:
    token: ${{ secrets.MARKETING_SITE_DISPATCH_TOKEN }}
    repository: smart-mcp-proxy/mcpproxy.app-website
    event-type: update-version
    client-payload: '{"version": "${{ github.ref_name }}"}'
```

**Marketing Site Workflow** (to be created in mcpproxy.app-website):
```yaml
# .github/workflows/update-version.yml
name: Update Version Links
on:
  repository_dispatch:
    types: [update-version]

jobs:
  update:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Update version in files
        run: |
          VERSION="${{ github.event.client_payload.version }}"
          VERSION_NO_V="${VERSION#v}"

          # Update index.astro and installation.astro
          sed -i "s/v[0-9]\+\.[0-9]\+\.[0-9]\+/${VERSION}/g" src/pages/index.astro
          sed -i "s/v[0-9]\+\.[0-9]\+\.[0-9]\+/${VERSION}/g" src/pages/docs/installation.astro

      - name: Commit and push
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git add -A
          git commit -m "chore: update download links to ${{ github.event.client_payload.version }}" || exit 0
          git push
```

**Token Requirements**:
- PAT with `repo` scope for cross-repo dispatch
- Store as `MARKETING_SITE_DISPATCH_TOKEN` in mcpproxy-go secrets

### 5. CLAUDE.md Size Check Implementation

**Decision**: Standalone GitHub Actions workflow with bash script

**Thresholds**:
| Size | Action | Exit Code |
|------|--------|-----------|
| ≤38,000 chars | Pass | 0 |
| 38,001-40,000 chars | Warn (annotation) | 0 |
| >40,000 chars | Fail | 1 |

**Implementation**:
```yaml
# .github/workflows/claude-md-check.yml
name: CLAUDE.md Size Check
on:
  pull_request:
    paths:
      - 'CLAUDE.md'

jobs:
  check-size:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Check CLAUDE.md size
        run: |
          SIZE=$(wc -c < CLAUDE.md)
          echo "CLAUDE.md size: $SIZE characters"

          if [ $SIZE -gt 40000 ]; then
            echo "::error file=CLAUDE.md::CLAUDE.md exceeds 40,000 character limit ($SIZE chars). Move detailed content to docs/"
            exit 1
          elif [ $SIZE -gt 38000 ]; then
            echo "::warning file=CLAUDE.md::CLAUDE.md approaching limit: $SIZE/40,000 characters. Consider moving content to docs/"
          else
            echo "✅ CLAUDE.md size OK: $SIZE/40,000 characters"
          fi
```

### 6. Documentation Content Structure

**Decision**: Symlink `/docs` to `/website/docs` during build (content stays in `/docs`)

**Rationale**:
- Existing docs stay in `/docs` at repo root (already there)
- Docusaurus configuration in `/website` references content via symlink or copy
- Contributors edit `/docs` directly (intuitive location)

**Build Script**:
```bash
# website/prepare-docs.sh (run before build)
#!/bin/bash
rm -rf docs
cp -r ../docs ./docs
```

**Sidebar Configuration** (sidebars.js):
```javascript
module.exports = {
  docs: [
    {
      type: 'category',
      label: 'Getting Started',
      items: ['getting-started/installation', 'getting-started/quick-start'],
    },
    {
      type: 'category',
      label: 'Configuration',
      items: ['configuration/config-file', 'configuration/upstream-servers', 'configuration/environment-variables'],
    },
    {
      type: 'category',
      label: 'CLI',
      items: ['cli/command-reference', 'cli/management-commands'],
    },
    {
      type: 'category',
      label: 'API',
      items: ['api/rest-api', 'api/mcp-protocol'],
    },
    {
      type: 'category',
      label: 'Web UI',
      items: ['web-ui/dashboard'],
    },
    {
      type: 'category',
      label: 'Features',
      items: [
        'features/docker-isolation',
        'features/oauth-authentication',
        'features/code-execution',
        'features/security-quarantine',
        'features/search-discovery',
      ],
    },
  ],
};
```

### 7. Brand Consistency with Marketing Site

**Decision**: Extract CSS variables from mcpproxy.app and apply to Docusaurus

**Color Palette** (from mcpproxy.app):
```css
/* website/src/css/custom.css */
:root {
  --ifm-color-primary: #3b82f6;      /* Blue - matches marketing */
  --ifm-color-primary-dark: #2563eb;
  --ifm-color-primary-darker: #1d4ed8;
  --ifm-color-primary-darkest: #1e40af;
  --ifm-color-primary-light: #60a5fa;
  --ifm-color-primary-lighter: #93c5fd;
  --ifm-color-primary-lightest: #bfdbfe;
  --ifm-font-family-base: 'Inter', system-ui, -apple-system, sans-serif;
}

[data-theme='dark'] {
  --ifm-color-primary: #60a5fa;
  --ifm-background-color: #0f172a;   /* Dark slate - matches marketing */
}
```

### 8. LLM-Friendly Documentation (llms.txt)

**Decision**: `docusaurus-plugin-llms` by rachfop

The [llmstxt.org](https://llmstxt.org/) standard provides a way for websites to offer LLM-friendly documentation access. Instead of parsing HTML, LLMs can fetch a single markdown file containing all documentation.

**Alternatives Considered**:
| Plugin | Features | Decision |
|--------|----------|----------|
| `docusaurus-plugin-llms` (rachfop) | Full llms.txt + llms-full.txt, custom files, import cleaning, path transforms | **Selected** |
| `docusaurus-plugin-llms-txt` (din0s) | Basic llms.txt generation | Rejected (fewer features) |
| `docusaurus-plugin-generate-llms-txt` | Simple concatenation | Rejected (minimal options) |

**Why rachfop's plugin**:
- Generates both `llms.txt` (table of contents with links) and `llms-full.txt` (complete docs)
- Cleans MDX imports that confuse LLMs
- Removes duplicate headings from auto-generated content
- Supports custom LLM files for specific sections (e.g., `llms-api.txt`)
- Configurable document ordering via glob patterns

**Configuration**:
```javascript
// docusaurus.config.js
plugins: [
  [
    'docusaurus-plugin-llms',
    {
      generateLLMsTxt: true,
      generateLLMsFullTxt: true,
      excludeImports: true,
      removeDuplicateHeadings: true,
      includeOrder: [
        'getting-started/*',
        'configuration/*',
        'cli/*',
        'api/*',
        'web-ui/*',
        'features/*',
      ],
    },
  ],
],
```

**Generated Files**:
| File | Purpose | Size Estimate |
|------|---------|---------------|
| `/llms.txt` | Table of contents with section links and descriptions | ~5KB |
| `/llms-full.txt` | Complete documentation in single markdown file | ~100KB |

**llms.txt Format** (per llmstxt.org spec):
```markdown
# MCPProxy Documentation

> MCPProxy is a smart proxy for AI agents using the Model Context Protocol (MCP).
> It provides intelligent tool discovery, massive token savings, and built-in security.

## Getting Started

- [Installation](/getting-started/installation): Install MCPProxy on macOS, Windows, or Linux
- [Quick Start](/getting-started/quick-start): First run and basic configuration

## Configuration

- [Config File](/configuration/config-file): mcp_config.json reference
- [Upstream Servers](/configuration/upstream-servers): Adding MCP servers

## Optional

- [Code Execution](/features/code-execution): JavaScript orchestration (advanced)
```

### 10. Version Display in Documentation

**Decision**: Inject version from release tag into Docusaurus config during CI build

**Requirement**: Display current MCPProxy minor version (0.X.*) prominently in the documentation. Patch versions can be ignored.

**Implementation Approach**:

1. **Version Source**: Extract from git tag during release CI (e.g., `v0.11.0` → `0.11`)
2. **Injection Point**: Update `docusaurus.config.js` or use environment variable
3. **Display Locations**:
   - Navbar badge/label
   - Footer
   - Announcement bar (optional)

**CI Implementation**:
```yaml
# In release.yml docs deployment job
- name: Set version for docs
  run: |
    VERSION="${{ github.ref_name }}"
    # Extract minor version: v0.11.2 → 0.11
    MINOR_VERSION=$(echo "$VERSION" | sed 's/^v//' | cut -d. -f1,2)
    echo "MCPPROXY_VERSION=$MINOR_VERSION" >> $GITHUB_ENV

- name: Build docs with version
  working-directory: website
  run: |
    # Inject version into config
    sed -i "s/__VERSION__/$MCPPROXY_VERSION/g" docusaurus.config.js
    npm run build
```

**Docusaurus Config**:
```javascript
// docusaurus.config.js
const config = {
  // ...
  themeConfig: {
    announcementBar: {
      id: 'version_bar',
      content: 'Documentation for MCPProxy <b>v__VERSION__</b>',
      backgroundColor: '#3b82f6',
      textColor: '#ffffff',
      isCloseable: false,
    },
    navbar: {
      // Add version badge
      items: [
        {
          type: 'html',
          position: 'right',
          value: '<span class="badge badge--primary">v__VERSION__</span>',
        },
        // ... other items
      ],
    },
  },
};
```

**Alternative: Custom React Component**:
```javascript
// src/theme/Root.js (swizzled)
import React from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

export default function Root({children}) {
  const {siteConfig} = useDocusaurusContext();
  // Version available via siteConfig.customFields.version
  return <>{children}</>;
}
```

**Version in customFields**:
```javascript
// docusaurus.config.js
module.exports = {
  customFields: {
    version: process.env.MCPPROXY_VERSION || 'dev',
  },
};
```

### 11. Screenshot Capture Strategy

**Decision**: Use Playwright MCP during task execution, with placeholders for missing screenshots

**Process**:
1. Start mcpproxy with Web UI: `./mcpproxy serve`
2. Navigate using `mcp__playwright__browser_navigate` to `http://127.0.0.1:8080/ui/`
3. Wait for load with `mcp__playwright__browser_wait_for`
4. Capture with `mcp__playwright__browser_take_screenshot`
5. Save to `docs/images/`

**Fallback**: Placeholder markdown format:
```markdown
![Dashboard Overview](./images/dashboard-overview.png)
<!-- PLACEHOLDER: Capture screenshot of main dashboard -->
```

## Resolved Unknowns

| Unknown | Resolution |
|---------|------------|
| Docusaurus version | 3.7.x (latest stable) |
| Search solution | @easyops-cn/docusaurus-search-local |
| Hosting platform | Cloudflare Pages |
| Cross-repo mechanism | repository_dispatch with PAT |
| CLAUDE.md thresholds | 38k warn, 40k fail |
| Docs content location | /docs at repo root, copied to /website/docs on build |
| Screenshot tool | Playwright MCP with placeholders as fallback |
| Brand colors | Extract from mcpproxy.app CSS variables |
| LLM documentation access | docusaurus-plugin-llms (generates llms.txt + llms-full.txt) |
| Version display | Inject minor version (0.X) from git tag during CI build |
| Commit build output? | NO - build fresh in CI, ignore website/build/ in .gitignore |
| Local docs preview | `make docs-dev` for hot reload, `make docs-build` for verification |

## Dependencies

### NPM Packages (website/package.json)

```json
{
  "dependencies": {
    "@docusaurus/core": "^3.7.0",
    "@docusaurus/preset-classic": "^3.7.0",
    "@easyops-cn/docusaurus-search-local": "^0.45.0",
    "docusaurus-plugin-llms": "^1.0.0",
    "react": "^18.3.0",
    "react-dom": "^18.3.0"
  },
  "devDependencies": {
    "@docusaurus/module-type-aliases": "^3.7.0",
    "@docusaurus/types": "^3.7.0"
  }
}
```

### GitHub Secrets Required

| Secret | Purpose | Repository |
|--------|---------|------------|
| CLOUDFLARE_API_TOKEN | Cloudflare Pages deployment | mcpproxy-go |
| CLOUDFLARE_ACCOUNT_ID | Cloudflare account identifier | mcpproxy-go |
| MARKETING_SITE_DISPATCH_TOKEN | Cross-repo workflow trigger (PAT) | mcpproxy-go |

### CI Workflow Files

| File | Purpose |
|------|---------|
| `.github/workflows/release.yml` | Extended with docs deployment + marketing trigger |
| `.github/workflows/docs.yml` | PR docs build validation |
| `.github/workflows/claude-md-check.yml` | CLAUDE.md size enforcement |

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| Docs build fails during release | Non-blocking: `continue-on-error: true` |
| Marketing site update fails | Non-blocking: logged, manual fix possible |
| Cloudflare rate limits | Cache dependencies, limit deploy frequency |
| Search index too large | Local search with client-side indexing handles this |
| Screenshots break on UI changes | Placeholder fallback, manual update path |

### 12. Build Output and Git Strategy

**Decision**: Do NOT commit build output to repository. Build fresh in CI.

**Research Sources**:
- [Cloudflare Pages Docusaurus Guide](https://developers.cloudflare.com/pages/framework-guides/deploy-a-docusaurus-site/)
- [Docusaurus Official .gitignore](https://github.com/facebook/docusaurus/blob/main/.gitignore)
- [Docusaurus Deployment Guide](https://docusaurus.io/docs/deployment)

**Best Practices Summary**:

| What to Commit | What to Ignore |
|----------------|----------------|
| `package.json` | `node_modules/` |
| `package-lock.json` or `yarn.lock` | `website/build/` |
| Source markdown files (`docs/`) | `website/.docusaurus/` |
| Docusaurus config (`docusaurus.config.js`) | `.cache-loader/` |
| Custom CSS and components | `*.log` files |

**Rationale**:
1. **Cloudflare Pages builds from source**: Every push triggers a fresh build on Cloudflare's infrastructure
2. **Build output is deterministic**: Same source = same output, no need to version
3. **Avoids merge conflicts**: Build artifacts create noisy, conflict-prone diffs
4. **Reduces repo size**: Build output can be 10-100x larger than source
5. **Industry standard**: Docusaurus, Next.js, Gatsby all recommend ignoring build output

**Required .gitignore Entries** (for `website/`):
```gitignore
# Docusaurus build output
website/build/
website/.docusaurus/
website/.cache-loader/

# Dependencies
website/node_modules/

# Logs
website/npm-debug.log*
website/yarn-error.log*

# Copied docs (generated by prepare-docs.sh)
website/docs/
```

**CI Build Flow**:
```
Push to main/tag → Cloudflare Pages → npm install → npm run build → Deploy build/
```

**Local Preview Flow** (for PR review):
```bash
make docs-dev    # Start local dev server with hot reload
make docs-build  # Build static site locally (verify before PR)
```

### 13. Local Documentation Development

**Decision**: Add Makefile targets for local docs development

**Commands**:
| Command | Purpose |
|---------|---------|
| `make docs-setup` | Install docs dependencies (one-time setup) |
| `make docs-dev` | Start local dev server with hot reload (http://localhost:3000) |
| `make docs-build` | Build static site locally for verification |
| `make docs-clean` | Remove build artifacts and node_modules |

**Implementation**:
```makefile
# Documentation site commands
docs-setup:
	@echo "📦 Installing documentation dependencies..."
	cd website && npm install
	@echo "✅ Documentation setup complete"

docs-dev:
	@echo "📄 Starting documentation dev server..."
	cd website && ./prepare-docs.sh && npm run start
	# Opens http://localhost:3000

docs-build:
	@echo "🔨 Building documentation site..."
	cd website && ./prepare-docs.sh && npm run build
	@echo "✅ Documentation built to website/build/"

docs-clean:
	@echo "🧹 Cleaning documentation artifacts..."
	rm -rf website/build website/.docusaurus website/node_modules website/docs
	@echo "✅ Documentation cleanup complete"
```

**Workflow for PR Review**:
1. Make changes to `docs/*.md` files
2. Run `make docs-dev` to preview locally with hot reload
3. Verify changes look correct
4. Run `make docs-build` to ensure production build succeeds
5. Commit source files only (build output ignored by .gitignore)
6. Push PR - CI validates build

## Next Steps

Phase 1 artifacts to generate:
1. `data-model.md` - Key entities (Documentation Page, Section, Navigation)
2. `quickstart.md` - Setup and validation steps
3. `contracts/` - API contracts if applicable (likely N/A for static site)
