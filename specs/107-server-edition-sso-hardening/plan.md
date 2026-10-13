# Implementation Plan: Server Edition — SSO Front Door Hardened for Real IdPs

**Branch**: `107-server-edition-sso-hardening` | **Date**: 2026-09-15 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `specs/107-server-edition-sso-hardening/spec.md` (Draft, 47 FRs, 11 SCs, 7 user stories). Evidence base: the seven lane maps and the completeness critic (`critic.md` C1–C14, G1–G11 override the lane maps where they conflict) and the maintainer's research decision of 2026-09-14 (posture (a) + a bounded slice of (c)); all anchors below were re-read at origin/main `b39800a89`.

## Summary

Make the sentence *"a person who signs in through the team's IdP can see and use exactly the servers their group grants, on every surface, and every decision about them is written down"* true, and remove the code and prose that promise things the proxy does not do. Four deliverables — **F4** freeze/cut of the latent multi-user/credential-injection chain, **F1** a discovery-based OIDC provider with JWKS-verified ID tokens and a groups claim, **F2** an IdP-group → server allowlist composed as one more narrow-only term of Spec 105's *effective authorization* (plus the tenant principal on core REST that critic G1 showed is missing), **F3** a synchronous, attributable JSONL audit line with a checked-in JSON Schema — shipped as **four independently mergeable PRs in that order** (PR-A → PR-D, §Delivery). Every PR is green under the same gate set (§Gates) and leaves the frozen tool-surface goldens untouched — the three byte-exact ones (`TestToolsListSnapshot_MatchesMergeBaseGoldens`, `TestToolsListSnapshot_DeltaIsEnumerated`, `TestMenuSurface_ExactDeltaFromPreFeature`) plus the direct-mode pair (FR-044). No new module dependency (FR-020, FR-015: JWK parsing and RFC 8785 canonicalisation are stdlib + the existing `golang-jwt/jwt/v5`; both are measured against the ~150-line rule in `research.md` D3/D5).

## Technical Context

**Language/Version**: Go 1.26 (`go.mod` toolchain; CI `unit-tests.yml` `go-version: "1.26"`); TypeScript 5.9 / Vue 3.5 for the Web UI; Python 3 only for `scripts/gen-roadmap.py`.
**Primary Dependencies**: existing only — `github.com/golang-jwt/jwt/v5 v5.3.1` (HS256 user JWT today; RS/PS/ES verification of ID tokens with a stdlib JWK → `*rsa.PublicKey`/`*ecdsa.PublicKey` parser), `go-chi/chi` (route table walk for FR-043(f)), `lumberjack` via `internal/logs.newRotatingSink` (`logger.go:183`), BBolt (user store), Bleve (unchanged), `mark3labs/mcp-go v1.0.0` (unchanged). **No new dependency** — `go-oidc`/`keyfunc`/`jcs` are rejected in `research.md` D3/D5 with line counts.
**Storage**: BBolt `users` bucket gains three fields on `users.User` (additive JSON, no migration — absent fields decode to zero values, `data-model.md` §1); the audit sink is a rotated file and/or stdout, never a bucket (FR-016). No schema version bump in BBolt.
**Testing**: `go test -race` (personal packages with the CI `-skip` regex for `internal/server`), `go test -race -tags server` (server packages + `./internal/server/... ./internal/httpapi/...` per FR-047), `./scripts/test-api-e2e.sh` on an isolated port/data-dir, `go test ./cmd/release-gate/`, golangci-lint v2 (`.github/.golangci.yml`, run twice: bare and `--build-tags server`), vitest (`frontend/tests/unit/*.spec.ts` only), an ad-hoc Playwright spec for US4, and the real-instance rig of `quickstart.md`.
**Target Platform**: Linux container (`ghcr.io/smart-mcp-proxy/mcpproxy-server`, distroless, `0.0.0.0:8080`) behind a TLS-terminating ingress; macOS/Linux personal edition unchanged except SC-007.
**Project Type**: single Go module with an embedded Vue frontend; server-only code under `//go:build server` in `internal/serveredition/` and `internal/config/server_edition_config.go`.
**Performance Goals**: SC-009 — with `audit_log` on, administrator p95 for `call_tool_read`, `retrieve_tools`, `tools/list` regresses ≤ max(10 %, 5 ms) against merge-base on the frozen 527-tool snapshot (Spec 105 FR-011 method); scoped tenant `retrieve_tools` p95 within 20 ms of administrator. The audit line is one `encoding/json` marshal + one buffered write per decision; the entitlement resolution is one BBolt read per authentication (down from two, FR-004).
**Constraints**: no built-in MCP tool gains an argument (FR-044); no `_auth_*`/argument-derived value on an audit line (FR-015); fail-closed on entitlement lookup failure (Spec 106 FR-004); non-disclosing refusal everywhere (Spec 105 FR-010); single replica (in-memory `pendingStates`, BBolt single writer); server-edition code is invisible to `swag`/`verify-oas-coverage.sh` (`swaggerignore` at `config.go:584`) so its REST contracts are hand-maintained under `contracts/`.
**Scale/Scope**: a small team (≤ 1,000 users per the telemetry bucket vocabulary), ≤ 100 tokens per owner, ≤ 10,000 pending logins in memory, one IdP.

