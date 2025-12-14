# Data Model: Docusaurus Documentation Site

**Feature**: 012-docusaurus-docs-site | **Date**: 2025-12-14

## Overview

This feature is primarily a static site generator configuration with CI/CD workflows. The "data model" represents the structure of documentation content, not database entities.

## Key Entities

### 1. Documentation Page

A single markdown file that renders as a page on docs.mcpproxy.app.

```yaml
# Frontmatter structure
---
id: installation          # URL slug
title: Installation       # Page title
sidebar_label: Install    # Short label for sidebar
sidebar_position: 1       # Order in sidebar
description: How to install MCPProxy on macOS, Windows, and Linux
keywords: [install, setup, homebrew, dmg, windows]
---

# Markdown content follows...
```

**Properties**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| id | string | No | URL path slug (defaults to filename) |
| title | string | Yes | Page title in browser/SEO |
| sidebar_label | string | No | Short name in sidebar (defaults to title) |
| sidebar_position | number | No | Sort order within category |
| description | string | Recommended | SEO meta description |
| keywords | string[] | No | SEO keywords |

### 2. Documentation Category

A folder that groups related pages with optional category metadata.

```yaml
# docs/configuration/_category_.json
{
  "label": "Configuration",
  "position": 2,
  "link": {
    "type": "generated-index",
    "description": "Learn how to configure MCPProxy"
  }
}
```

**Properties**:
| Field | Type | Description |
|-------|------|-------------|
| label | string | Category name in sidebar |
| position | number | Sort order among categories |
| link.type | string | "generated-index" or "doc" |
| link.description | string | Index page description |

### 3. Sidebar Configuration

Defines navigation structure for the documentation site.

```javascript
// sidebars.js
module.exports = {
  docs: [
    {
      type: 'category',
      label: 'Getting Started',
      items: ['getting-started/installation', 'getting-started/quick-start'],
      collapsed: false,  // Expanded by default
    },
    // ... more categories
  ],
};
```

**Category Properties**:
| Field | Type | Description |
|-------|------|-------------|
| type | 'category' | Marks as folder |
| label | string | Display name |
| items | string[] | Doc IDs or nested categories |
| collapsed | boolean | Default expansion state |

### 4. Search Index Entry

Auto-generated from page content for client-side search.

```typescript
interface SearchIndexEntry {
  docId: string;           // e.g., "getting-started/installation"
  title: string;           // Page title
  content: string;         // Full text content (stripped of markdown)
  sectionTitles: string[]; // H2/H3 headings
  url: string;             // Relative URL path
}
```

### 5. CI Workflow Event

Cross-repo dispatch event payload for marketing site updates.

```typescript
interface VersionUpdateEvent {
  event_type: 'update-version';
  client_payload: {
    version: string;       // e.g., "v1.0.5"
    version_no_v: string;  // e.g., "1.0.5"
    release_url: string;   // GitHub release URL
    release_notes: string; // Abbreviated release notes
  };
}
```

## File Structure

