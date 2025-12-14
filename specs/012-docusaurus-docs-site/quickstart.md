# Quickstart: Docusaurus Documentation Site

**Feature**: 012-docusaurus-docs-site | **Date**: 2025-12-14

## Prerequisites

- Node.js 20+ installed
- npm or yarn package manager
- Git for version control
- Cloudflare account with Pages access (for deployment)

## Local Development Setup

### 1. Initialize Docusaurus in website/ folder

```bash
cd /Users/user/repos/mcpproxy-go

# Create website directory
mkdir -p website

# Initialize Docusaurus (classic preset)
cd website
npx create-docusaurus@latest . classic --typescript

# Clean up default content (we'll use /docs folder)
rm -rf docs blog
```

### 2. Configure Docusaurus

Create `docusaurus.config.ts`:

```typescript
import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';

const config: Config = {
  title: 'MCPProxy Documentation',
  tagline: 'Smart MCP Proxy for AI Agents',
  favicon: 'img/favicon.ico',
  url: 'https://docs.mcpproxy.app',
  baseUrl: '/',
  organizationName: 'smart-mcp-proxy',
  projectName: 'mcpproxy-go',
  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    ['classic', {
      docs: {
        routeBasePath: '/',
        sidebarPath: './sidebars.ts',
        editUrl: 'https://github.com/smart-mcp-proxy/mcpproxy-go/edit/main/website/',
      },
      blog: false,
      theme: {
        customCss: './src/css/custom.css',
      },
    }],
  ],

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

  themes: [
    ['@easyops-cn/docusaurus-search-local', {
      hashed: true,
      language: ['en'],
      indexDocs: true,
      indexBlog: false,
      docsRouteBasePath: '/',
    }],
  ],

  themeConfig: {
    navbar: {
      title: 'MCPProxy',
      logo: {
        alt: 'MCPProxy Logo',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Documentation',
        },
        {
          href: 'https://mcpproxy.app',
          label: 'Home',
          position: 'right',
        },
        {
          href: 'https://github.com/smart-mcp-proxy/mcpproxy-go',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Documentation',
          items: [
            { label: 'Getting Started', to: '/getting-started/installation' },
            { label: 'Configuration', to: '/configuration/config-file' },
            { label: 'CLI Reference', to: '/cli/command-reference' },
          ],
        },
        {
          title: 'Community',
          items: [
            { label: 'GitHub', href: 'https://github.com/smart-mcp-proxy/mcpproxy-go' },
            { label: 'Issues', href: 'https://github.com/smart-mcp-proxy/mcpproxy-go/issues' },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} MCPProxy. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'go'],
    },
  },
};

export default config;
```

### 3. Install plugins

```bash
npm install @easyops-cn/docusaurus-search-local docusaurus-plugin-llms
```

### 4. Create docs preparation script

```bash
# website/prepare-docs.sh
cat > prepare-docs.sh << 'EOF'
#!/bin/bash
# Copy docs from repo root to website/docs for Docusaurus build
set -e
rm -rf docs
cp -r ../docs ./docs
echo "Docs copied successfully"
EOF
chmod +x prepare-docs.sh
```

### 5. Update package.json scripts

```json
{
  "scripts": {
    "docusaurus": "docusaurus",
    "start": "./prepare-docs.sh && docusaurus start",
    "build": "./prepare-docs.sh && docusaurus build",
    "swizzle": "docusaurus swizzle",
    "deploy": "docusaurus deploy",
    "clear": "docusaurus clear",
    "serve": "docusaurus serve"
  }
}
```

### 6. Start local development server

```bash
npm start
```

Open http://localhost:3000 to see the docs site.

## Validation Checklist

### Local Build Validation

```bash
# Clean and build
cd website
npm run clear
npm run build

# Verify build output
ls -la build/
# Should contain index.html, assets/, etc.

# Test production build locally
npm run serve
# Visit http://localhost:3000
```

### Link Validation

```bash
# Docusaurus validates internal links during build
npm run build 2>&1 | grep -i "broken"
# Should show no broken link errors
```

### Search Validation

1. Start dev server: `npm start`
2. Use search box (Ctrl+K or click)
3. Search for "installation" - should find Getting Started page
4. Search for "OAuth" - should find OAuth Authentication page

