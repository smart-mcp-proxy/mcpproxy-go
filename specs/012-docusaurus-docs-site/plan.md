# Implementation Plan: Docusaurus Documentation Site

**Branch**: `012-docusaurus-docs-site` | **Date**: 2025-12-14 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification for docs.mcpproxy.app with CI deployment and CLAUDE.md optimization

## Summary

Build a Docusaurus 3 documentation site deployed to docs.mcpproxy.app via Cloudflare Pages. The implementation includes:
1. Docusaurus site setup with existing docs/ content
2. CI workflow for docs deployment on release tags
3. CLAUDE.md size check CI workflow (warn >38k, fail >40k)
4. Cross-repo trigger to update marketing site download links
5. CLAUDE.md refactoring with links to detailed documentation

## Technical Context

**Language/Version**: Node.js 20, TypeScript (Docusaurus), Bash (CI scripts)
**Primary Dependencies**: Docusaurus 3.x, @docusaurus/preset-classic, @easyops-cn/docusaurus-search-local
**Storage**: N/A (static site generation)
**Testing**: Docusaurus build validation, link checking, Lighthouse audit
**Target Platform**: Cloudflare Pages (static hosting)
**Project Type**: Documentation site (static) + CI workflows
**Performance Goals**: <3s page load, Lighthouse 90+
**Constraints**: Non-blocking deployment (docs failure doesn't block release)
**Scale/Scope**: 15 documentation pages, 9 screenshots, CLAUDE.md <25k chars

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | N/A | Documentation site, not runtime performance |
| II. Actor-Based Concurrency | N/A | No Go code in this feature |
| III. Configuration-Driven | PASS | Docusaurus uses config file (docusaurus.config.js) |
| IV. Security by Default | PASS | Public docs, no auth required; API key used for cross-repo trigger |
| V. Test-Driven Development | PASS | Build validation in CI, link checking |
| VI. Documentation Hygiene | PASS | This feature IS the documentation improvement |

**All gates pass.** Proceeding to Phase 0.

## Project Structure

### Documentation (this feature)

```text
specs/012-docusaurus-docs-site/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (from /speckit.tasks)
```

### Source Code (repository root)

```text
website/                          # Docusaurus site (NEW)
├── docusaurus.config.js          # Site configuration
├── sidebars.js                   # Navigation structure
├── package.json                  # Dependencies
├── src/
│   ├── css/custom.css           # Brand colors matching mcpproxy.app
│   └── pages/index.js           # Landing page redirect
├── static/
│   └── img/                     # Screenshots and assets
└── docs/                        # Symlink or copy from /docs

docs/                             # Content source (EXISTING - enhanced)
├── getting-started/
│   ├── installation.md
│   └── quick-start.md
├── configuration/
│   ├── config-file.md
│   ├── upstream-servers.md
│   └── environment-variables.md
├── cli/
│   ├── command-reference.md
│   └── management-commands.md   # Existing
├── api/
│   ├── rest-api.md              # References swagger.yaml
│   └── mcp-protocol.md
├── web-ui/
│   └── dashboard.md
├── features/
│   ├── docker-isolation.md      # Existing
│   ├── oauth-authentication.md
│   ├── code-execution/          # Existing folder
│   ├── security-quarantine.md
│   └── search-discovery.md
└── images/                       # Screenshots (NEW)
    ├── dashboard-overview.png
    ├── server-list.png
    └── ...

.github/workflows/
├── release.yml                   # Updated with docs deployment + marketing trigger
├── docs.yml                      # NEW: PR docs build validation
└── claude-md-check.yml           # NEW: CLAUDE.md size check

CLAUDE.md                         # Refactored with doc links
```

**Structure Decision**: Docusaurus in `/website` with docs content in `/docs` (already exists). This follows Docusaurus best practice of separating site config from content.

## Complexity Tracking

> No constitution violations. All complexity justified by feature requirements.

| Decision | Justification |
|----------|---------------|
| Separate `/website` folder | Standard Docusaurus pattern; keeps site config separate from docs content |
| Cross-repo trigger | Required by spec for marketing site link updates; simpler than monorepo |
| Two CI workflows | CLAUDE.md check runs on all PRs; docs deployment only on releases |
