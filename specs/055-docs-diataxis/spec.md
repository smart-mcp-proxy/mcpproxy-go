# Feature Specification: Documentation Diátaxis Restructure

**Feature Branch**: `055-docs-diataxis`
**Created**: 2026-05-23
**Status**: Draft
**Input**: Restructure docs.mcpproxy.app around the four Diátaxis quadrants (Spec 053 WP-G1).

## Context: current documentation state

- **Generator**: docs.mcpproxy.app is a **Docusaurus 3** site (`website/`), publishing a curated subset of the repo `docs/` tree via a `docs.include[]` allowlist and a hand-maintained `website/sidebars.js`. Deployed to Cloudflare Pages.
- **Scale**: ~133 markdown files in `docs/`; only ~71 are published. ~62 are internal artifacts (plans, proposals, designs, QA reports, speckit specs, code reviews) that pollute the tree and risk accidental publication.
- **Core problem**: docs are organised by **topic/audience**, not by Diátaxis **user-need type**. The 19-file `features/` directory is a catch-all where nearly every page mixes Explanation + Reference + How-to. There is **no true learning-oriented Tutorial anywhere**, and **no dedicated Explanation quadrant** (the "why" is scattered as page preambles).
- **Bright spot**: the `errors/` catalog (README + 29 `MCPX_*` codes) already models clean Reference + How-to and is hard-linked from product code — its URLs must be frozen.
- **Out of scope**: the separate Astro marketing site at `mcpproxy.app` (cross-link target only).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — A newcomer can succeed on first contact (Priority: P1)

As a developer new to mcpproxy, I want a single guided "your first proxy" lesson that takes me from install to a working tool call with guaranteed success, so I learn the product without assembling steps from scattered how-to pages.

**Why this priority**: Tutorials are the biggest gap (currently zero). First-run success is the highest-impact UX improvement and the entry point to everything else.

**Independent Test**: Follow the new tutorial end-to-end on a clean machine with pinned versions; reach a successful tool call and see it in the activity log without consulting any other page.

**Acceptance Scenarios**:

1. **Given** a clean install, **When** a newcomer follows the "Your first proxy" tutorial top to bottom, **Then** they reach a working tool call with no dead ends and no assumed prior knowledge.
2. **Given** the tutorial, **When** read in the docs nav, **Then** it appears under a dedicated **Tutorials** section, distinct from How-to guides.

---

### User Story 2 — A working user finds the exact recipe or fact fast (Priority: P2)

As an operator with a specific task ("add an OAuth server", "create an agent token", "query the activity log"), I want a focused how-to recipe, and as someone configuring the system I want austere reference tables — each as its own page, not buried inside a mixed "feature" doc.

**Why this priority**: The bulk of existing content is usable but mis-shaped; extracting recipes and reference from the `features/` catch-all delivers immediate findability gains.

**Independent Test**: For a sample task, land on a how-to page that contains only the steps (no conceptual preamble, no full config dump); for a config question, land on a reference page that is a scannable table.

**Acceptance Scenarios**:

1. **Given** the restructured docs, **When** I look for how to do a task, **Then** I find a How-to page scoped to that task, separate from explanation and reference.
2. **Given** a configuration question, **When** I open the relevant Reference page, **Then** it is information-dense (tables/lists) without tutorial narrative.
3. **Given** each former `features/*` mixed page, **When** the restructure is complete, **Then** its content has been split across the correct quadrants and the `features/` catch-all no longer exists.

---

### User Story 3 — A user who wants to understand "why" has somewhere to read it (Priority: P3)

As an evaluator deciding whether/how to adopt mcpproxy, I want coherent Explanation pages (security model, tool-discovery/token-savings rationale, architecture) that I can read away from the keyboard.

**Why this priority**: The security model is mcpproxy's main differentiator but its rationale is fragmented across five mixed pages; a unified Explanation quadrant turns scattered fragments into a compelling narrative.

**Independent Test**: Read the "Security model" explanation page and understand TPA → quarantine → tool-level quarantine → sensitive-data detection → intent declaration as one story, without needing config tables or step lists.

**Acceptance Scenarios**:

1. **Given** the restructured docs, **When** I look for conceptual understanding, **Then** there is a dedicated **Explanation** section with a unified security-model page and an architecture page.
2. **Given** duplicate architecture/config pages exist today, **When** the restructure completes, **Then** stale duplicates are removed and a single canonical page remains.

---

### User Story 4 — The docs tree is clean and links never break (Priority: P2)

As a maintainer, I want internal engineering artifacts out of `docs/` and every moved page redirected, so the published tree is trustworthy and existing inbound/product links keep working.

**Why this priority**: ~62 internal files risk accidental publication; restructuring will move most URLs, and product code hard-links to `docs.mcpproxy.app/errors/<CODE>`.

**Independent Test**: Build the site with broken-link checking enabled (must pass); click-test redirects for top moved pages and all `/errors/<CODE>` deep links referenced from product code.

