# Feature Specification: Docusaurus Documentation Site

**Feature Branch**: `012-docusaurus-docs-site`
**Created**: 2025-12-14
**Status**: Draft
**Input**: User description: "Build Docusaurus documentation site at docs.mcpproxy.app with automatic CI deployment, addressing GitHub issue #189 about CLAUDE.md size optimization"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Developer Reads Documentation (Priority: P1)

A developer new to MCPProxy visits docs.mcpproxy.app to learn how to install, configure, and use the application. They navigate through getting started guides, configuration reference, and feature documentation to successfully set up MCPProxy for their AI workflow.

**Why this priority**: Documentation accessibility is the primary value proposition - without readable, navigable docs, the entire feature fails.

**Independent Test**: Can be fully tested by visiting docs.mcpproxy.app, verifying all navigation works, pages load, and content renders correctly on desktop and mobile.

**Acceptance Scenarios**:

1. **Given** a developer visits docs.mcpproxy.app, **When** the page loads, **Then** they see a professional documentation home page with clear navigation to key sections within 3 seconds
2. **Given** a developer is on any documentation page, **When** they use the site navigation, **Then** they can reach any other page within 3 clicks
3. **Given** a developer uses mobile device, **When** they access docs.mcpproxy.app, **Then** the site is fully responsive and usable

---

### User Story 2 - Automatic Documentation Deployment (Priority: P1)

When a maintainer creates a new release tag (v*), the documentation site is automatically built and deployed to docs.mcpproxy.app without manual intervention. The deployment happens as part of the existing release CI pipeline.

**Why this priority**: Manual deployment defeats the purpose - automation ensures docs stay current with releases.

**Independent Test**: Can be tested by pushing a release tag and verifying the docs site updates automatically within the CI workflow execution time.

**Acceptance Scenarios**:

1. **Given** a maintainer pushes a version tag (e.g., v1.0.5), **When** the release CI workflow runs, **Then** the documentation site is built and deployed to docs.mcpproxy.app
2. **Given** the documentation build fails, **When** the CI workflow runs, **Then** the release continues but the failure is reported clearly in the workflow logs
3. **Given** a PR is created with documentation changes, **When** CI runs, **Then** the documentation build is validated (but not deployed)

---

### User Story 3 - Developer Searches Documentation (Priority: P2)

A developer looking for specific information uses the built-in search functionality to quickly find relevant documentation sections. They can search for keywords like "OAuth", "Docker isolation", or "API key" and get instant results.

**Why this priority**: Search significantly improves documentation usability but is not required for MVP - users can still navigate manually.

**Independent Test**: Can be tested by using the search box and verifying results appear for known keywords in the documentation.

**Acceptance Scenarios**:

1. **Given** a developer is on any documentation page, **When** they use the search function with a keyword, **Then** relevant results appear within 1 second
2. **Given** no results match a search query, **When** the search completes, **Then** a helpful "no results" message is displayed with suggestions

---

### User Story 4 - Contributor Updates Documentation (Priority: P2)

A contributor wants to fix a typo or add documentation. They edit markdown files in the repository's docs/ folder, submit a PR, and see the changes reflected after merge and release.

**Why this priority**: Contributor workflow enables community participation but is secondary to the core documentation delivery.

**Independent Test**: Can be tested by editing a docs/ file, submitting a PR, merging it, and verifying the change appears on docs.mcpproxy.app after deployment.

**Acceptance Scenarios**:

1. **Given** a contributor edits a markdown file in docs/, **When** the PR is merged and a release is created, **Then** the change appears on docs.mcpproxy.app
2. **Given** a contributor adds a new documentation page, **When** they follow the documented contribution guide, **Then** the page appears in the site navigation after deployment

---

### User Story 5 - User Navigates Between Marketing and Docs Sites (Priority: P3)

A potential user lands on mcpproxy.app (marketing site), reads about MCPProxy features, and clicks a link to dive into detailed documentation at docs.mcpproxy.app. The experience feels cohesive despite being separate deployments.

**Why this priority**: Cross-site navigation improves user experience but sites are independently functional.

**Independent Test**: Can be tested by navigating from mcpproxy.app to docs.mcpproxy.app and verifying the link works and visual consistency is maintained.

**Acceptance Scenarios**:

1. **Given** a user is on mcpproxy.app, **When** they click a "Documentation" link, **Then** they are taken to docs.mcpproxy.app
2. **Given** a user is on docs.mcpproxy.app, **When** they click the logo or "Home" link, **Then** they can return to mcpproxy.app

---

### User Story 6 - Marketing Site Download Links Auto-Update (Priority: P2)

When a new version is released, the download links on the marketing site (mcpproxy.app) are automatically updated to point to the new version's binaries. This eliminates manual updates and ensures users always see current download links.

**Why this priority**: Stale download links frustrate users and undermine trust; automation prevents human error.

**Independent Test**: Can be tested by creating a release tag and verifying the marketing site's download links update to the new version within CI workflow execution time.

**Acceptance Scenarios**:

1. **Given** a maintainer pushes a version tag (e.g., v1.0.5), **When** the release CI workflow completes, **Then** the marketing site at mcpproxy.app shows download links pointing to v1.0.5
2. **Given** the marketing site update fails, **When** the release CI workflow runs, **Then** the release continues but the failure is reported clearly in the workflow logs
3. **Given** the marketing site is updated, **When** a user visits mcpproxy.app, **Then** all versioned download links (Windows installer, macOS DMG, Linux binaries) reflect the latest release version

---

### User Story 7 - CLAUDE.md Size Prevention (Priority: P1)

