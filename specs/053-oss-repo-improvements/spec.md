# Spec 053 — OSS Repo Improvements (umbrella / work-split)

**Status:** Decomposition spec — splits the remaining `oss_report.html` backlog into
independent work packages (WPs) so each can be executed in its own session/PR.
**Source:** `file:///Users/user/oss_report.html` (Parts 3.3 + 3.4).
**Date:** 2026-05-22 · **Owner:** @Dumbris

> This is an **umbrella spec**. Each Track below is independently shippable; when a
> session picks one up, give it its own `specs/<NNN>-<slug>/` spec+plan if non-trivial.
> "Owner: agent" = Claude can do it end-to-end; "Owner: user" = needs an account,
> Stripe, external submission, or human content; "mixed" = agent preps, user finalizes.

---

## Already shipped (context)

| PR | Delivered |
|----|-----------|
| #487 | Contributor Covenant CoC, issue templates, dependabot, coordinated-disclosure `SECURITY.md` (in `.github/`) → Community Standards 100% |
| #488 | README hero: frosted-tiles banner (`logo.svg`), web-UI demo GIF, macOS screenshots, cross-platform messaging, badge row, `scripts/demo/` pipeline; social-preview uploaded |
| #489 | Repo declutter (deleted report/backup junk, gitignore guards) |

`CONTRIBUTING.md` (docs/) and `pull_request_template.md` already existed.

---

## Track A — Finish Phase 1 (quick wins)

### WP-A1 · Kubernetes-style label taxonomy
- **Owner:** agent · **Effort:** S · **Deps:** none
- Add `kind/*`, `area/*`, `priority/*`, `triage/*`, `size/*` + keep `good first issue`/`help wanted`. Define in a checked-in `.github/labels.yml` and sync via an `EndBug/label-sync` (or `crazy-max/ghaction-github-labeler`) workflow so labels are version-controlled.
- **Accept:** `gh label list` shows the taxonomy; a labeler workflow keeps it in sync.

### WP-A2 · GitHub Sponsors + `FUNDING.yml`
- **Owner:** mixed (user opens Sponsors) · **Effort:** S · **Deps:** Sponsors enabled
- User enables GitHub Sponsors for @Dumbris/org (+Stripe). Then agent adds `.github/FUNDING.yml` (`github: [Dumbris]`, `custom: ["https://mcpproxy.app/sponsor"]`).
- **Accept:** Sponsor button live on the repo.

### WP-A3 · Move remaining community files into `.github/` (root −2)
- **Owner:** agent · **Effort:** S · **Deps:** none
- `git mv CODE_OF_CONDUCT.md .github/` and `.golangci.yml .github/` (both still detected/honored there). Update `.golangci.yml` references in CI/`scripts/run-linter.sh` if any.
- **Accept:** root file count drops 2; lint + community profile still green.

---

## Track B — Supply-chain security (CI)  ← highest leverage for a "security" product

### WP-B1 · CodeQL workflow
- **Owner:** agent · **Effort:** M · **Deps:** none
- `.github/workflows/codeql.yml` for Go + JavaScript (Vue frontend). Schedule + PR.

### WP-B2 · OpenSSF Scorecard workflow + README badge
- **Owner:** agent · **Effort:** M · **Deps:** none (publishes results)
- `ossf/scorecard-action` with `publish_results: true`; add the Scorecard badge.

### WP-B3 · Dependency-review on PRs
- **Owner:** agent · **Effort:** S · **Deps:** none
- `actions/dependency-review-action` gate on PR builds.

### WP-B4 · Trivy scan for Docker images
- **Owner:** agent · **Effort:** M · **Deps:** none
- `aquasecurity/trivy-action` on `scanner-images.yml`/release image builds.

### WP-B5 · Pin all GitHub Actions to commit SHAs
- **Owner:** agent · **Effort:** M · **Deps:** none
- 109 `@vN` action refs → 40-char SHAs (Scorecard `Pinned-Dependencies`, highest weight). Use `pin-github-action`/`frizbee`; let dependabot keep them current.
- **Accept:** `Pinned-Dependencies` check passes; dependabot configured for actions (already present).

### WP-B6 · Verify/harden branch protection on `main`
- **Owner:** mixed (settings) · **Effort:** S · **Deps:** none
- Already requires review (`REVIEW_REQUIRED`). Confirm: required status checks (lint+unit+e2e+CodeQL+Scorecard), linear history, dismiss-stale. Document in `docs/`.

