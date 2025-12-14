# Tasks: Docusaurus Documentation Site

**Input**: Design documents from `/specs/012-docusaurus-docs-site/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, quickstart.md

**Tests**: Tests are NOT explicitly requested - build validation and link checking serve as quality gates.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Docusaurus site**: `website/` at repository root
- **Documentation content**: `docs/` at repository root
- **CI workflows**: `.github/workflows/`
- **Agent context**: `CLAUDE.md` at repository root

---

## Phase 1: Setup (Docusaurus Project Initialization)

**Purpose**: Create Docusaurus project structure and install dependencies

- [x] T001 Create website/ directory structure per plan.md in website/
- [x] T002 Initialize Docusaurus project with package.json in website/package.json
- [x] T003 [P] Create docusaurus.config.js with base configuration in website/docusaurus.config.js
- [x] T004 [P] Create sidebars.js with navigation structure in website/sidebars.js
- [x] T005 [P] Create prepare-docs.sh script to copy docs/ to website/docs/ in website/prepare-docs.sh
- [x] T006 [P] Create custom.css with brand colors matching mcpproxy.app in website/src/css/custom.css
- [x] T007 [P] Copy logo.svg from existing assets to website/static/img/logo.svg
- [x] T008 Install npm dependencies: @docusaurus/core, @docusaurus/preset-classic, react, react-dom in website/
- [x] T009 [P] Install search plugin: @easyops-cn/docusaurus-search-local in website/
- [x] T010 [P] Install llms.txt plugin: docusaurus-plugin-llms in website/
- [x] T011 Verify local build succeeds with `npm run build` in website/
- [x] T011a [P] Add .gitignore entries for website/build/, website/.docusaurus/, website/node_modules/, website/docs/ in .gitignore

**Checkpoint**: Docusaurus project builds successfully (empty content)

---

## Phase 2: Foundational (Documentation Structure)

**Purpose**: Create documentation folder structure and category metadata

**CRITICAL**: No user story content can begin until folder structure is ready

- [x] T012 Create docs/getting-started/_category_.json with category metadata
- [x] T013 [P] Create docs/configuration/_category_.json with category metadata
- [x] T014 [P] Create docs/cli/_category_.json with category metadata
- [x] T015 [P] Create docs/api/_category_.json with category metadata
- [x] T016 [P] Create docs/web-ui/_category_.json with category metadata
- [x] T017 [P] Create docs/features/_category_.json with category metadata
- [x] T018 Create docs/images/ directory for screenshots
- [x] T019 Verify folder structure matches data-model.md file structure

**Checkpoint**: Documentation folder structure ready - content creation can begin

---

## Phase 3: User Story 1 - Developer Reads Documentation (Priority: P1) - MVP

**Goal**: Developer can visit docs.mcpproxy.app and navigate documentation

**Independent Test**: Visit local dev server, verify all navigation works, pages load correctly on desktop and mobile

### Implementation for User Story 1

#### Getting Started Section
- [x] T020 [US1] Create docs/getting-started/installation.md with platform installers and download links
- [x] T021 [P] [US1] Create docs/getting-started/quick-start.md with first run guide

#### Configuration Section
- [x] T022 [P] [US1] Create docs/configuration/config-file.md with mcp_config.json reference
- [x] T023 [P] [US1] Create docs/configuration/upstream-servers.md with stdio/HTTP/OAuth server setup
- [x] T024 [P] [US1] Create docs/configuration/environment-variables.md with MCPPROXY_* reference

#### CLI Section
- [x] T025 [P] [US1] Create docs/cli/command-reference.md with all CLI commands and examples
- [x] T026 [US1] Create docs/cli/management-commands.md with Docusaurus frontmatter

#### API Section
- [x] T027 [P] [US1] Create docs/api/rest-api.md with OpenAPI documentation from oas/swagger.yaml
- [x] T028 [P] [US1] Create docs/api/mcp-protocol.md with built-in tools documentation

#### Web UI Section
- [x] T029 [US1] Create docs/web-ui/dashboard.md with Web UI features and usage guide

#### Features Section
- [x] T030 [P] [US1] Create docs/features/docker-isolation.md with Docusaurus frontmatter
- [x] T031 [P] [US1] Create docs/features/oauth-authentication.md from existing oauth-*.md files
- [x] T032 [P] [US1] Create docs/features/code-execution.md with Docusaurus frontmatter
- [x] T033 [P] [US1] Create docs/features/security-quarantine.md with TPA protection guide
- [x] T034 [P] [US1] Create docs/features/search-discovery.md with BM25 search documentation

#### Verification
- [x] T035 [US1] Update sidebars.js to include all 15 documentation pages in website/sidebars.js
- [x] T036 [US1] Verify local build succeeds with all pages accessible
- [x] T037 [US1] Test mobile responsiveness using browser dev tools

**Checkpoint**: All 15 documentation pages created and navigable locally (SC-011)

---

## Phase 4: User Story 2 - Automatic Documentation Deployment (Priority: P1)

**Goal**: Documentation deploys automatically when release tag is pushed

**Independent Test**: Push test tag to branch, verify docs workflow runs and deploys to Cloudflare Pages

### Implementation for User Story 2

- [x] T038 [US2] Create .github/workflows/docs.yml for PR build validation
- [x] T039 [US2] Update .github/workflows/release.yml to add docs deployment job
- [x] T040 [US2] Add Cloudflare Pages deployment step using wrangler-action@v3 in release.yml
- [x] T041 [US2] Configure version injection: extract minor version from tag and inject into docusaurus.config.js
- [x] T042 [US2] Add continue-on-error: true to docs deployment to avoid blocking releases (FR-013)
- [ ] T043 [US2] Test docs.yml workflow on a PR with documentation changes

**Checkpoint**: CI workflow builds and deploys docs on release tags (SC-003)

---

## Phase 5: User Story 3 - Developer Searches Documentation (Priority: P2)

**Goal**: Search functionality works across all documentation

**Independent Test**: Use search box on local dev server, verify results appear for keywords like "OAuth", "Docker", "API key"

### Implementation for User Story 3

- [x] T044 [US3] Configure @easyops-cn/docusaurus-search-local plugin in docusaurus.config.js
- [x] T045 [US3] Verify search index is generated during build (check build output for search files)
- [x] T046 [US3] Test search with keywords: "installation", "OAuth", "Docker isolation", "API key"

**Checkpoint**: Search returns relevant results for 95% of test queries (SC-006)

---

## Phase 6: User Story 4 - Contributor Updates Documentation (Priority: P2)

**Goal**: Contributors can edit docs via GitHub and see changes after deployment

**Independent Test**: Edit a markdown file, submit PR, verify build passes, check edit link works

### Implementation for User Story 4

- [x] T047 [US4] Verify "Edit this page" links point to correct GitHub URLs in docusaurus.config.js
- [x] T048 [US4] Create docs/contributing.md with documentation contribution guidelines
- [ ] T049 [US4] Test end-to-end: edit file, verify docs.yml runs on PR

**Checkpoint**: Contributors can successfully submit documentation PRs

---

## Phase 7: User Story 5 - Cross-Site Navigation (Priority: P3)

**Goal**: Users can navigate between mcpproxy.app and docs.mcpproxy.app seamlessly

**Independent Test**: Click navigation links between sites, verify branding consistency

### Implementation for User Story 5

- [x] T050 [US5] Add "Home" link in navbar pointing to mcpproxy.app in docusaurus.config.js
- [x] T051 [US5] Verify footer links include marketing site reference
- [ ] T052 [US5] Test visual consistency: verify brand colors match mcpproxy.app

**Checkpoint**: Navigation between sites works, branding is consistent (FR-012)

---

## Phase 8: User Story 6 - Marketing Site Download Links Auto-Update (Priority: P2)

**Goal**: Marketing site download links update automatically on new release

**Independent Test**: Trigger repository_dispatch manually, verify marketing site workflow receives it

### Implementation for User Story 6

- [x] T053 [US6] Add repository_dispatch trigger to release.yml for marketing site update
- [x] T054 [US6] Create update-version.yml workflow template for mcpproxy.app-website repo
- [x] T055 [US6] Document required secret: MARKETING_SITE_DISPATCH_TOKEN (PAT with repo scope)
- [x] T056 [US6] Add continue-on-error: true to marketing trigger to avoid blocking releases (FR-017)

**Checkpoint**: Cross-repo dispatch configured (actual deployment depends on marketing site setup)

---

## Phase 9: User Story 7 - CLAUDE.md Size Prevention (Priority: P1)

**Goal**: CI checks CLAUDE.md size on every PR with warnings and failures

**Independent Test**: Create PR that modifies CLAUDE.md, verify size check runs and reports correctly

### Implementation for User Story 7

- [x] T057 [US7] Create .github/workflows/claude-md-check.yml with size validation
- [x] T058 [US7] Implement threshold logic: pass ≤38k, warn 38k-40k, fail >40k
- [x] T059 [US7] Add helpful error message with fix suggestions when threshold exceeded
- [ ] T060 [US7] Test workflow: create test PR with CLAUDE.md changes

**Checkpoint**: CLAUDE.md size check runs on every PR (SC-010)

---

## Phase 10: User Story 8 - AI Agent Accesses Extended Documentation (Priority: P1)

**Goal**: CLAUDE.md is streamlined with links to detailed docs

**Independent Test**: Read CLAUDE.md, verify all major sections have links to detailed docs

### Implementation for User Story 8

- [x] T061 [US8] Refactor CLAUDE.md: move detailed Docker isolation content to docs link
- [x] T062 [P] [US8] Refactor CLAUDE.md: move detailed OAuth content to docs link
- [x] T063 [P] [US8] Refactor CLAUDE.md: move detailed code execution content to docs link
- [x] T064 [P] [US8] Refactor CLAUDE.md: move detailed CLI commands to docs link
- [x] T065 [P] [US8] Refactor CLAUDE.md: move detailed API documentation to docs link
- [x] T066 [P] [US8] Refactor CLAUDE.md: move detailed configuration to docs link
- [x] T067 [US8] Add doc links in format: "See [docs/topic.md](docs/topic.md) for details"
- [x] T068 [US8] Verify CLAUDE.md size is under 25,000 characters (SC-009)

**Checkpoint**: CLAUDE.md <25k chars with links to all detailed docs

---

## Phase 11: User Story 9 - LLM Accesses Documentation via llms.txt (Priority: P2)

**Goal**: llms.txt and llms-full.txt are generated for LLM access

**Independent Test**: Build docs, verify llms.txt and llms-full.txt exist in build output

### Implementation for User Story 9

- [x] T069 [US9] Configure docusaurus-plugin-llms in docusaurus.config.js with includeOrder
- [x] T070 [US9] Verify llms.txt is generated with table of contents
- [x] T071 [US9] Verify llms-full.txt is generated with complete documentation
- [x] T072 [US9] Test llms.txt format follows llmstxt.org specification

**Checkpoint**: llms.txt and llms-full.txt accessible at docs.mcpproxy.app (SC-014)

---

## Phase 12: Screenshots (FR-023)

**Purpose**: Capture Web UI screenshots for documentation

- [ ] T073 [P] Capture dashboard-overview.png using Playwright MCP or add placeholder
- [ ] T074 [P] Capture server-list.png using Playwright MCP or add placeholder
- [ ] T075 [P] Capture add-server-form.png using Playwright MCP or add placeholder
- [ ] T076 [P] Capture server-details.png using Playwright MCP or add placeholder
- [ ] T077 [P] Capture quarantine-list.png using Playwright MCP or add placeholder
- [ ] T078 [P] Capture approval-dialog.png using Playwright MCP or add placeholder
- [ ] T079 [P] Capture oauth-status.png using Playwright MCP or add placeholder
- [ ] T080 [P] Capture tool-search.png using Playwright MCP or add placeholder
- [ ] T081 [P] Capture full-dashboard.png using Playwright MCP or add placeholder

**Checkpoint**: 9 screenshots in docs/images/ (SC-013)

---

## Phase 13: Polish & Cross-Cutting Concerns

**Purpose**: Final validation and refinements across all stories

- [ ] T082 Run Lighthouse audit and verify 90+ accessibility score (SC-004)
- [ ] T083 [P] Test site in Chrome, Firefox, Safari, and Edge (SC-007)
- [x] T084 [P] Verify all internal links are valid (zero broken links) (SC-005)
- [x] T085 [P] Verify version badge displays correctly in navbar (SC-015)
- [x] T086 Run quickstart.md validation steps
- [x] T087 Final review: ensure all 15 documentation pages render correctly (SC-011)
- [x] T088 Verify REST API documentation includes all endpoints from swagger.yaml (SC-012)

**Checkpoint**: All success criteria validated

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 - BLOCKS content creation
- **User Stories (Phase 3-11)**: All depend on Phase 2 completion
  - US1, US7, US8 are P1 priorities (MVP)
  - US2, US3, US4, US6, US9 are P2 priorities
  - US5 is P3 priority
- **Screenshots (Phase 12)**: Can run in parallel with any phase after foundational
- **Polish (Phase 13)**: Depends on all content phases being complete

### User Story Dependencies

| Story | Priority | Dependencies | Notes |
|-------|----------|--------------|-------|
| US1 (Docs Content) | P1 | Phase 2 | MVP - core documentation |
| US2 (Auto Deploy) | P1 | US1 | Needs content to deploy |
| US7 (Size Check) | P1 | None | Independent CI workflow |
| US8 (CLAUDE.md) | P1 | US1 | Needs docs to link to |
| US3 (Search) | P2 | US1 | Needs content to search |
| US4 (Contributor) | P2 | US1, US2 | Needs docs + CI working |
| US6 (Marketing) | P2 | US2 | Needs release workflow |
| US9 (llms.txt) | P2 | US1 | Needs content to generate |
| US5 (Cross-site) | P3 | US1 | Low priority polish |

### Parallel Opportunities

**Phase 1 - Setup (parallel tasks)**:
- T003, T004, T005, T006, T007 can run in parallel
- T009, T010 can run in parallel

**Phase 2 - Foundational (parallel tasks)**:
- T013, T014, T015, T016, T017 can run in parallel

**Phase 3 - US1 Content (parallel tasks)**:
- All documentation pages in different sections can be created in parallel
- T020-T021 (Getting Started) in parallel
- T022-T024 (Configuration) in parallel
- T027-T028 (API) in parallel
- T030-T034 (Features) in parallel

**Phase 10 - US8 CLAUDE.md (parallel tasks)**:
- T062-T066 refactoring tasks can run in parallel

**Phase 12 - Screenshots (all parallel)**:
- All screenshot tasks can run in parallel

---

## Parallel Example: User Story 1 - Content Creation

```bash
# Launch all Getting Started docs together:
Task: "Create docs/getting-started/installation.md"
Task: "Create docs/getting-started/quick-start.md"