**Acceptance Scenarios**:

1. **Given** the restructure, **When** the docs site builds, **Then** the build passes with broken-link checking on.
2. **Given** a previously published page that moved, **When** its old URL is requested, **Then** a redirect resolves to the new location.
3. **Given** the `/errors/<CODE>` URLs referenced from product code, **When** the restructure completes, **Then** those URLs are unchanged (frozen).
4. **Given** the ~62 internal artifacts, **When** the restructure completes, **Then** they no longer live under `docs/` (moved to `specs/` or an internal archive) and are not published.

### Edge Cases

- A page legitimately serves two needs → split it; do not leave a hybrid.
- The ready-made `code_execution/` set (overview/examples/api-reference/troubleshooting) is currently unpublished but Diátaxis-shaped → publish it rather than rewrite.
- Moving a page that other docs link to internally → broken-link checker must catch it at build; external inbound links need a redirect.
- A tutorial that drifts out of date silently → tutorials must be verified against a live install as part of acceptance.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Documentation MUST be organised under the four Diátaxis quadrants — Tutorials, How-to guides, Reference, Explanation — as the primary navigation, with Web UI and Operations as audience sub-areas.
- **FR-002**: At least one guaranteed-success **Tutorial** ("Your first proxy") MUST exist and be verified end-to-end on a clean install; a second tutorial (tool discovery) SHOULD follow.
- **FR-003**: Each former `features/*` mixed page MUST be decomposed into the correct quadrants (Explanation / Reference / How-to), after which the `features/` catch-all MUST be retired.
- **FR-004**: A dedicated **Explanation** quadrant MUST exist, including a unified security-model page and a consolidated architecture page.
- **FR-005**: Reference pages MUST be information-dense (tables/lists) without tutorial narrative; duplicate reference pages MUST be merged.
- **FR-006**: The ~62 internal engineering artifacts MUST be moved out of `docs/` (to `specs/` or an internal archive) and excluded from publication.
- **FR-007**: The ready-made `code_execution/` content MUST be published into the appropriate quadrants.
- **FR-008**: Every moved page MUST have a redirect from its old URL; the docs build MUST pass with broken-link checking enabled.
- **FR-009**: The `/errors/<CODE>` URLs (referenced from product code) MUST remain unchanged.
- **FR-010**: The orphaned-but-present pages (e.g. `cli/security-commands`, web-ui detail pages) MUST be re-added to the navigation.
- **FR-011**: The work MUST NOT require Go code changes (content + IA + build config only) and MUST keep the existing generator (Docusaurus) — no tooling migration.

### Key Entities

- **Diátaxis quadrant**: one of Tutorials / How-to / Reference / Explanation, each serving a distinct user need (learning / task / information / understanding).
- **Doc page**: a markdown file with exactly one Diátaxis type after restructure.
- **Redirect mapping**: old URL → new URL for every moved page.
- **Internal artifact**: a non-public engineering doc to be relocated out of `docs/`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new user can go from zero to a successful tool call by following a single tutorial, with a 100% step-success rate on a clean install (no dead ends).
- **SC-002**: Every published page maps to exactly one Diátaxis quadrant; the `features/` catch-all no longer exists.
- **SC-003**: Zero internal engineering artifacts remain published under `docs/`.
- **SC-004**: The docs site builds with broken-link checking enabled and zero broken internal links; 100% of moved pages resolve via redirect; all `/errors/<CODE>` URLs are unchanged.
- **SC-005**: A reader can find the unified security-model explanation as a single coherent page rather than fragments across five pages.

## Assumptions

- Keep Docusaurus (healthy: search, llms.txt, edit links, broken-link checking); restructuring is information-architecture + content surgery, not a generator migration.
- Estimated effort ~11–15 days; deliver incrementally (cleanup → tutorial → security explanation → publish code_execution → iterate sidebar).
- The `errors/` catalog is the model to emulate and its URLs are frozen.

## Non-Goals

- No Go code changes.
- No generator migration (no VitePress/MkDocs).
- No changes to the separate Astro marketing site beyond cross-linking.
- Not a content-accuracy audit of every page (a CLI-reference accuracy pass is a noted follow-up, not in scope here).

## Highest-value quick wins (suggested first PRs)

1. Clean `docs/` of internal artifacts (mechanical, pure win).
2. Write + verify the "Your first proxy" tutorial.
3. Write the unified "Security model" explanation.
4. Publish the existing `code_execution/` set.
5. Introduce the four-quadrant sidebar headers and migrate pages iteratively.

## Commit Message Conventions *(mandatory)*

- Use `Related #[issue]` (never `Fixes/Closes/Resolves`).
- Do **not** include `Co-Authored-By: Claude` or "Generated with Claude Code" (repo policy).
- Conventional Commits; e.g. `docs(055): add 'Your first proxy' tutorial`.
