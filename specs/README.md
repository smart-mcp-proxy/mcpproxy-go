# Specs Index

Every numbered directory under `specs/` is a feature specification produced with [GitHub spec-kit](https://github.com/github/spec-kit). This page is the canonical list; badges reflect `tasks.md` checklist progress and are a quick heuristic — not a guarantee. When ambiguous, cross-check `git log --grep='<spec-number>'` and the spec's `plan.md`.

**Status legend**

- `shipped` — ≥ 95 % of `tasks.md` items checked
- `in-flight` — 1–94 % checked
- `drafted` — spec/plan written, `tasks.md` empty or unchecked
- `—` — no `tasks.md` in the directory (doc-only spec or pre-speckit draft)

## Operational runbooks

- [`docs/release-runbook.md`](../docs/release-runbook.md) — SPOFs in the release pipeline (macOS notarize, Windows sign, Claude notes, Cloudflare R2 apt/rpm, Homebrew tap, `next` branch hygiene)

## Related design docs

Brainstormed design docs that feed future specs live under [`docs/superpowers/specs/`](../docs/superpowers/specs/):

- [`2026-03-23-telemetry-and-feedback-design.md`](../docs/superpowers/specs/2026-03-23-telemetry-and-feedback-design.md) — MCPProxy Telemetry & Feedback — Design Spec
- [`2026-03-30-ci-swift-tray-build-design.md`](../docs/superpowers/specs/2026-03-30-ci-swift-tray-build-design.md) — Design: CI Build for Swift macOS Tray App + Installer Updates
- [`2026-04-24-diagnostics-error-taxonomy-design.md`](../docs/superpowers/specs/2026-04-24-diagnostics-error-taxonomy-design.md) — Diagnostics & error taxonomy deep-dive
- [`2026-04-24-retention-telemetry-hygiene-design.md`](../docs/superpowers/specs/2026-04-24-retention-telemetry-hygiene-design.md) — Retention telemetry hygiene + activation instrumentation + auto-start defaults
- [`macos-design-guide.md`](../docs/superpowers/specs/macos-design-guide.md) — MCPProxy macOS App Design Guide

## Numbered specs

| # | Title | Status | Progress |
| --- | --- | --- | --- |
| [001-code-execution](./001-code-execution/) | JavaScript Code Execution Tool for MCP Tool Composition | `drafted` | 0/127 (0%) |
| [001-fix-skipped-auth-tests](./001-fix-skipped-auth-tests/) | Fix Skipped API Key Authentication Tests | — | — |
| [001-oas-endpoint-documentation](./001-oas-endpoint-documentation/) | Complete OpenAPI Documentation for REST API Endpoints | `in-flight` | 49/69 (71%) |
| [001-oauth-scope-discovery](./001-oauth-scope-discovery/) | OAuth Scope Auto-Discovery | — | — |
| [001-update-version-display](./001-update-version-display/) | Update Check Enhancement & Version Display | `in-flight` | 11/58 (19%) |
| [002-windows-installer](./002-windows-installer/) | Windows Installer for MCPProxy | `in-flight` | 25/60 (42%) |
| [003-tool-annotations-webui](./003-tool-annotations-webui/) | Tool Annotations & MCP Sessions in WebUI | `in-flight` | 10/64 (16%) |
| [004-management-health-refactor](./004-management-health-refactor/) | Management Service Refactoring & OpenAPI Generation | `in-flight` | 45/101 (45%) |
| [005-rest-management-integration](./005-rest-management-integration/) | REST Endpoint Management Service Integration | `shipped` | 45/45 (100%) |
| [006-oauth-extra-params](./006-oauth-extra-params/) | OAuth Extra Parameters Support | `in-flight` | 31/65 (48%) |
| [007-oauth-e2e-testing](./007-oauth-e2e-testing/) | OAuth E2E Testing & Observability | `in-flight` | 88/103 (85%) |
| [008-oauth-token-refresh](./008-oauth-token-refresh/) | OAuth Token Refresh Bug Fixes and Logging Improvements | `in-flight` | 57/64 (89%) |
| [009-proactive-oauth-refresh](./009-proactive-oauth-refresh/) | Proactive OAuth Token Refresh & UX Improvements | `drafted` | 0/87 (0%) |
| [010-release-notes-generator](./010-release-notes-generator/) | Release Notes Generator | `in-flight` | 24/36 (67%) |
| [011-resource-auto-detect](./011-resource-auto-detect/) | Auto-Detect RFC 8707 Resource Parameter for OAuth Flows | `shipped` | 39/39 (100%) |
| [012-docusaurus-docs-site](./012-docusaurus-docs-site/) | Docusaurus Documentation Site | `in-flight` | 74/89 (83%) |
| [012-unified-health-status](./012-unified-health-status/) | Unified Health Status | `shipped` | 44/44 (100%) |
| [013-structured-server-state](./013-structured-server-state/) | Structured Server State | `shipped` | 46/46 (100%) |
| [013-tool-change-notifications](./013-tool-change-notifications/) | Subscribe to notifications/tools/list_changed for Automatic Tool Re-indexing | `in-flight` | 26/45 (58%) |
| [014-cli-output-formatting](./014-cli-output-formatting/) | CLI Output Formatting System | `shipped` | 65/66 (98%) |
| [015-server-management-cli](./015-server-management-cli/) | Server Management CLI | `shipped` | 50/50 (100%) |
| [016-activity-log-backend](./016-activity-log-backend/) | Activity Log Backend | `drafted` | 0/50 (0%) |
| [017-activity-cli-commands](./017-activity-cli-commands/) | Activity CLI Commands | `drafted` | 0/60 (0%) |
| [018-intent-declaration](./018-intent-declaration/) | Intent Declaration with Tool Split | `shipped` | 69/69 (100%) |
| [019-activity-webui](./019-activity-webui/) | Activity Log Web UI | `shipped` | 73/73 (100%) |
| [020-oauth-login-feedback](./020-oauth-login-feedback/) | OAuth Login Error Feedback | — | — |
| [021-request-id-logging](./021-request-id-logging/) | Request ID Logging | `in-flight` | 20/42 (48%) |
| [022-oauth-redirect-uri-persistence](./022-oauth-redirect-uri-persistence/) | OAuth Redirect URI Port Persistence | `shipped` | 24/25 (96%) |
| [023-oauth-state-persistence](./023-oauth-state-persistence/) | OAuth Token Refresh Reliability | `shipped` | 38/39 (97%) |
| [023-smart-config-patch](./023-smart-config-patch/) | Smart Config Patching | `shipped` | 52/53 (98%) |
| [024-expand-activity-log](./024-expand-activity-log/) | Expand Activity Log | `shipped` | 63/66 (95%) |
| [026-pii-detection](./026-pii-detection/) | Sensitive Data Detection | `shipped` | 130/130 (100%) |
| [027-status-command](./027-status-command/) | Status Command | `shipped` | 25/25 (100%) |
| [028-agent-tokens](./028-agent-tokens/) | Agent Tokens | `drafted` | 0/43 (0%) |
| [029-mcpproxy-teams](./029-mcpproxy-teams/) | MCPProxy Teams | `shipped` | 29/29 (100%) |
| [033-typescript-code-execution](./033-typescript-code-execution/) | TypeScript Code Execution Support | `drafted` | 0/19 (0%) |
| [034-expand-secret-refs](./034-expand-secret-refs/) | Expand Secret/Env Refs in All Config String Fields | `shipped` | 17/17 (100%) |
| [035-enhanced-annotations](./035-enhanced-annotations/) | Enhanced Tool Annotations Intelligence | — | — |
| [037-macos-swift-tray](./037-macos-swift-tray/) | Native macOS Swift Tray App (Spec A) | — | — |
| [038-mcp-accessibility-server](./038-mcp-accessibility-server/) | MCP Accessibility Testing Server (Spec C) | — | — |
| [039-connect-and-dashboard](./039-connect-and-dashboard/) | Connect Clients & Dashboard Visual Redesign | — | — |
| [039-scanner-qa-audit](./039-scanner-qa-audit/) | Security Scanner QA Audit & Fix | — | — |
| [039-security-scanner-plugins](./039-security-scanner-plugins/) | Security Scanner Plugin System | — | — |
| [040-server-ux](./040-server-ux/) | Add/Edit Server UX Improvements | `drafted` | 0/35 (0%) |
| [041-quarantine-invariants](./041-quarantine-invariants/) | Quarantine State Machine Invariants & Property Tests | — | — |
| [042-telemetry-tier2](./042-telemetry-tier2/) | Telemetry Tier 2 — Privacy-Respecting Usage Signals | `drafted` | 0/91 (0%) |
| [043-linux-package-repos](./043-linux-package-repos/) | Linux Package Repositories (apt/yum) | `shipped` | 39/41 (95%) |

## Updating this index

The index is not auto-generated. Refresh the table when you:

- add a new numbered spec directory under `specs/`
- ship or abandon an existing spec (adjust the badge)
- add a design doc under `docs/superpowers/specs/`

Future-you will thank present-you for a short PR update when the status actually changes, so the badges stay honest.