# Launch all Configuration docs together:
Task: "Create docs/configuration/config-file.md"
Task: "Create docs/configuration/upstream-servers.md"
Task: "Create docs/configuration/environment-variables.md"

# Launch all Features docs together:
Task: "Enhance docs/docker-isolation.md"
Task: "Create docs/features/oauth-authentication.md"
Task: "Create docs/features/security-quarantine.md"
Task: "Create docs/features/search-discovery.md"
```

---

## Implementation Strategy

### MVP First (User Stories 1, 2, 7, 8)

1. Complete Phase 1: Setup (Docusaurus project)
2. Complete Phase 2: Foundational (folder structure)
3. Complete Phase 3: User Story 1 (15 documentation pages)
4. Complete Phase 9: User Story 7 (CLAUDE.md size check)
5. Complete Phase 10: User Story 8 (CLAUDE.md refactoring)
6. Complete Phase 4: User Story 2 (CI deployment)
7. **STOP and VALIDATE**: Test locally, verify docs build and deploy
8. Deploy/demo if ready - MVP complete!

### Incremental Delivery

1. Setup + Foundational → Project ready
2. Add US1 (Content) → Local docs browsable → Demo!
3. Add US7 + US8 (CLAUDE.md) → Size check + refactoring complete
4. Add US2 (CI) → Auto-deployment working
5. Add US3 (Search) → Search functional
6. Add US9 (llms.txt) → LLM access ready
7. Add US4, US5, US6 → Full feature set
8. Screenshots + Polish → Production ready

### Suggested MVP Scope

**Minimum viable product includes**:
- Phase 1-2: Setup and structure
- Phase 3: All 15 documentation pages (US1)
- Phase 9: CLAUDE.md size check (US7)
- Phase 10: CLAUDE.md refactoring (US8)
- Phase 4: CI deployment (US2)

**Total MVP tasks**: ~45 tasks (T001-T043, T057-T068)

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Screenshots can be placeholders initially, captured later
- CLAUDE.md refactoring is critical for solving issue #189
- Docusaurus build validation catches most issues (broken links, missing frontmatter)
- Cross-repo marketing trigger requires separate setup in mcpproxy.app-website repo