CI automatically checks CLAUDE.md file size on every PR to prevent it from growing beyond usable limits. This addresses the recurring issue (#189) where CLAUDE.md exceeds size thresholds that impact AI agent performance.

**Why this priority**: Preventing CLAUDE.md bloat is the root cause of issue #189 - without this check, the problem will recur.

**Independent Test**: Can be tested by creating a PR that increases CLAUDE.md beyond the threshold and verifying CI fails with a clear message.

**Acceptance Scenarios**:

1. **Given** a PR modifies CLAUDE.md, **When** CI runs, **Then** the file size is checked against defined thresholds
2. **Given** CLAUDE.md exceeds 38,000 characters, **When** CI runs, **Then** a warning is emitted but the check passes
3. **Given** CLAUDE.md exceeds 40,000 characters, **When** CI runs, **Then** the check fails with a clear error message suggesting to move content to docs/

---

### User Story 8 - AI Agent Accesses Extended Documentation (Priority: P1)

An AI agent (Claude Code, Cursor, etc.) working on the MCPProxy codebase reads the streamlined CLAUDE.md and follows links to detailed documentation when deeper information is needed. This allows agents to efficiently access comprehensive docs without context bloat.

**Why this priority**: This is the core solution to issue #189 - lean CLAUDE.md with links to detailed docs.

**Independent Test**: Can be tested by having an AI agent read CLAUDE.md and successfully navigate to linked documentation for specific topics.

**Acceptance Scenarios**:

1. **Given** an AI agent reads CLAUDE.md, **When** it needs detailed information about a feature, **Then** it finds a link to the relevant docs/ page
2. **Given** CLAUDE.md contains a topic summary, **When** the agent follows the "See docs/X for details" link, **Then** it finds comprehensive documentation
3. **Given** all major sections in CLAUDE.md, **When** reviewed, **Then** each has corresponding detailed documentation in docs/

---

### User Story 9 - LLM Accesses Documentation via llms.txt (Priority: P2)

An LLM or AI agent that doesn't have access to the MCPProxy codebase can access complete documentation via the llms.txt standard endpoint. This enables any LLM to efficiently consume MCPProxy documentation without parsing HTML.

**Why this priority**: Extends documentation accessibility beyond codebase-aware agents to any LLM with web access.

**Independent Test**: Can be tested by fetching docs.mcpproxy.app/llms.txt and verifying it returns a properly formatted llms.txt file with links to documentation sections.

**Acceptance Scenarios**:

1. **Given** an LLM fetches docs.mcpproxy.app/llms.txt, **When** the response is received, **Then** it contains a table of contents with links to all documentation sections
2. **Given** an LLM fetches docs.mcpproxy.app/llms-full.txt, **When** the response is received, **Then** it contains the complete documentation in a single markdown file
3. **Given** the llms.txt file, **When** parsed, **Then** it follows the llmstxt.org specification format (H1 title, blockquote summary, H2 sections with links)

---

### Edge Cases

- What happens when the Docusaurus build fails during release? (Non-blocking: release continues, docs deployment is skipped with clear error)
- What happens if Cloudflare Pages deployment fails? (Retry once, then fail the job with clear error message)
- How are broken internal links handled? (Build-time validation fails the CI check)
- What happens when docs/ content references non-existent pages? (Link validation during build)
- What happens if marketing site cross-repo update fails? (Non-blocking: release continues, failure logged, manual update can be done later)
- What if marketing site repo workflow is disabled? (Release continues, warning emitted)
- What if CLAUDE.md is exactly at threshold (38,000 or 40,000)? (Treat as exceeded - warn at 38k, fail at 40k)
- What if docs/ link in CLAUDE.md points to non-existent file? (CI should validate internal doc links)

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST build a Docusaurus site from markdown files in the repository's docs/ folder
- **FR-002**: System MUST deploy the built site to docs.mcpproxy.app subdomain via Cloudflare Pages
- **FR-003**: System MUST integrate documentation deployment into the existing release CI workflow (release.yml)
- **FR-004**: System MUST validate documentation build on all PRs (build check without deployment)
- **FR-005**: System MUST provide client-side search functionality across all documentation
- **FR-006**: System MUST support responsive design for mobile and desktop viewing
- **FR-007**: System MUST organize existing docs/ content into logical navigation sections
- **FR-008**: System MUST include a "Getting Started" guide as the entry point
- **FR-009**: System MUST include configuration reference documentation
- **FR-010**: System MUST include feature documentation (OAuth, Docker isolation, code execution, etc.)
- **FR-011**: System MUST provide "Edit this page" links pointing to the GitHub repository
- **FR-012**: System MUST maintain consistent branding with the mcpproxy.app marketing site (colors, logo)
- **FR-013**: Deployment MUST NOT block the release workflow - documentation build failure should be reported but not prevent binary releases
- **FR-014**: System MUST store documentation source files in the same repository as the code (see Architecture Decision below)
- **FR-015**: Release CI MUST trigger cross-repo workflow to update marketing site download links with new version
- **FR-016**: Marketing site update MUST replace all versioned download URLs in index.astro and installation.astro
- **FR-017**: Marketing site update MUST NOT block the release workflow - failures should be logged but not prevent releases
- **FR-018**: CI MUST check CLAUDE.md file size on every PR and warn if >38,000 characters
- **FR-019**: CI MUST fail the build if CLAUDE.md exceeds 40,000 characters
- **FR-020**: CLAUDE.md MUST be refactored to contain only essential summaries with links to detailed docs/
- **FR-021**: CLAUDE.md MUST include links to docs.mcpproxy.app for each major topic area
- **FR-022**: Initial documentation site MUST include user-facing documentation for: Installation, Configuration, CLI Commands, REST API (with Swagger), Web UI, and Features
- **FR-023**: Documentation MUST include screenshots of Web UI captured via Playwright MCP or placeholder images for manual insertion
- **FR-024**: System MUST generate llms.txt file following the llmstxt.org standard for LLM-friendly documentation access
- **FR-025**: System MUST generate llms-full.txt containing complete documentation in a single file for LLMs with large context windows
- **FR-026**: Documentation site MUST display the current MCPProxy version (minor version 0.X.*) prominently, updated automatically during release CI

### Key Entities

- **Documentation Page**: A markdown file with frontmatter metadata (title, description, sidebar position)
- **Documentation Section**: A logical grouping of pages (e.g., "Getting Started", "Configuration", "Features")
- **Navigation Sidebar**: Hierarchical menu structure generated from docs/ folder structure
- **Search Index**: Generated index of all documentation content for client-side search

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Documentation site loads completely within 3 seconds on standard broadband connection
- **SC-002**: 100% of existing docs/ markdown files are included and accessible in the built site
- **SC-003**: Documentation deployment completes within 5 minutes as part of release CI
- **SC-004**: Site achieves Lighthouse accessibility score of 90+
- **SC-005**: All internal documentation links are validated at build time with zero broken links
- **SC-006**: Search functionality returns relevant results for 95% of queries against known documentation keywords
- **SC-007**: Site is fully functional on latest versions of Chrome, Firefox, Safari, and Edge
- **SC-008**: Marketing site download links are updated within 10 minutes of a release being published
- **SC-009**: CLAUDE.md size is reduced to under 25,000 characters after refactoring
- **SC-010**: CLAUDE.md size check runs on every PR and completes within 10 seconds
- **SC-011**: All 15 required documentation pages are created and accessible on docs.mcpproxy.app
- **SC-012**: REST API documentation includes all endpoints from oas/swagger.yaml
- **SC-013**: At least 9 Web UI screenshots are included in documentation (captured or placeholder)
- **SC-014**: llms.txt and llms-full.txt files are generated and accessible at docs.mcpproxy.app/llms.txt and docs.mcpproxy.app/llms-full.txt
- **SC-015**: Documentation site displays current MCPProxy minor version (e.g., "v0.11") in navbar or announcement bar

## Architecture Decision: Same Repository vs. Separate Repository

### Recommendation: Same Repository (docs/ folder)

After thorough research, the documentation should be kept **in the same repository** as the code. Here is the analysis:

### Same Repository Approach (Recommended)

**Advantages:**

| Benefit                     | Description                                                     |
|-----------------------------|-----------------------------------------------------------------|
| Documentation stays in sync | When code changes, docs can be updated in the same PR           |
| Simpler contribution flow   | Contributors make one PR for code + docs changes                |
| Single CI/CD pipeline       | Docs build integrates with existing release.yml                 |
| Open source friendly        | All content visible in one place for community                  |
| Version alignment           | Docs naturally align with code versions                         |
| No cross-repo permissions   | Contributors don't need access to multiple repos                |

**Disadvantages:**

| Concern              | Mitigation                                                          |
|----------------------|---------------------------------------------------------------------|
| Increases repo size  | Docusaurus is lightweight; node_modules are not committed           |
| Could slow CI        | Docs build is fast (<2 min); only runs on releases                  |
| Mixed concerns       | Clear folder separation: /docs for content, /website for config    |

### Separate Repository Approach (Not Recommended)

**Disadvantages for this project:**

| Concern                   | Impact                                                   |
|---------------------------|----------------------------------------------------------|
| Docs fall out of sync     | Major risk - documentation becomes stale                 |
| Complex contribution flow | Contributors must coordinate across repos                |
| Cross-repo triggers       | Need complex CI to rebuild docs when code changes        |
| Permission management     | Separate access controls for community contributors      |
| Open source friction      | Community must learn/navigate multiple repos             |

**When separate repo makes sense (not applicable here):**
- Very large organizations with dedicated documentation teams
- Multiple products sharing one documentation portal
- Documentation with completely independent release cycles

### Industry Evidence

Based on Docusaurus community discussions and open source best practices:

- Most successful open source projects keep docs in the main repo
- React Native saw contributions "skyrocket" by making docs accessible in plain markdown
- Cross-repo publishing adds complexity: "If content changes in one repo, I have to trigger the build of the docs repo"

### Implementation for docs.mcpproxy.app

The documentation will be hosted on Cloudflare Pages at docs.mcpproxy.app, separately from the marketing site at mcpproxy.app (which uses Cloudflare Workers from a different repository).

**Why Cloudflare Pages:**
- Free tier is generous for open source
- Automatic HTTPS for custom domains
- Native Docusaurus support with build presets
- Easy subdomain configuration (docs.mcpproxy.app CNAME)

**Integration with marketing site:**
- Marketing site (mcpproxy.app) links to docs.mcpproxy.app
- Docs site links back to marketing site
- Consistent branding maintained through shared color palette and logo
- Each site is independently deployed - no complex routing needed

## Cross-Repository CI Integration: Marketing Site Link Updates

The marketing site at mcpproxy.app (repository: `smart-mcp-proxy/mcpproxy.app-website`) contains hardcoded version numbers in download links that must be updated on each release.

### Files Requiring Updates

| File | Links to Update |
|------|-----------------|
| `src/pages/index.astro` | Windows AMD64 installer, macOS ARM64 DMG, Linux AMD64 binary |
| `src/pages/docs/installation.astro` | All platform installers and versioned binary downloads |

### Version Pattern Examples

Links follow these patterns that need version replacement:
- `v0.10.10` → `v{NEW_VERSION}` in URLs like `/download/v0.10.10/mcpproxy-setup-v0.10.10-amd64.exe`
- `0.10.10` → `{NEW_VERSION_NO_V}` in URLs like `/mcpproxy-0.10.10-darwin-arm64-installer.dmg`

### Cross-Repo Trigger Mechanism

The release workflow in mcpproxy-go will trigger a workflow dispatch in the marketing site repository:
1. mcpproxy-go release workflow completes successfully
2. Triggers `repository_dispatch` event to `smart-mcp-proxy/mcpproxy.app-website`
3. Marketing site workflow receives new version number
4. Workflow updates version strings in both .astro files
5. Commits changes and deploys to Cloudflare Pages

### Authentication Requirements

- Personal Access Token (PAT) or GitHub App token with `repo` scope for cross-repo dispatch
- Token stored as secret in mcpproxy-go repository (e.g., `MARKETING_SITE_DISPATCH_TOKEN`)

## Initial Documentation Content (MVP)

The first version of the documentation site must cover essential user topics. Content will be generated from existing sources (CLAUDE.md, docs/, code, OpenAPI spec).

### Required Documentation Pages

| Section | Page | Content Source | Description |
|---------|------|----------------|-------------|
| Getting Started | Installation | CLAUDE.md, marketing site | Platform installers, Homebrew, binary downloads |
| Getting Started | Quick Start | CLAUDE.md | First run, basic configuration, connecting to Cursor |
| Configuration | Config File Reference | CLAUDE.md, docs/configuration.md | mcp_config.json structure and options |
| Configuration | Adding Upstream Servers | CLAUDE.md | Stdio, HTTP, OAuth server configuration |
| Configuration | Environment Variables | CLAUDE.md | MCPPROXY_* variables reference |
| CLI | Command Reference | CLAUDE.md, code | All CLI commands with examples |
| CLI | Management Commands | docs/cli-management-commands.md | upstream, doctor, tools, auth commands |
| API | REST API Reference | oas/swagger.yaml | OpenAPI-based API documentation |
| API | MCP Protocol | CLAUDE.md | Built-in tools: retrieve_tools, call_tool, etc. |
| Web UI | Dashboard Guide | Frontend code | Web UI features and usage |
| Features | Docker Isolation | docs/docker-isolation.md | Container security for MCP servers |
| Features | OAuth Authentication | docs/oauth-*.md | OAuth 2.1 setup and troubleshooting |
| Features | Code Execution | docs/code_execution/*.md | JavaScript orchestration feature |
| Features | Security Quarantine | CLAUDE.md | TPA protection and server approval |
| Features | Search & Discovery | CLAUDE.md | BM25 tool search functionality |

### Content Generation Guidelines

1. **Extract from CLAUDE.md**: Move detailed sections from CLAUDE.md to dedicated docs pages
2. **Preserve technical accuracy**: Content must match current implementation
3. **User-focused language**: Write for end users, not developers
4. **Include examples**: Each feature doc should have working examples
5. **Cross-reference**: Link related topics together
6. **Include screenshots**: Use Playwright MCP to capture Web UI screenshots where applicable

### Screenshots for Documentation

Documentation pages should include screenshots to improve user understanding. Screenshots will be captured from the running Web UI using Playwright MCP tool (`mcp__playwright__browser_take_screenshot`).

#### Required Screenshots

| Page | Screenshot | Description | Capture Method |
|------|------------|-------------|----------------|
| Quick Start | Dashboard overview | Main Web UI dashboard showing server status | Playwright MCP |
| Adding Upstream Servers | Server list | Web UI showing configured upstream servers | Playwright MCP |
| Adding Upstream Servers | Add server form | Form for adding new MCP server | Playwright MCP |
| Web UI Dashboard | Full dashboard | Complete dashboard with all panels | Playwright MCP |
| Web UI Dashboard | Server details | Expanded server details view | Playwright MCP |
| Security Quarantine | Quarantine list | Quarantined servers awaiting approval | Playwright MCP |
| Security Quarantine | Approval dialog | Server approval/rejection interface | Playwright MCP |
| OAuth Authentication | OAuth status | Server showing OAuth authentication state | Playwright MCP |
| Search & Discovery | Tool search | Search results for tool discovery | Playwright MCP |

#### Screenshot Specifications

- **Format**: PNG
- **Width**: 1072px (optimized for documentation)
- **Location**: `docs/images/` or `website/static/img/`
- **Naming**: `{feature}-{description}.png` (e.g., `dashboard-overview.png`)
- **Alt text**: Required for accessibility

#### Placeholder Format

If screenshot cannot be captured automatically, use placeholder:

```markdown
![Dashboard Overview](./images/dashboard-overview.png)
<!-- TODO: Capture screenshot of main dashboard showing server status -->
```

#### Screenshot Capture Process

1. Start mcpproxy with Web UI enabled (`./mcpproxy serve`)
2. Navigate to relevant page using `mcp__playwright__browser_navigate`
3. Wait for content to load using `mcp__playwright__browser_wait_for`
4. Capture screenshot using `mcp__playwright__browser_take_screenshot`
5. Save to documentation images folder

### CLAUDE.md Refactoring

After documentation is generated, CLAUDE.md must be refactored to:

1. **Keep essential summaries** (~1-2 paragraphs per major topic)
2. **Add doc links** in format: `See [docs/topic.md](docs/topic.md) for details`
3. **Remove verbose content** that now lives in docs/
4. **Target size**: Under 25,000 characters (with buffer below 38k warning threshold)
5. **Preserve agent context**: Keep enough info for agents to understand the codebase structure

### CLAUDE.md Link Format

For each major section, include a reference like:
```markdown
## Feature Name

Brief 2-3 sentence summary of the feature.

**Documentation**: See [Feature Guide](docs/feature-guide.md) for detailed configuration and examples.
```

## CLAUDE.md Size Check CI Workflow

A new CI workflow will be added to check CLAUDE.md size on every PR.

### Thresholds

| Size | Action |
|------|--------|
| ≤38,000 chars | Pass silently |
| 38,001-40,000 chars | Pass with warning annotation |
| >40,000 chars | Fail build with error |

### Error Message Format

When threshold exceeded:
```
❌ CLAUDE.md size check failed!

Current size: 41,234 characters
Limit: 40,000 characters
Exceeded by: 1,234 characters

To fix this:
1. Move detailed content to docs/ folder
2. Replace with brief summary + link to docs
3. See docs/contributing.md for guidelines

Example:
  Before: [500 lines of detailed OAuth setup]
  After:  "See docs/oauth-setup.md for OAuth configuration details."
```

## Assumptions

- Cloudflare Pages account is available with permissions to add docs.mcpproxy.app domain
- DNS for mcpproxy.app allows adding CNAME record for docs subdomain
- Existing docs/ markdown files are suitable for Docusaurus with minimal restructuring
- Docusaurus v3 (latest stable) will be used for the site generator
- Node.js 20 (already in CI) is compatible with Docusaurus build requirements
- GitHub Actions secrets for Cloudflare (API token, account ID) will be configured
- PAT or GitHub App token with cross-repo dispatch permissions will be created and stored as secret
- Marketing site repository (`smart-mcp-proxy/mcpproxy.app-website`) will have a workflow that accepts `repository_dispatch` events

## Out of Scope

- Documentation versioning (tracking multiple versions of docs) - can be added later
- Internationalization/translations - English only for initial release
- User authentication or gated content - all docs are public
- Comments or user feedback on documentation pages
- Integration with external documentation tools (ReadMe, GitBook, etc.)
- Automated API documentation generation from code annotations
- Major redesign of marketing site content (only version link updates are in scope)

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #189` - Links the commit to the issue without auto-closing

### Co-Authorship
- Do NOT include AI tool attribution in commits

### Example Commit Message
```
feat: add Docusaurus documentation site with Cloudflare Pages deployment

Related #189

Implements documentation site at docs.mcpproxy.app to address CLAUDE.md size
concerns and provide user-friendly documentation browsing experience.

## Changes
- Add Docusaurus configuration in /website folder
- Restructure docs/ content for site navigation
- Add documentation build to release CI workflow
- Configure Cloudflare Pages deployment

## Testing
- Verified local Docusaurus build succeeds
- Confirmed all existing docs render correctly
- Tested search functionality
- Validated mobile responsiveness
```
