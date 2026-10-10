# Implementation Plan: MCP Credential Lifecycle

**Branch**: `115-mcp-credential-lifecycle` | **Date**: 2026-10-10 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/115-mcp-credential-lifecycle/spec.md` (issue #1552)

## Summary

This plan adds an admin-only built-in MCP tool, `credentials` (`list | get | create_client | create_token | revoke`). Through it a trusted admin agent can issue a locked custom client credential or a profile-pinned agent token, inspect safe metadata, and revoke either one, without the CLI or REST.

The tool is a thin adapter over a new `runtime.CredentialsService`:
- Client operations delegate to the existing `ClientsService`.
- Token create and revoke logic is moved out of the REST handler, so REST, CLI and MCP share one path for validation, the binding guard (enforced on MCP), `bindingWriteMu`, audit (`profile_change` with `issue`/`revoke`) and the live `credentials.changed` event.
- Visibility and per-call gates reuse the `profiles` tool's predicate and its `read_only_mode`/`disable_management` handling.
- Revocation reaches live sessions through the existing per-request token validation.

The Web UI gains issuer, lease, purpose and revoked-at display, client links in "Assigned to", live refresh, and an honest tier label. End-to-end proof uses an in-process daemon with dispatch-counting upstreams (`TestE2E_CredentialsLifecycle_*`), plus a live-binary harness run.

## Technical Context

**Language/Version**: Go 1.26 (backend), TypeScript 5.9 / Vue 3.5 (Web UI)
**Primary Dependencies**: existing only: mark3labs/mcp-go v1.0.0, bbolt, zap, chi, Vue/Pinia/daisyUI. **No new dependencies.**
**Storage**: BBolt `config.db`, `agent_tokens` bucket. Additive JSON fields on `auth.AgentToken`, no migration.
**Testing**: `go test -race` (unit, in `internal/runtime`, `internal/server`, `internal/httpapi`, `internal/storage`, `internal/auth`); `TestE2E_*` in `internal/server` (e2e lane); vitest (`frontend/tests/unit/*.spec.ts`); Playwright Web-UI sweep (`docs/development/web-ui-verification.md`); live harness on the built binary.
**Target Platform**: personal edition (macOS/Linux/Windows); the server edition is covered by behaviour checks only (A1).
**Project Type**: Go core plus an embedded Vue SPA.
**Performance Goals**: adding the tool costs at most 1,200 cl100k tokens on admin `tools/list` (SC-006). Issue and revoke take under 50 ms on a local store.
**Constraints**: secrets appear only in the one-time response (FR-023). The goldens change only by the enumerated `credentials` delta. Both lint passes (bare and `--build-tags server`) must stay clean.
**Scale/Scope**: up to 100 tokens (`auth.MaxTokens`); clients are unbounded but small.

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at scale | ✅ | No change on the per-request path. Token validation stays one bbolt lookup |
| II. Actor-based concurrency | ✅ | Mutations serialize on the existing `bindingWriteMu`. No new goroutines except the existing async last-used update |
| III. Configuration-driven | ✅ | No new config keys. Gates are read live from the config (`read_only_mode`, `disable_management`, `require_mcp_auth`) |
| IV. Security by default | ✅ | Admin-only visibility plus an execution-time check. Profile, expiry and guard are mandatory on the MCP path. The secret is delivered once. Redaction is defence in depth |
| V. TDD | ✅ | tasks.md writes the failing test before each implementation task |
| VI. Documentation hygiene | ✅ | New docs page, updates to `profiles.md`/`agent-tokens.md`/`rest-api.md`, a CLAUDE.md Recent Changes line, docs-site sidebar/allowlist |
| Core + tray split | ✅ | The tray is unchanged and reads the same REST fields (A8) |
| Event-driven updates | ✅ | `credentials.changed` SSE event |
| DDD layering | ✅ | Domain logic lives in `internal/runtime`. `internal/server` (MCP) and `internal/httpapi` (REST) are adapters |

No violations, so Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/115-mcp-credential-lifecycle/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md          # E2E test plan + live-harness recipe
├── contracts/
│   ├── mcp-credentials-tool.md
│   ├── errors.md
│   └── rest-sse-activity.md
├── checklists/requirements.md
└── tasks.md
```

### Source code (touched)

```text
internal/auth/agent_token.go                 # RevokedAt, Issuer, Purpose; CredentialIssuer; MaxCredentialPurpose
internal/storage/agent_tokens.go             # stamp RevokedAt; RevokeAgentTokenReport(before, after)
internal/storage/client_credentials.go       # stamp RevokedAt on forget
internal/profile/contract.go                 # ChangeIssue, ChangeRevoke; credential error codes
internal/profile/testdata/contract/enums.json
internal/runtime/credentials_service.go      # NEW: CredentialsService, CredentialView, requests, errors
internal/runtime/credentials_service_test.go # NEW
internal/runtime/clients_service.go          # Add → issue path; forget publishes credentials.changed; change kind param
internal/runtime/clients_service_connect.go  # issueLocked option RefuseExistingRecord
internal/runtime/binding_guard.go            # GuardState.PinnedTokens; conservative guard handles them
internal/runtime/events.go                   # EventTypeCredentialsChanged
internal/runtime/runtime.go                  # wire CredentialsService (shares bindingWriteMu, store, hmac, activity, publish)
internal/server/profile_binding_guard.go     # BindingGuardDelta evaluates PinnedTokens with bindingBypassable
internal/server/mcp_admin_access.go          # NEW: adminToolAccess (extracted from profilesToolAccess)
internal/server/mcp_credentials_tool.go      # NEW: build/filter/handle `credentials`, audit body
internal/server/mcp_credentials_tool_schema.go # NEW
internal/server/mcp.go, mcp_routing.go       # register next to profiles; filterProfileV3Tools filters credentials
internal/server/mcp_server_view.go / internal/oauth (AuditRedaction) # mcp_agt_/mcp_cli_ value backstop
internal/server/testdata/toolslist_goldens/*.json, toolslist_snapshot_test.go (allowed delta)
internal/server/e2e_test.go                  # MockUpstreamServer dispatch counter
internal/server/credentials_lifecycle_e2e_test.go # NEW TestE2E_CredentialsLifecycle_*
internal/httpapi/tokens.go                   # create/revoke → CredentialsService; new view fields
internal/httpapi/client_bindings.go          # custom add → CredentialsService; snippet/links helpers exported to MCP
internal/httpapi/admin_views.go              # CredentialSnippet, UILinks for the MCP adapter
oas/swagger.yaml, docs/api/rest-api.md
frontend/src/stores/system.ts                # relay credentials.changed
frontend/src/stores/clients.ts, stores/profiles.ts, views/AgentTokens.vue, views/Clients.vue (rows),
frontend/src/views/ProfileEditor.vue (Assigned to links), components/profiles/ProfileCard.vue,
frontend/src/utils/profiles.ts (tierPhrase with exceptions), utils/credentials.ts (NEW: lease/issuer copy),
frontend/src/views/Activity.vue (issue/revoke labels), types/api.ts, types/contracts.ts
frontend/tests/unit/*.spec.ts                # NEW specs
docs/features/mcp-credential-lifecycle.md    # NEW; docs-site allowlist + sidebars.js
docs/features/profiles.md, docs/features/agent-tokens.md
```

## Phasing (one PR, or two stacked-free PRs if review size demands)

1. **Foundation (backend, no surface change)**: record fields, change kinds, error codes, event type, guard `PinnedTokens`, `CredentialsService`, with REST tokens and custom client add moved onto it. REST and CLI behaviour stays the same apart from the audit record and the event.
2. **MCP tool**: `adminToolAccess` extraction, the `credentials` tool, redaction backstop, deliberate golden update.
3. **E2E**: dispatch counter and the six `TestE2E_CredentialsLifecycle_*` scenarios.
4. **Web UI**: event relay, rows, links, lease and tier copy, Activity labels, vitest, Playwright sweep and screenshots.
5. **Docs and verification**: docs pages, CLAUDE.md line, live-harness evidence (not committed), OpenCode GPT-6.1 Sol cross-review rounds.

If one PR exceeds what a review round can cover, split after phase 3: backend plus MCP plus E2E first, then UI plus docs. Each part merges on its own from main (no stacking).

## Risk Register

| Risk | Mitigation |
|---|---|
| Moving token logic changes REST behaviour | REST token tests (`tokens_test.go`, `tokens_profile_test.go`, `tokens_cap_test.go`, `tokens_revoked_name_test.go`) run unchanged before and after the move. The only intended diffs are the new fields, the record and the event |
| `assign` → `issue` on custom client add breaks consumers | Grep tests and UI for `change === 'assign'` on add. Update `clients_service_test.go` expectations and Activity labels. Call it out in release notes |
| Guard enforcement on MCP tokens surprises users with default config | The structured `binding_bypassable_without_auth` body carries fixes (set `require_mcp_auth`, or set `anonymous_profile`). The docs page leads with this. E2E daemons set `require_mcp_auth: true` |
| Secret leaks through an unexpected sink (mcp-go logging, sensitive-data detector, SSE) | FR-023 sink-scan test, redaction backstop, and a zap observer at debug level in E2E-5 |
| Golden churn hides an unintended change | Enumerated delta: only `credentials` may differ, and the `profiles` entry stays byte-equal |
| Race between issue and profile delete | Both take `bindingWriteMu`. A test races them with `-count=50` |