## Constitution Check

*GATE: passed before Phase 0 research; re-checked after Phase 1 design (this file).*

| Principle | Compliance | Notes |
|---|---|---|
| I. Performance at Scale | ✅ | SC-009 budget; the entitlement resolution collapses two store reads into one (FR-004); discovery/JWKS are cached (5 min–24 h); no per-call store lookup for audit identity (FR-013 places owner identity on the validated token's `AuthContext` ephemerally). |
| II. Actor-Based Concurrency | ✅ with one justified exception | The audit sink is a single writer goroutine-free `bufio`+lumberjack writer guarded by one mutex — the spec forbids the event bus (it drops on a full 256-slot buffer, `event_bus.go:205-216`) and requires the line before the funnel returns (FR-012). Justified in Complexity Tracking. Everything else rides on existing context propagation. |
| III. Configuration-Driven Architecture | ✅ | Every knob is a `mcp_config.json` key with a documented default; `audit_log`, `trusted_proxies` and `public_url` get `MCPPROXY_*` env overrides (FR-014, FR-025, FR-027); `access.*`, `admin_emails`, `trusted_proxies` hot-reload; the rest is restart-pinned and *reported* by `DetectConfigChanges` (FR-039) — closing the current silent "No configuration changes detected" (`config_hotreload.go:420-423`). |
| IV. Security by Default | ✅ | Forced MCP auth under the server edition (FR-029), `email_verified_policy: refuse_false`, `trusted_proxies` default empty, Secure cookie `auto`, audit on by default in the server edition, fail-closed empty entitlement (FR-006), no anonymous administrator in the container. Personal edition defaults unchanged (SC-007). |
| V. TDD | ✅ | `tasks.md`: every implementation task is preceded by a failing-test task (the two regression pins that are green by design — `mcp_session_never_reaches_test.go` in T068 — are labelled as pins, not tests-first); the two-fixture sentinel oracle (Spec 105 shape) is built in the `setup_wiring_test.go` harness; guard tests pin absence of removed symbols (FR-035) and the route-table walk (FR-043(f)). |
| VI. Documentation Hygiene | ✅ | FR-019/FR-036/FR-046: new `docs/features/audit-log.md`, `docs/operations/deploying-for-a-team.md`, `server_edition` reference in `docs/configuration/config-file.md`, tombstone + rewrites, `CLAUDE.md` editions row, `.github/RELEASE_NOTICE.md` (FR-042). Sidebar entries added manually (`project_docs_site_pipeline`). |
| Arch: Event-driven / DDD / 3-layer upstream | ✅ | Audit is Infrastructure (`internal/audit/`), consumed from Presentation funnels in `internal/server`; the 3-layer upstream client loses only the dead brokered branches (`core/connection_http.go:171-192`). |
| Workflow: pre-commit gates, conventional commits, `Related #N` | ✅ | §Gates; commit convention per spec "Commit Message Conventions" (`Related #1177`, `Related #1169`; no AI attribution lines — the repo convention overrides the session reminder). |
| Workflow: branch strategy (`next`) | ⚠️ documented deviation | PRs base on `main`, as Spec 106's plan recorded; `next` receives RCs by tag. |

## Project Structure

### Documentation (this feature)

```text
specs/107-server-edition-sso-hardening/
├── spec.md              # acceptance contract (final)
├── plan.md              # this file
├── research.md          # Phase 0 decisions D1–D14 with alternatives
├── data-model.md        # entity/field changes, ephemeral records, config shapes
├── quickstart.md        # local real-instance rig (fake OIDC IdP + server binary)
├── contracts/
│   ├── audit-line.schema.json   # FR-013 JSON Schema (draft 2020-12), checked into docs/ by PR-D
│   ├── audit-line-events.md     # per-event required/optional/forbidden table + vocabularies
│   ├── config-keys.md           # every new/removed key: default per edition, live/restart, env, validation text
│   ├── rest-endpoints.md        # OAS snippets for the new/changed server-edition routes + tenant-session allowlist
│   ├── entitlement-predicate.md # owner-resolution callback, restricted marker, session-principal hook
│   └── telemetry-v13.md         # payload fields, privacy rules
├── tasks.md             # PR-A..PR-D phases, US-tagged, failing-test-first
├── verification.md      # created empty by T004 (Phase 0); one section per PR, filled by the verification tasks
└── checklists/requirements.md
```

### Source Code (repository root)

```text
internal/
├── audit/                          # NEW (PR-D, edition-neutral): line builder, canonical JSON (RFC 8785), sink (file+stdout), attempt record on ctx
│   ├── attempt.go                  # AuditAttempt context value (FR-012)
│   ├── canonical.go                # RFC 8785 serialiser + args_sha256 (FR-015) — stdlib
│   ├── line.go                     # closed key set, per-event validation, redaction structural rule
│   ├── sink.go                     # lumberjack file + os.Stdout writer, always-on atomic write-failure + sanitizer-hit counters (mirrored to Prometheus when metrics are on; read by doctor), once-per-minute log
│   └── testdata/canonical/         # published JCS test vectors
├── auth/
│   ├── context.go                  # AuthContext ephemeral Email/Provider/Role on agent tokens (FR-013); CredentialKind + IsSessionPrincipal (owned here — httpapi imports auth, so the type cannot live in httpapi; defined by T076 in PR-C phase C.2 because T078 in the same phase records it); CanRevealSecrets denies session principals (FR-002)
│   └── agent_token.go              # non-persisted OwnerEmail/OwnerProvider/OwnerRole (`json:"-"`) stamped by storage and copied by AuthContext() (FR-013, data-model §3); MaxTokens per owner semantics doc (FR-037); exported ParseTokenExpiry shared by core /tokens and /user/tokens (FR-011)
├── config/
│   ├── config.go                   # + AuditLog, TrustedProxies (top-level, edition-neutral); Validate reaches ServerEdition.Validate (FR-039)
│   ├── audit_log.go                # NEW AuditLogConfig (pointer wire fields) + EffectiveAuditLog(cfg, transport) — under stdio the ABSENT-block default is suppressed (WARN), an explicit stdout-only block is StartupError{ExitCode:4}; StartupError also for an unopenable path; validateAuditLog reached from Config.Validate/ValidateDetailed
│   ├── trusted_proxies.go          # NEW CIDR parsing + IsTrustedProxy + forwarded-header helpers (FR-027); validateTrustedProxies reached from Config.Validate/ValidateDetailed
│   ├── doctor_findings{,_serveredition,_stub}.go   # NEW: config-derived doctor findings (edition-neutral file + server-tagged half); read the live config
│   ├── loader.go                   # MCPPROXY_AUDIT_LOG_*, MCPPROXY_TRUSTED_PROXIES; server-build normaliser for removed keys/modes (FR-032) — records LoadDiagnostics on the Config (the loader has no logger; main/reload log them once)
│   ├── load_diagnostics.go         # NEW (edition-neutral): LoadDiagnostic type, Config.LoadDiagnostics(), LogLoadDiagnostics(cfg, logger)
│   ├── env_serveredition{,_stub}.go   # NEW: MCPPROXY_PUBLIC_URL (the one nested env alias, FR-025)
│   ├── removed_keys{,_stub}.go     # NEW: ValidateRemovedKeys on the raw document for PATCH / apply (FR-032 write-time refusal)
│   ├── server_edition_config.go    # − MaxUserServers/WorkspaceIdleTimeout; + OAuth{IssuerURL,Scopes,GroupsClaim,EmailVerifiedPolicy,DisplayName,AllowInsecureIssuer}, PublicURL, SessionCookieSecure, Access{GroupServers,DefaultServers}; non-mutating Validate + ApplyDefaults
│   ├── server_edition_config_stub.go / auth_broker_stub.go   # json.RawMessage carriers (FR-040)
│   ├── auth_broker.go              # − Header/HeaderFormat, − token_exchange/entra_obo (FR-032)
│   └── serveredition_accessors{,_stub}.go  # NEW: build-tagged accessors (ForceMCPAuth, IdPProviderFamily, PublicURL…) so internal/server, internal/httpapi, internal/telemetry stay edition-neutral (FR-029, FR-038)
├── httpapi/
│   ├── server.go                   # apiKeyAuthMiddleware credential precedence (presence = header/query membership) + session hook + tenant allowlist (FR-001/002); PATCH UseNumber + raw removed-keys check (FR-040/FR-032); /index/search calls SearchToolsScoped for scoped callers (filter before the ranked cut, T075a)
│   ├── session_principal.go        # NEW: SetSessionPrincipalResolver, tenantSessionAllowlist matcher (explicit deny for /servers/import/paths; no /profiles/{slug} — none exists), fixed 403 body
│   ├── middleware.go               # tagRequestMeta: client IP via trusted_proxies + mount (mcp|api) on the context for the audit line (PR-B)
│   ├── profiles.go                 # tenant projection (FR-002)
│   ├── preflight.go                # restricted marker (FR-006)
│   ├── sse_scope.go                # re-resolve session principal per frame (FR-005)
│   ├── swagger.go                  # forwarded headers gated by trusted_proxies (FR-027)
│   └── tokens.go                   # 409 body per-owner wording (FR-037)
├── index/{bleve.go,manager.go}     # SearchToolsScoped: same query, exhaustive From/Size paging (no result cap), filter before the top-K cut (Spec 105 ranking-under-scope on the REST door)
├── preflight/scope.go              # ScopeInputs.Restricted (FR-006)
├── storage/agent_tokens.go         # per-owner cap; intersectAllowedServers non-nil empty; single owner-resolution callback (FR-004/006/037)
├── storage/activity_models.go      # ActivityFilter.UserID authorization term (both /user/activity terms inside Matches, FR-002)
├── reqcontext/                     # RequestMeta (client IP, mount) beside the request id
├── server/
│   ├── server.go                   # mcpAuthMiddleware forced auth via accessor (FR-029); ctx-first funnels; `type ServerOption func(*Server)` + variadic `...ServerOption` on NewServer/NewServerWithConfigPath (`WithAuditSink`); stdioAuthContext tags ConnectionSourceStdio
│   ├── mcp.go, mcp_routing.go, mcp_code_execution.go, output_sanitisation.go   # audit attempt before first gate; authz/tool_call lines (FR-012)
│   ├── serveredition_wire{,_stub}.go  # install session-principal resolver + audit sink for login events
│   └── server.go (boot)            # registers the doctor sources: config.DoctorFindings + audit sink counter (FR-018/025/026/029)
├── management/{service.go,diagnostics.go}   # AddRuntimeWarningSource seam — Doctor() is the ONLY producer of `mcpproxy doctor` findings (doctor_cmd.go only renders GET /api/v1/diagnostics)
├── jsruntime/runtime.go            # ExecutionContext authorization-decision observer (FR-012 nested refusals)
├── runtime/config_hotreload.go     # server_edition / audit_log / trusted_proxies clauses (FR-039)
├── serveredition/
│   ├── registry.go                 # Dependencies + ProjectActivity func(*storage.ActivityRecord) contracts.ActivityRecord (PR-C: convert+mask, the door holds storage records) + AuditSink audit.Sink (PR-D)
│   ├── setup.go                    # cookie policy, public URL (+ one INFO boot line with the resolved public/callback URLs), session-principal resolver, single owner resolution, audit login sink
│   ├── auth/
│   │   ├── oauth_providers.go      # registry accepts "oidc"; factory takes *ServerEditionOAuthConfig; the handler constructs the provider ONCE (h.provider) so discovery/JWKS caches outlive a request; legacy providers untouched
│   │   ├── oidc_provider.go        # NEW: discovery cache, JWKS cache, verified ID token, nonce, client-auth method choice, userinfo sub check
│   │   ├── oidc_jwks.go            # NEW: stdlib JWK → public key (RSA/EC)
│   │   ├── oauth_handler.go        # nonce, bounded pendingStates, relative redirect, generic refusal pages (incl. IdP error= responses → authorization_denied), live admin_emails role via ServerEditionConfigProvider, subject binding, groups upsert, auth_event emit
│   │   ├── session_store.go        # Secure policy; forwarded headers via trusted_proxies
│   │   ├── idp_subject_token.go    # DELETED (FR-033)
│   │   └── middleware.go           # session resolver reused by the core hook (FR-001)
│   ├── api/
│   │   ├── user_handlers.go        # entitledServerNamesFor(user) core + entitledServerNames(userID) wrapper (one GetUser); group term; 8 Shared sites collapsed; session-only mint; parseExpiry
│   │   ├── auth_endpoints.go       # /auth/me groups; /auth/token session-only; /auth/provider (public)
│   │   ├── admin_handlers.go       # /admin/users groups + subject_rebind_armed_at; enable arms rebind
│   │   ├── user_activity.go        # wired ActivityFilter for user-typed principals only; admin_user keeps today's empty response (FR-002 "unchanged")
│   │   ├── credential_handlers.go  # entitlement predicate; "stored, not injected" (FR-034)
│   │   └── connector_provider.go   # public_url base; − ConnectorFor (FR-031)
│   ├── users/{models,store}.go     # Groups, GroupsUpdatedAt, SubjectRebindArmedAt; Validate admits "oidc"
│   ├── multiuser/{router,tool_filter}.go   # DELETED; activity.go stays
│   ├── workspace/                  # DELETED
│   └── broker/{token_exchanger,credential_resolver,injector}.go   # DELETED; audit.go trimmed
├── transport/broker_auth.go        # DELETED; http.go brokered plumbing removed; context.go + ConnectionSourceStdio (PR-D, audit caller.kind: stdio)
├── upstream/core/{connection_http,client}.go   # brokered branches removed
├── telemetry/{feature_flags,telemetry,env_markers}.go   # v13 fields (FR-038)
└── logs/{logger.go,sanitizer.go}   # exported NewRotatingWriter (primitive args, eager path probe) + NewStringSanitizer(WithoutHighEntropy()) — the generic 32+-char rule would mask every args_sha256/email_hash; audit → logs only, never the reverse (FR-014/FR-015)
cmd/mcpproxy/
├── credential_cmd.go               # "stored, not injected" banner (FR-034)
├── doctor_cmd.go                   # UNCHANGED — renders runtime_warnings; findings come from internal/management
└── status_serveredition.go         # prints new provider unchanged
frontend/src/
├── views/settings/fields.ts        # − max_user_servers; + oidc fields, public_url, session_cookie_secure, trusted_proxies, audit_log.*
├── views/teams/{Login,AdminUsers,AdminServers,UserActivity}.vue, stores/auth.ts, router/index.ts, App.vue, Dashboard.vue, Servers.vue, ServerCard.vue, stores/onboarding.ts, components/ModeSwitcher.vue   # FR-041 principal-kind gating; /auth/provider probe
└── services/auth-api.ts            # provider probe
frontend/tests/unit/*.spec.ts       # settings-server-edition-wording extended; principal gating specs
tests/oauthserver/                  # Options.OIDC, Options.UserClaims, id_token, /userinfo, CLI flags (FR-047)
scripts/
├── dev-server-edition.sh           # NEW: quickstart rig (build, fake IdP, scratch instance, headless login, mint, /mcp, audit tail)
└── test-api-e2e.sh                 # audit-file assertion (FR-019)
docs/                               # see FR-019/FR-036; website/sidebars.js entries
e2e/playwright/server-edition-tenant.spec.ts   # NEW ad-hoc US4 spec
.github/{RELEASE_NOTICE.md, workflows/unit-tests.yml, .golangci.yml}   # FR-042, FR-047
roadmap.yaml, ROADMAP.md, specs/README.md      # `sso` epic un-parked → spec 107; regenerated
```

**Structure Decision**: single Go module, existing layout. The only new package is `internal/audit/` (edition-neutral, Infrastructure layer). Build-tagged *accessors* (`internal/config/serveredition_accessors{,_stub}.go`) are the one new pattern, copied from `internal/oauth/serverfields_*.go`, so that `internal/server`, `internal/httpapi` and `internal/telemetry` never import a server-only type. The `//go:build server` boundary is unchanged.

## Delivery: four PRs, in order

Each PR is independently mergeable: it compiles in both editions, passes every gate in §Gates, changes personal-edition behaviour only where SC-007 allows, and carries its own `.github/RELEASE_NOTICE.md` entry, docs and roadmap task ticks. Order is binding — each PR's tests rely on the previous PR's seams. A PR that is not merged when the next one starts is rebased, never stacked into one.

| PR | Branch | Scope (FRs) | Stories | Why this order |
|---|---|---|---|---|
| **PR-A** — freeze/cut + honesty + hygiene | `107-a-freeze-cut` | FR-031..FR-036 (cut, dead knobs, `store_idp_tokens` no-op, Path B "stored, not injected", both-edition verification, docs corrections), FR-040 (personal-build raw-JSON carriers — required so PR-A's removed keys are not *erased* by a personal-build write-back), FR-039 part 1 (non-mutating `ServerEditionConfig.Validate` + separate apply step, reached from `Config.Validate`/`ValidateDetailed` — required for the write-time refusal of removed keys/modes), FR-037 (per-owner token cap, #1177 — Phase 0 hygiene, orthogonal to the cut per the freeze lane §6), FR-047 part 1 (lint `--build-tags server`; race job widened with the `-skip` regex; `RELEASE_NOTICE.md` created), FR-042 entry | US5, US6 | Smallest blast radius; deletes the contradictory `Router.GetServerForUser` rule set before F2 is built on the live seam; puts the server build under lint before B–D add code; the personal E2E is the required check for the transport/upstream cut. |
| **PR-B** — generic OIDC + front door + telemetry | `107-b-oidc-front-door` | FR-020..FR-024 (provider, verification, claims, subject binding, generic refusal), FR-025..FR-030 (public URL incl. `MCPPROXY_PUBLIC_URL`, Secure cookie, `trusted_proxies` + the edition-neutral request-metadata tag that PR-D's `client.ip`/mount provenance reads, relative redirect, forced MCP auth, `/auth/provider`), FR-008 **capture half** (groups stored on the user record at login, `/auth/me` and `/admin/users` show them — no authorization effect yet), FR-038 (telemetry v13), FR-039 part 2 (`server_edition` restart-pinned clauses + `admin_emails` hot + `trusted_proxies` hot), FR-047 part 2 (`tests/oauthserver` OIDC extension + CLI flags; `scripts/dev-server-edition.sh`), FR-036 config-file section for these keys, FR-042 entry | US2, US7 | F1 is the input F2 needs (groups only exist through `oidc`); the ingress prerequisites and forced MCP auth must exist before a tenant principal is introduced (no anonymous administrator to bind to, critic G1/agent5). Telemetry rides here because `idp_provider` needs the provider family enum this PR defines. |
| **PR-C** — group grants + tenant principal | `107-c-group-grants-tenant-principal` | FR-001..FR-006 (credential precedence, session hook, tenant allowlist, one predicate, single owner resolution, SSE re-resolve, empty = deny-all), FR-007 (`access` block), FR-008 **authorization half**, FR-009, FR-010, FR-011 (session-cookie-only mint doors, 365-day cap), FR-039 part 3 (`access.*` hot), FR-041 (Web UI), FR-043 (a,c,d,f,g,h,i,k), FR-045 matrix, FR-046 docs invariant, FR-047 part 3 (harness: group map, hot reload, empty entitlement, per-owner cap, session principal), FR-036 docs for the group map/tenant principal, FR-042 entry | US1, US4 | The authorization core; depends on stored groups (B) and on forced MCP auth (B). Also the largest PR — kept reviewable by landing A and B first. |
| **PR-D** — JSONL audit line | `107-d-audit-line` | FR-012..FR-019 (funnels, attempt record, schema, sinks, redaction, no read surface, `auth_event`, sink failure policy, docs + e2e assertion), FR-039 part 4 (`audit_log` restart-pinned), FR-043 (b,j), SC-009 benchmark, `docs/operations/deploying-for-a-team.md` + validated example (the capstone guide needs every key), FR-042 entry | US3 | Needs `caller.user_email`/`role` from C's single owner resolution, `session_admin` from C's hook, `trusted_proxies` from B for `client.ip`, `auth_event` reasons from B's callback branches. Last so its byte-absence and count fixtures run against the final authorization paths. |

**FR-039 is split by key, on purpose**: the non-mutating validator (A) is a prerequisite for refusing removed keys at write time; each later PR adds the `DetectConfigChanges` clause for the keys it introduces, with `config_hotreload_test.go` coverage in the same PR (`Eventually` on the snapshot — `project_config_reload_commit_ordering`).
**FR-008 is split by half**: capture (B) is a data-model change with a fixture; composition (C) is the authorization change. B's `groups_claim_missing` is a warning log; D turns it into the `auth_event` flag.
**FR-036/FR-042 are per-PR**: each PR corrects the docs sentences it makes true and adds its own release-notice bullet; the deploy guide lands with D.

## Execution (per PR)

1. **Branch** from `origin/main` (`git switch -c <branch> origin/main`; the speckit script branches from current HEAD — `project_speckit_branch_base_gotcha`); work in a worktree (`isolation: worktree` for any subagent).
2. **Failing tests first** for every task (tasks.md pairs each implementation task with the test task that precedes it). Two kinds of red, and every test-first task carries a label — **[behaviour-red]**, **[compile-red until Txxx]** (naming every implementation task whose symbols it needs, so it compiles the moment the last of them lands) or **[tooling]**: (a) a test against an *existing* seam must fail **behaviourally** on HEAD — prove it by running it before the fix, never by neutering code in a way that orphans an import (`feedback_verify_test_bites_build_failure`); (b) a test that references a type, field or package the paired implementation task introduces (`Options.OIDC`, `auth.CredentialKind`, `internal/audit`, …) is red as a **compile failure** on HEAD — that is the expected red, recorded as such in verification.md, and the test must then pass unchanged once the implementation lands (no scaffold tasks; the pairing is the proof).
3. **Implement**, then run the gate set (§Gates) locally; the isolated-instance rules for `test-api-e2e.sh` apply (`reference_isolated_dev_instance`).
4. **Real-instance verification** per user story on the `quickstart.md` rig; record commands and outcomes in `verification.md` (one section per PR; never claim a skipped gate passed).
5. **Cross-model review**: `gtimeout 1500 codex exec -m gpt-5.6-sol -c model_reasoning_effort="high" --sandbox read-only -C <repo> --output-last-message <verdict.md> "<brief>" < /dev/null` (maintainer directive 2026-09-15: the Copilot quota is exhausted, so the reviewer is the codex CLI with Sol 5.6, not opencode/astra; codex is static-only — run the tests yourself), briefs split per file group; an empty result is not a clean one (`feedback_codex_reviews_specs_too`); at most **10** fix→re-review rounds per PR (`feedback_opencode_review_round_cap`), verify each finding before fixing.
6. **Publish** the PR with `Related #1177` / `Related #1169` (never `Closes`), no AI attribution lines (spec convention), body via `--body-file` (`reference_gh_pr_body_quoted_heredoc_backticks`); run `python3 scripts/gen-roadmap.py --check` before pushing (tasks.md ticks change the render); post qa-gate status if the PR is green-but-blocked (`project_qa_gate`). Do not merge without instruction.

## Gates (every PR, in this order)

```bash
# builds — never bare `-tags server` without -o (it clobbers ./mcpproxy)
go build -o /dev/null ./cmd/mcpproxy
go build -tags server -o mcpproxy-server ./cmd/mcpproxy
# server-edition race suites (CI job + FR-047 widening)
go test -race -tags server -timeout 20m ./internal/serveredition/... ./internal/config/... ./internal/oauth/... ./internal/storage/...
go test -race -tags server -timeout 20m -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/... ./internal/httpapi/...
# personal race suite with the CI skip regex (internal/server hangs to the 7m timeout otherwise)
go test -race -timeout 20m -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/...
go test -race -timeout 10m ./cmd/mcpproxy && go test -race -tags server -timeout 10m ./cmd/mcpproxy
# frozen tool-surface goldens, unregenerated (FR-044)
go test ./internal/server/ -run 'TestToolsListSnapshot_MatchesMergeBaseGoldens|TestToolsListSnapshot_DeltaIsEnumerated|TestMenuSurface_ExactDeltaFromPreFeature|TestDirectFullMode_ByteStableAgainstPreFeatureE2E'
# release gate audits
go test ./cmd/release-gate/
# lint, both tag sets (CI v2 config is stricter than scripts/run-linter.sh)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --timeout=10m ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server --timeout=10m ./...
# personal API E2E — isolated (pgrep for a running copy; pkill-stripped scratch copy; high port; restore the tracked config)
pgrep -f test-api-e2e.sh && echo "another run is live — wait" || LISTEN_PORT=18${RANDOM:0:3} ./scripts/test-api-e2e.sh; git checkout -- test/e2e-config.json
# generators
make swagger-verify && go run ./cmd/generate-types && go test ./cmd/generate-types/ -run TestContractsInSync
python3 scripts/gen-roadmap.py --check
cd frontend && npx vitest run                          # only frontend/tests/unit/*.spec.ts execute
gofmt -l $(git diff --name-only origin/main -- '*.go')   # only files you touched
# native catalogue parity — required CI jobs (native-tests.yml:83,97) that no Go gate covers; run whenever fields.ts or SettingsCatalog.swift changes (PR-A, PR-B, PR-D)
python3 scripts/check-settings-parity.py
(cd native/macos/MCPProxy && swift test)                 # judge by the final summary line (project_swift_test_silent_miss)
```

PR-C and PR-D additionally run the ad-hoc Playwright spec (`e2e/playwright/server-edition-tenant.spec.ts`) against the quickstart rig, and PR-D runs the SC-009 benchmark against merge-base.

## Config-field wiring checklist (applied to every key in `contracts/config-keys.md`)

From the wiring lane §1 and `project_config_field_checklist`, each new or removed key touches, in the same PR:

1. **Struct + JSON tag + default** — `internal/config/server_edition_config.go` (server-tagged) or `config.go`/`audit_log.go`/`trusted_proxies.go` (edition-neutral); defaults documented; `DefaultServerEditionConfig` is *not* called by the loader (`loader.go:267` unmarshals directly) so defaults are applied in the new `ApplyDefaults` step at setup, never inside `Validate` (FR-039).
2. **Validation** — non-mutating `Validate` reached from `Config.Validate()`/`ValidateDetailed()` under the server build; message text fixed in `contracts/config-keys.md` so PATCH and boot say the same thing. **Every key's rule is reachable from both `Config.Validate()` and `ValidateDetailed()`** — `trusted_proxies` (`validateTrustedProxies`, T050) and `audit_log` (`validateAuditLog`, T109) from the edition-neutral `config.go`; a rule that needs a top-level field beside a `server_edition.*` key (`session_cookie_secure: false` × `tls.enabled`, T051) lives in the build-tagged `*Config`-level bridge `validateServerEditionConfig` (T019), never on `ServerEditionConfig.Validate`, which cannot see `Config.TLS`; the paired test drives boot load, `Config.Validate`, PATCH and `/config/apply`. **Removed keys are a raw-document check** (`config.ValidateRemovedKeys(map[string]any)`, server build; stub returns nil): `json.Unmarshal` into the typed `Config` silently drops unknown keys (`httpapi/server.go:5405-5408`), so `Validate` can never see `max_user_servers` or a removed `auth_broker.mode`; PATCH and `/config/apply` call the raw check on the generic map before typed decoding, boot normalises and warns instead.
3. **`DetectConfigChanges`** (`internal/runtime/config_hotreload.go:77-423`) — a clause per key group: `slices.Equal` for lists (`:380-386` nil-vs-`[]` comment), `jsonEqual` for structs with `omitempty` slices (`:357-359`), restart-pinned keys set `RequiresRestart` + `RestartReason`; test in `config_hotreload_test.go` (assert with `Eventually`). **A "live" key also needs live readers**: every request-time consumer of `trusted_proxies`, `admin_emails` and `access.*` reads through a provider closure (`Dependencies.ConfigProvider` / the httpapi config provider), never a slice or pointer captured at construction — with a test that mutates the value after construction (T047, T044).
4. **Env override** — `applyTLSEnvOverrides` (`loader.go:629-790`) for the top-level keys (`MCPPROXY_AUDIT_LOG_ENABLED|PATH|STDOUT`, `MCPPROXY_TRUSTED_PROXIES`); the one nested key with an env alias, `server_edition.public_url` ← `MCPPROXY_PUBLIC_URL` (FR-025), goes through a build-tagged `applyServerEditionEnvOverrides` (`internal/config/env_serveredition{,_stub}.go`, personal stub no-op) called from the same function; every other `server_edition.*` key stays file-only with `${env:}` refs (FR-020).
5. **Docs** — published `docs/configuration/config-file.md` (`server_edition` reference section — one complete table covering the retained keys too, T057) and `docs/configuration/environment-variables.md` (incl. `MCPPROXY_CRED_KEY`, whose only page T023 tombstones); the repo-root `docs/configuration.md` gets the same rows for the edition-neutral keys (it is unpublished — never *link* to it, `project_docs_site_pipeline`); `docs/operations/reverse-proxy.md` for `trusted_proxies`.
6. **`make swagger`** — regenerates `oas/swagger.yaml` + `oas/docs.go` for edition-neutral top-level keys (`audit_log`, `trusted_proxies`); `server_edition` stays `swaggerignore` (`config.go:584`) — its shape is documented in `contracts/config-keys.md` and `config-file.md` instead; `make swagger-verify` must be clean.
7. **`contracts.ts` generator** — `go run ./cmd/generate-types` + `TestContractsInSync` (`cmd/generate-types/main_test.go:30`); the generator emits vocabularies, not payload shapes, so it is expected to be a no-op — the task exists to *prove* it (drift is byte-compared).
8. **Settings catalogue** — `frontend/src/views/settings/fields.ts:323-327` (`SERVER_EDITION_FIELDS`) plus a new accordion for `audit_log.*`; `restart: true` on restart-pinned keys; `control: 'secret'` never for `client_secret` in the UI (masked read via `oauth.RedactedConfig`); the `Settings` column of `contracts/config-keys.md` is the authority for every key: rows for the new OIDC/front-door/`audit_log`/`trusted_proxies` keys; **Raw-JSON-only by decision** for `access` (no map control), `oauth.allow_insecure_issuer` (loopback-only development toggle) and the retained keys that have no row today (`admin_emails`, `session_ttl`, `bearer_token_ttl`, `oauth.client_id`, `oauth.tenant_id`, `oauth.allowed_domains` — SC-007 minimal UI change); **never a row** for `credential_encryption_key` (secret, env-only) and the deprecated `store_idp_tokens`; `frontend/tests/unit/settings-server-edition-wording.spec.ts` extended; `scripts/check-settings-parity.py` exempts `server_edition.` (`:48-50`) but **not** `audit_log.`/`trusted_proxies` → the Swift `SettingsCatalog.swift` parity check runs in `native-tests.yml` — add the rows there or extend `WEB_ONLY_PREFIXES` (decision: add to the Swift catalogue as read-only rows, research D12).
9. **Redaction walk** — `oauth.RedactedConfig` masks by key-name heuristic (`redactview.go:110-126`); `client_secret`/`credential_encryption_key` already masked; new plain fields pass through the value detector; a leaf added to `ServerConfig` would trip the `serverfields_serveredition` canary — no key of this spec lives on `ServerConfig` (FR-007).
10. **Storage canary** — `TestSaveServerSyncFieldCoverage` (`internal/storage/async_ops_test.go:353,415`) lists top-level `ServerConfig` fields and explicitly skips `AuthBroker`; it cannot see nested broker leaves, so **no key of this spec touches it** (PR-A's `Header`/`HeaderFormat` removals are covered by the `internal/oauth/serverfields_serveredition.go` mask-table test instead). Existing tests that name a removed field or constant are retired/rewritten in the same PR (T017 lists them).
11. **Roadmap** — `roadmap.yaml`: un-park epic `sso` (`:803-809`) → `spec: specs/107-server-edition-sso-hardening`, `status: in_progress`, four `tasks` rows (PR-A..D) with `pr:` filled as they open; `python3 scripts/gen-roadmap.py` (commit `ROADMAP.md`); `specs/README.md` row for 107 (manual, the 105 row at `:84` is the pattern).
12. **Release notice** — `.github/RELEASE_NOTICE.md` bullet (FR-042).

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Mutex-guarded synchronous audit writer (Principle II) | FR-012: a line must exist before the funnel returns and must survive a saturated event bus and a mid-call crash (`authz` before the upstream call). | Event-bus subscriber (Option B in the audit lane §8) inherits the 256-slot non-blocking drop (`event_bus.go:205-216`) and has no `AuthContext`; a dedicated writer goroutine with a channel re-introduces the drop-or-block choice. One `sync.Mutex` around a `bufio.Writer` is the smallest correct thing; `RecordToolCallRejected` and preflight already write synchronously for the same reason (`event_bus.go:562-567`). |
| Second entry point into the server-edition session resolver (`SetSessionPrincipalResolver`) | FR-001: core `/api/v1` must accept a session with a *different* credential order than the server-edition group (`middleware.go:88-108` tries the cookie first). | Mounting the server-edition middleware on the core group would let a cookie rescue a failed API key (FR-001 forbids) and would put `AdminUserContext` on `/api/v1` with secret reveal (FR-002 forbids). One resolver function, two mount points, ordering owned by each caller. |
| Build-tagged accessors instead of `cfg.ServerEdition != nil && …` | FR-029/FR-038: `internal/server` and `internal/telemetry` must not import a server-only type; the personal stub is opaque JSON after FR-040. | Reading the raw JSON in the personal build to answer "is SSO enabled" is both wrong (the personal binary must not act on it) and a second parser. Pattern already exists (`internal/oauth/serverfields_*.go`). |
| `internal/audit` as a new package | Edition-neutral sink used by `internal/server` (dispatch) and `internal/serveredition/auth` (login events) — neither may import the other. | Placing it in `internal/logs` couples to zap cores (the stdout path must bypass the console encoder, `logger.go:201-206`); placing it in `internal/server` creates an import cycle for login events. |
| Audit stdout **default** suppressed under the native stdio transport (server-edition default is stdout) | Standard output *is* the MCP transport in stdio mode (`server.go:968-989`); a JSON audit line interleaved with JSON-RPC corrupts the client's stream. | Writing to stderr would interleave with the log stream instead; refusing to start would turn a *default* into a boot failure. Resolve: an **absent** block resolves to disabled + one WARN naming `audit_log.path`; an **explicit** stdout-only block is a `StartupError` (exit 4) — FR-014 "an explicit value always wins" forbids silently disabling what the operator asked for; an explicit path is honoured. |