---

## Track C — Release provenance

### WP-C1 · cosign keyless signing of release artifacts
- **Owner:** agent · **Effort:** M · **Deps:** none
- Sign the checksums file via sigstore keyless in `release.yml` (no GoReleaser migration needed — bolt onto the existing pipeline).

### WP-C2 · SBOM (Syft) attached to releases
- **Owner:** agent · **Effort:** M · **Deps:** none — `anchore/sbom-action`.

### WP-C3 · SLSA build provenance
- **Owner:** agent · **Effort:** M · **Deps:** none — `slsa-framework/slsa-github-generator`.

### WP-C4 · `CHANGELOG.md` via git-cliff
- **Owner:** agent · **Effort:** M · **Deps:** none
- Conventional Commits already in use → generate `CHANGELOG.md` with `git-cliff`, wire into `release.yml`.

### WP-C5 · commitlint CI check (optional)
- **Owner:** agent · **Effort:** S · **Deps:** none — enforce Conventional Commits on PRs.

---

## Track D — Discovery / distribution (mostly external)

### WP-D1 · pkg.go.dev + Go Report Card verification
- **Owner:** agent · **Effort:** S — confirm module indexes on pkg.go.dev and the Go Report Card grade; fix lint nits to reach A if needed.

### WP-D2 · MCP directory submissions
- **Owner:** mixed/user · **Effort:** L · **Deps:** accounts
- PR to `modelcontextprotocol/registry`; `smithery mcp publish`; mcp.so issue; pulsemcp form; mcphunt; glama claim. Agent can draft the registry PR + `server.json`.

### WP-D3 · Awesome-list PRs
- **Owner:** mixed · **Effort:** M
- `punkpeye/awesome-mcp-servers`, `appcypher/awesome-mcp-servers`, `avelino/awesome-go` (needs Go Report Card A + tests).

---

## Track E — Promotion / launch (user-led; agent can draft)

### WP-E1 · 3 anchor blog posts
- **Owner:** mixed · **Effort:** L — (a) "Cutting context tokens by 99%", (b) "Defending against Tool Poisoning Attacks", (c) "Why MCP needs a gateway layer". Agent drafts; lives in `mcpproxy.app-website`.

### WP-E2 · Coordinated 48-hour launch
- **Owner:** user · **Effort:** L — Show HN (Tue–Thu 08–11 ET) → r/mcp + r/LocalLLaMA + r/golang + r/selfhosted → X thread + LinkedIn → email first starrers. Target 500+ stars/24h.

### WP-E3 · Newsletter / podcast / CFP pitches
- **Owner:** user · **Effort:** M — Golang Weekly, TLDR AI, Ben's Bites, Latent Space; Go Time, Changelog; AI Engineer Summit / GopherCon CFP.

---

## Track F — Monetization (user-led)

### WP-F1 · Sponsor tiers + `mcpproxy.app/sponsor` page
- **Owner:** user · **Effort:** M — Caddy-style tiers ($25/$249/$999/$3k+); page in `mcpproxy.app-website`.

### WP-F2 · Sponsor outreach (~30 companies)
- **Owner:** user · **Effort:** L — Anthropic-ecosystem/IDE vendors; risk-mitigation framing (Filippo model).

### WP-F3 · Auto sponsors mosaic in README (`sponsorkit`)
- **Owner:** agent · **Effort:** S · **Deps:** WP-A2/WP-F1.

---

## Track G — Documentation

### WP-G1 · Diátaxis restructure of `docs.mcpproxy.app`
- **Owner:** mixed · **Effort:** XL — Tutorials / How-to / Reference / Explanation quadrants; `mcpproxy.app-website`.

---

## Recommended sequencing

1. **Now, agent-only, this repo:** Track B (B1–B5) + Track C (C1–C4) + A1, A3 — the engineering/security/provenance work that reinforces the product's security positioning and needs no external accounts. Bundle as 2–3 PRs (e.g. "security workflows", "release provenance", "labels+declutter").
2. **User unblocks:** A2/F1 (open Sponsors) → then F3.
3. **External, batchable:** D1–D3, then the E launch window once B/C give green security badges.
4. **Long-haul:** G1 docs, E1 blog drafts.

## Non-goals
- License change (stay MIT). No AGPL/SSPL (kills enterprise adoption per report 3.4).
- No GoReleaser migration — extend the existing `release.yml`.