### llms.txt Validation

```bash
# After build, verify llms.txt files exist
ls -la build/llms.txt build/llms-full.txt

# Check llms.txt format (should start with H1 heading)
head -20 build/llms.txt
# Should show:
# # MCPProxy Documentation
# > Brief description...
# ## Getting Started
# - [Installation](/getting-started/installation): ...

# Check llms-full.txt size (should contain all docs)
wc -c build/llms-full.txt
# Should be ~50-150KB depending on content

# Verify llms.txt is accessible after deploy
curl https://docs.mcpproxy.app/llms.txt | head -30
curl https://docs.mcpproxy.app/llms-full.txt | wc -c
```

### Mobile Responsiveness

1. Open Chrome DevTools (F12)
2. Toggle device toolbar (Ctrl+Shift+M)
3. Test on iPhone 12, iPad, Galaxy S20
4. Verify navigation menu works on mobile

### Lighthouse Audit

```bash
# Install Lighthouse CLI
npm install -g lighthouse

# Run audit on local build
npm run build && npm run serve &
sleep 3
lighthouse http://localhost:3000 --output html --output-path ./lighthouse-report.html
```

Target scores:
- Performance: 90+
- Accessibility: 90+
- Best Practices: 90+
- SEO: 90+

## CI Integration Validation

### GitHub Actions Syntax Check

```bash
# Validate workflow syntax
gh workflow view release.yml
gh workflow view docs.yml
gh workflow view claude-md-check.yml
```

### CLAUDE.md Size Check

```bash
# Local test of size check
SIZE=$(wc -c < CLAUDE.md)
echo "CLAUDE.md size: $SIZE characters"
if [ $SIZE -gt 40000 ]; then
  echo "ERROR: Exceeds 40k limit"
elif [ $SIZE -gt 38000 ]; then
  echo "WARNING: Approaching limit"
else
  echo "OK: Within limits"
fi
```

## Deployment Validation

### Cloudflare Pages Test Deploy

```bash
# Test deploy to preview URL (not production)
cd website
npm run build
npx wrangler pages deploy build --project-name=mcpproxy-docs-preview
```

### DNS Verification

After production deployment:

```bash
# Verify DNS resolution
dig docs.mcpproxy.app CNAME

# Verify HTTPS
curl -I https://docs.mcpproxy.app
```

## Common Issues and Solutions

### Issue: "Cannot find module" errors

```bash
# Clear caches and reinstall
cd website
rm -rf node_modules .docusaurus
npm install
```

### Issue: Broken internal links

```bash
# Check for typos in markdown links
grep -r "\[.*\](.*\.md)" ../docs/ | grep -v "http"
# Verify all referenced files exist
```

### Issue: Search not working

```bash
# Rebuild search index
npm run clear
npm run build
# Check for search-index.json in build/
ls build/search-index*.json
```

### Issue: Styles not matching marketing site

- Compare CSS variables in `src/css/custom.css` with mcpproxy.app
- Verify Inter font is loaded
- Check dark mode colors

## Success Criteria Verification

| Criterion | How to Verify | Target |
|-----------|---------------|--------|
| SC-001: Page load <3s | Lighthouse Performance | 90+ |
| SC-002: All docs included | Build completes without errors | 15 pages |
| SC-003: Deploy <5min | GitHub Actions timing | <5min |
| SC-004: Accessibility 90+ | Lighthouse Accessibility | 90+ |
| SC-005: Zero broken links | Build output | No errors |
| SC-006: Search works | Manual test with keywords | 95% relevance |
| SC-007: Cross-browser | Test in Chrome/Firefox/Safari/Edge | All work |
| SC-008: Marketing links updated | Check mcpproxy.app after release | <10min |
| SC-009: CLAUDE.md <25k chars | `wc -c < CLAUDE.md` | <25,000 |
| SC-010: Size check runs | PR CI workflow | <10s |
| SC-011: 15 pages created | Sidebar navigation | All present |
| SC-012: REST API docs complete | API page content | All endpoints |
| SC-013: 9 screenshots | docs/images/ | 9 PNG files |
| SC-014: llms.txt generated | `curl docs.mcpproxy.app/llms.txt` | Valid format |
| SC-015: Version displayed | Check navbar/announcement bar | Shows "v0.X" |