```text
docs/                                  # Content source (at repo root)
├── getting-started/
│   ├── _category_.json               # Category metadata
│   ├── installation.md               # FR-022: Installation guide
│   └── quick-start.md                # FR-022: Quick start guide
├── configuration/
│   ├── _category_.json
│   ├── config-file.md                # FR-022: Config file reference
│   ├── upstream-servers.md           # FR-022: Adding upstream servers
│   └── environment-variables.md      # FR-022: Env vars reference
├── cli/
│   ├── _category_.json
│   ├── command-reference.md          # FR-022: CLI commands
│   └── management-commands.md        # Existing file (enhanced)
├── api/
│   ├── _category_.json
│   ├── rest-api.md                   # FR-022: REST API (from swagger.yaml)
│   └── mcp-protocol.md               # FR-022: MCP protocol docs
├── web-ui/
│   ├── _category_.json
│   └── dashboard.md                  # FR-022: Web UI guide
├── features/
│   ├── _category_.json
│   ├── docker-isolation.md           # Existing (enhanced)
│   ├── oauth-authentication.md       # FR-022: OAuth setup
│   ├── code-execution/               # Existing folder
│   ├── security-quarantine.md        # FR-022: Quarantine feature
│   └── search-discovery.md           # FR-022: Tool search
└── images/                            # FR-023: Screenshots
    ├── dashboard-overview.png
    ├── server-list.png
    ├── add-server-form.png
    ├── server-details.png
    ├── quarantine-list.png
    ├── approval-dialog.png
    ├── oauth-status.png
    ├── tool-search.png
    └── full-dashboard.png

website/                               # Docusaurus configuration
├── docusaurus.config.js              # Site configuration
├── sidebars.js                       # Navigation structure
├── package.json                      # Dependencies
├── src/
│   ├── css/custom.css               # Brand styling
│   └── pages/index.js               # Redirect to /getting-started/installation
├── static/
│   └── img/
│       └── logo.svg                 # MCPProxy logo
└── prepare-docs.sh                  # Script to copy /docs to /website/docs

.github/workflows/
├── release.yml                       # Extended: docs deploy + marketing trigger
├── docs.yml                          # NEW: PR docs validation
└── claude-md-check.yml               # NEW: CLAUDE.md size check
```

## Data Flow

### Documentation Build Flow

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│   /docs/*   │────▶│ prepare-docs │────▶│ website/docs/*  │
│  (source)   │     │    script    │     │    (copied)     │
└─────────────┘     └──────────────┘     └─────────────────┘
                                                  │
                                                  ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  /website/  │────▶│  npm run     │────▶│ website/build/  │
│   build/    │     │    build     │     │   (static)      │
└─────────────┘     └──────────────┘     └─────────────────┘
                                                  │
                                                  ▼
                    ┌──────────────────────────────────────┐
                    │     Cloudflare Pages Deployment       │
                    │       docs.mcpproxy.app               │
                    └──────────────────────────────────────┘
```

### Cross-Repo Update Flow

```
┌───────────────────┐     ┌────────────────────────────────┐
│  mcpproxy-go      │     │    mcpproxy.app-website        │
│  release.yml      │     │    update-version.yml          │
├───────────────────┤     ├────────────────────────────────┤
│                   │     │                                │
│  1. Build release │     │                                │
│         │         │     │                                │
│         ▼         │     │                                │
│  2. Deploy docs   │     │                                │
│         │         │     │                                │
│         ▼         │     │                                │
│  3. repository    │────▶│  4. Receive dispatch           │
│     _dispatch     │     │         │                      │
│                   │     │         ▼                      │
│                   │     │  5. sed -i version in          │
│                   │     │     index.astro                │
│                   │     │     installation.astro         │
│                   │     │         │                      │
│                   │     │         ▼                      │
│                   │     │  6. git commit + push          │
│                   │     │         │                      │
│                   │     │         ▼                      │
│                   │     │  7. Cloudflare deploy          │
└───────────────────┘     └────────────────────────────────┘
```

## Validation Rules

### Frontmatter Validation

- `title` is required on all pages
- `sidebar_position` must be unique within category
- `id` must be URL-safe (lowercase, hyphens only)

### Link Validation

- Internal links validated at build time by Docusaurus
- Broken links cause build failure (catches missing pages)
- External links validated by optional plugin

### CLAUDE.md Size Validation

```bash
# Validation script logic
SIZE=$(wc -c < CLAUDE.md)
if [ $SIZE -gt 40000 ]; then exit 1; fi
if [ $SIZE -gt 38000 ]; then echo "::warning::..."; fi
```

## Contracts

This feature is primarily static site generation with no runtime APIs. Contracts are defined through:

1. **GitHub Actions Event Schema**: `repository_dispatch` payload for cross-repo updates
2. **Docusaurus Configuration**: `docusaurus.config.js` as the contract with Docusaurus
3. **Frontmatter Schema**: YAML frontmatter structure for documentation pages

No additional contract files are needed in `contracts/` directory.
