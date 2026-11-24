# Inventory — administrative write doors and what they record today

Snapshot of `main` at af68aab32 (2026-11-24). Line numbers drift; the door registry test of FR-022 is the living version of this table. Built by a read-only sweep of the code and spot-checked (`tokens.go:397-420` hard delete with no row, `server.go:7340-7350` tool approval with no context, `server.go:1105-1111` actor middleware on `/api/v1`).

Paths are relative to repo root. "Row" = the activity record that lands in the activity log.
Legend for ACTOR column: Y = row carries actor_kind/actor_name/surface (+request_id where stated); N = no actor keys; n/a = no row.
Channel: BUS = goes through Runtime.publishEvent -> ActivityService (droppable); SYNC = storage.SaveActivity called inline.

## 0. Key mechanics (read first)

### 0.1 Emitters (the only writers of admin-ish rows)
| activity type | emitter (file:line) | channel | actor keys |
|---|---|---|---|
| config_change | Runtime.emitConfigChange internal/runtime/event_bus.go:920 (via EmitActivityConfigChangeCtx :900, EmitActivityServerRemovedCtx :913; non-ctx variants :893, :907 stamp nothing) -> ActivityService.handleConfigChange internal/runtime/activity_service.go:1239 | BUS | Y via stampActor (event_bus.go:925) + copyActorMetadata (activity_service.go:1271); request_id on record |
| quarantine_change (server) | Runtime.EmitActivityQuarantineChangeCtx event_bus.go:732 (non-ctx :725 = no actor) -> handleQuarantineChange activity_service.go:909 | BUS | Y with ctx (stampActor event_bus.go:738; copy activity_service.go:923) |
| tool_quarantine_change | Runtime.emitToolQuarantineEventWith internal/runtime/tool_quarantine.go:3764 (publishEvent :3802) -> handleToolQuarantineChange activity_service.go:1506 | BUS | **N** (no stampActor, no copyActorMetadata, no request_id; approver string ("api"/"mcp") is only logged, never put in metadata). Metadata keys: action, tool_name, old/new_hash, old/new_description/schema (truncated) |
| skill_quarantine_change | Runtime.emitSkillQuarantineChangeCtx internal/runtime/skills_quarantine.go:114 -> handleSkillQuarantineChange activity_service.go:1549 | BUS | Y via stampActor (skills_quarantine.go:125); if ctx has no caller, actor_kind="system" (:126). approved_by also in metadata |
| profile_change | writeChangeRecord internal/runtime/clients_service.go:466 (saves via the injected `activity` = storageManager.SaveActivity, runtime.go:768/784/801) | **SYNC** | Y (explicit Actor arg: actor_kind, actor_name, surface, + change, profile, previous_profile, client_id, token_name, diff; request_id from ctx via reqcontext.GetRequestID :487) |
| profile_change (profile CRUD / config funnel) | Runtime.writeProfileChanges internal/runtime/profile_change_diff.go:222 -> writeChangeRecord with sm.SaveActivity | **SYNC** | Y |
| credential_broker (server edition only) | internal/serveredition/api/broker_audit.go:42 -> storage.SaveActivityAsync (:70; goroutine, storage/activity.go:609) | async goroutine, not bus | N (UserID, ServerName only) |
| internal_tool_call (every MCP built-in call incl. upstream_servers / quarantine_security / profiles / credentials) | MCPProxyServer.emitActivityInternalToolCall internal/server/mcp.go:1394 -> Runtime.EmitActivityInternalToolCallAttributed (event_bus.go:792) -> handleInternalToolCall activity_service.go:1012 | BUS | N for actor_kind keys. Carries Spec 108 attribution (token/client/profile) from p.activityAttribution(ctx, sessionID), NOT actor_kind/surface. Args redacted (mcp_activity_args.go:89) |

copyActorMetadata (activity_service.go:1301) is called from exactly three places: :923 (quarantine_change), :1271 (config_change), :1561 (skill_quarantine_change). Nothing else carries the actor keys via the bus.

### 0.2 Bus droppability (profile_change question)
- Runtime.publishEvent internal/runtime/event_bus.go:206-226: per-subscriber non-blocking `select { case ch <- evt: default: }`; buffer defaultEventBuffer=256 (event_bus.go:163). ActivityService subscribes with subscribeInternalEvents (event_bus.go:182; activity_service.go:326) and a single goroutine drains and writes (activity_service.go:306-370). A burst that fills the 256 buffer silently drops events (no counter, no log in publishEvent).
- **profile_change is NOT on the bus.** Both emitters write synchronously with storage.Manager.SaveActivity (writeChangeRecord, clients_service.go:466-506; error is only logged at :504). Not droppable by the bus; a failure is swallowed (logged) and does not fail the mutation.
- **Droppable (BUS) admin rows:** config_change, quarantine_change, tool_quarantine_change, skill_quarantine_change, internal_tool_call for MCP admin tools.
- Precedent for sync-because-droppable: RecordToolCallRejected (activity_service.go:630) and RecordPreflight (activity_preflight.go:142) bypass the bus for exactly this reason.
- Admission gate additionally emits from a detached goroutine (config_load_admission_gate.go:263-268), so those quarantine_change rows are doubly best-effort.

### 0.3 Actor stamping
REST
- Middleware chain in /api/v1 (internal/httpapi/server.go:1100-1115): apiRequestTimeout, TagRequestMeta, apiKeyAuthMiddleware, **actorContextMiddleware (server.go:1111)**, telemetry middlewares. RequestIDMiddleware (internal/httpapi/middleware.go:125) puts request id in ctx (reqcontext.WithRequestID :136).
- actorContextMiddleware internal/httpapi/surface.go:60-69: runtime.ContextWithActor(ctx, actorFromRequest(r)).
- actorFromRequest surface.go:40-47: runtime.ActorFromContext(ctx, surfaceFromRequest(r)); if transport.GetConnectionSource == ConnectionSourceTray -> Kind="socket".
- surfaceFromRequest surface.go:23-38: header X-MCPProxy-Surface in {web,macos,cli} wins; else X-MCPProxy-Client prefix cli/ -> cli, tray/ -> macos, webui/|web/ -> web; else api. Attribution only, spoofable by caller (surface.go:12-14).
- Handlers that call services with an explicit Actor use actorFromRequest(r) (profiles_crud.go:63/98/137/171, tokens.go:209/380/442, client_bindings.go:119/191/291/383/462/522/582, oauth_login.go:311, connect.go:292/474/581). Handlers that pass only r.Context() rely on runtime.RequestActor(ctx) (activity_actor.go:29-47) which returns the stamped actor (REST) else derives from AuthContext with surface "api" (kind "socket" for tray) else ok=false (background ctx -> no actor keys).
MCP
- Server ops: mcpActorContext internal/server/mcp_upstream_writer.go:39-41 = ContextWithActor(ctx, ActorFromContext(ctx, SurfaceMCP)). Used at mcp.go:4721 (add_from_registry), 5661 (enable/disable), 6241 (quarantine), 6550 (add), 6642 (remove).
- Profiles/credentials tools: profilesActor mcp_profiles_tool.go:216-222 (ActorFromContext(ctx, mcp); kind "socket" for tray). Used at mcp_profiles_tool.go:288-341/450/465 and mcp_credentials_tool.go:351.
- Tool approve/block/enable via quarantine_security pass NO ctx (mcp.go:5168-5260: ApproveTools(..., "mcp") etc.) -> no actor.
actor_kind values (string)
- from auth.CredentialKind internal/auth/context.go:25-31: `socket`, `api_key`, `bearer_jwt`, `agent_token`, `oauth`, `cookie`, `anonymous`. ActorFromContext (clients_service.go:101-134) infers api_key/agent_token/anonymous when CredentialKind unset; actor_name = AuthContext.AgentName else Email.
- hard-coded extras: `system` (reconcilerActor clients_service_reconcile.go:28; skill transitions skills_quarantine.go:126), `cli_offline` (cmd/mcpproxy/connect_credential.go:277, :306).
surface values (profile.Surface, internal/profile/contract.go:264-268): `web`, `macos`, `cli`, `mcp`, `api`. Record `source` is derived by surfaceSource (clients_service.go:448): cli->cli, mcp->mcp, everything else (web, macos, api) -> api.
Row keys: actor_kind, actor_name (omitted if empty for stampActor rows, but "" present for profile_change), surface, request_id (record.RequestID).

### 0.4 Redaction helpers (before/after values)
- redactActivityConfigValues internal/server/mcp_activity_args.go:344 (flat before/after map; each leaf through redactActivityValue(key, normalizeForRedaction(v))). Used: server.go:2588 (server_updated previous/new), mcp.go:6550 (MCP add new_values).
- serverAddedValues mcp_activity_args.go:366: REST add new_values {protocol, enabled, quarantined, url?, command?}; url/command redacted. Used server.go:2379.
- serverConfigDelta mcp_activity_args.go:~389 (field-by-field JSON diff feeding server_updated).
- redactedConfigDiff mcp_activity_args.go:236 (MCP update/patch `config_diff` placed in the internal_tool_call args, NOT in a config_change row).
- redactActivityValue :305 -> redactValueWith :312 -> auditRedaction (:288) = oauth.AuditRedaction (internal/oauth/redactview.go:90) with 512-char cap (activityArgValueLimit :68). normalizeForRedaction :281 -> oauth.NormalizeForRedaction (internal/oauth/serverwrite.go:227). liveRedaction (:302) is for live reads only.
- activityArgsFromRequest :89 / redactActivityJSONParam :130 (MCP internal_tool_call args); redactBuiltinResponseForActivity internal/server/mcp_server_view.go:139 (MCP response); screenedSummary mcp_credentials_tool.go:221 (credentials tool secret-shaped input).
- profile_change `diff`: NOT run through a redactor. Credential rows store token_prefix only (lifecycleDiff credentials_service.go:442); profile diffs are structural (diffProfileFields profile_change_diff.go).
- tool_quarantine_change: descriptions/schemas truncated via truncateEventText only (tool_quarantine.go:3781-3795), no secret redaction.
- Server-edition admin handlers echo configs through oauth.RedactedConfigView (admin_handlers.go:~596) but write no activity.

---

## 1. REST (internal/httpapi) -- mounted under /api/v1 (server.go:1100). Gate column = requireServerOp(op) unless noted.

### 1.1 Tokens / credentials / secrets
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /tokens | tokens.go:195 handleCreateToken (server.go:1359) | CredentialsService.IssueToken credentials_service.go:549 -> writeLifecycle :756 (:485) | profile_change change=issue (SYNC) | Y |
| DELETE /tokens/{name} (revoke) | tokens.go:363 (server.go:1363) | CredentialsService.Revoke :1009 -> writeLifecycle :1085 | profile_change change=revoke (SYNC) | Y |
| DELETE /tokens/{name}/permanent | tokens.go:397 (server.go:1364) | direct s.tokenStore.DeleteAgentToken tokens.go:411 | **NO RECORD** (hard delete of a credential row; history references token_name) | n/a |
| POST /tokens/{name}/regenerate | tokens.go:425 (server.go:1365) | CredentialsService.Regenerate :1099 -> writeLifecycle :1135 | profile_change change=rotate (SYNC) | Y |
| POST /clients | client_bindings.go:177 | credentials().IssueClient :823 -> mintClientLocked :977 -> writeLifecycle :990 | profile_change issue (SYNC) | Y |
| PUT /clients/{client}/binding | client_bindings.go:90 | ClientsService.SetBinding clients_service.go:554 -> writeChange :605 | profile_change assign/unassign/mode change (SYNC) | Y |
| POST /clients/bulk-assign | client_bindings.go:499 | ClientsService.BulkAssign :625 (per client via writeChange) | profile_change per client (SYNC) | Y |
| POST /clients/upgrade-admin-key-holders | client_bindings.go:570 | ClientsService.ApplyAdminKeyUpgrade clients_service_upgrade.go:306 -> connect minter -> recordMint clients_service.go:971 (via clients_service_connect.go:340) | profile_change assign per mint (SYNC) | Y (actorOf(intent); ctx is context.Background() so request_id absent, clients_service_connect.go:333) |
| POST /clients/{client}/rotate | client_bindings.go:275 | ClientsService.Rotate clients_service.go:836 (StageClientCredentialRotation); for a Connect-supported client the handler goes through Connect instead (client_bindings.go:307-344), which reconciles first and can commit before a later 409 | **NO RECORD at stage time**; the single `rotate` row is written on finalize/rollback | n/a |
| POST /clients/{client}/rotate/finalize | client_bindings.go:376 | FinalizeRotation :866 -> writeChange :897 | profile_change rotate outcome=finalized (SYNC) | Y |
| (rollback of rotation) | via rollbackLocked clients_service.go:907 -> writeChange :921 | | profile_change rotate outcome=rolled_back | Y |
| DELETE /clients/{client} | client_bindings.go:417 | ClientsService.Forget :730 -> writeChange :817 | profile_change forget (SYNC) | Y |
| DELETE /clients/{client}/api-key | oauth_login.go:300 | ClientsService.RevokeAPIKey clients_service_oauth.go:382 -> :408 | profile_change revoke (credential_kind=api_key) (SYNC) | Y |
| DELETE /clients/{client}/logins/{grant_id} | oauth_login.go:269 | OAuthLoginService.RevokeGrant oauth_login_tokens.go:224 -> :253 | profile_change revoke (SYNC) | Y (RequestActor) |
| POST /secrets (set keyring) | server.go:5183 handleSetSecret (route :1264) | resolver.Store + controller.NotifySecretsChanged runtime.go:1541 -> emitSecretsChanged event_bus.go:423 (SSE event only) | **NO RECORD** | n/a |
| DELETE /secrets/{name} | server.go:5274 (:1265) | resolver.Delete (server.go:5319) + NotifySecretsChanged | **NO RECORD** | n/a |
| POST /secrets/migrate | server.go:5094 (:1263) | analysis only, always `dry_run: true` (server.go:5113-5127) | not a writer (exempt) | n/a |
| POST /servers/{id}/config-to-secret | server.go:3219 (:1193) | resolver.Store :3303 then controller.PatchServer (server.go:3338-3347) -> emit server_updated | config_change action=server_updated (BUS) + secrets.changed SSE only; keyring write itself unrecorded | Y (ctx) |

### 1.2 Server approval, tool approval, quarantine
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /servers/{id}/quarantine | server.go:4040 (:1199) | s.quarantineServer surface.go:84 -> Server.QuarantineServerCtx server.go:2671 -> Runtime.QuarantineServerCtx lifecycle.go:2459 -> emit lifecycle.go:2553 | quarantine_change (BUS) | Y |
| POST /servers/{id}/unquarantine | server.go:4078 (:1200) | quarantineServer / quarantineServerAt (surface.go:93) -> Runtime.QuarantineServerAtCtx lifecycle.go:2474 -> emit :2553; also approveBaselineToolsForServerAt lifecycle ~2535 -> tool_approved rows | quarantine_change (BUS) + one tool_quarantine_change `tool_approved` per baseline-approved tool (tool_quarantine.go:2985; BUS, no actor) | Y for server row, **N** for tool rows |
| POST /servers/{id}/security/approve | security_scanner.go:389 (:1243) | scanner.Service.ApproveServer*/ApproveServerReviewedAt service.go:2324/2345/2365/2372 -> FinishServerApprovalCtx(ctx) service.go:2607 -> runtime tool_approval_review.go:404-411 emit | quarantine_change reason "Server unquarantined by administrator" (BUS) + tool_quarantine_change (tool_approved / tool_blocked for `block` list via RecordToolBlocksForSecurityApproval, tool_quarantine.go:3731/3734) | Y for server row; **N** for tool rows. approver "api" hard-coded (security_scanner.go:407) |
| POST /servers/{id}/security/reject | security_scanner.go:463 (:1244) | scanner.Service.RejectServer service.go:2641 (deletes scan reports/jobs/baseline/snapshot image) | **NO RECORD** | n/a |
| POST /servers/{id}/tools/approve | server.go:7318 (:1223) | controller.ApproveTools / ApproveAllTools(serverID,..., "api") -> runtime tool_quarantine.go:2888/3519 -> emit :2985 | tool_quarantine_change action=tool_approved (BUS) | **N** (no ctx passed at server.go:7346/7386) |
| POST /servers/{id}/tools/block | server.go:7454 (:1226) | controller.BlockTools/BlockAllTools(..., "api") tool_quarantine.go:3578/3682 -> emit :3654/3734 | tool_quarantine_change action=tool_blocked (BUS) | **N** |
| POST /servers/{id}/tools/{tool}/enabled | server.go:7521 (:1227) | controller.SetToolEnabled(..., "api") tool_quarantine.go:3229 -> setToolEnabledNoEmit emit :3109 | tool_quarantine_change action=tool_enabled/tool_disabled (BUS) | **N** |
| POST /servers/{id}/tools/enable_all, /disable_all | server.go:7608 (:1230/1231) | controller.SetAllToolsEnabled tool_quarantine.go:3285 (loop of setToolEnabledNoEmit, per-tool emit :3109) | one tool_quarantine_change per flipped tool (BUS) | **N** |
| (automatic) tool discovery/auto-approve/changed | tool_quarantine.go:593,1219,1264,1468,1967,2514,2645,2650; tool_quarantine_scan_approve.go:120 | emitToolQuarantineEvent(With) :3746/:3764 | tool_quarantine_change tool_discovered / tool_auto_approved / tool_description_changed (BUS) | N (no caller; system-originated) |
| POST /servers/{id}/scan, /scan/cancel; POST /security/scan-all, /cancel-all | security_scanner.go:256/333/689/766 | scanner.Service.StartScan :1433 / ScanAll :3491 | result row only: security_scan (settled) via handleSecurityScanSettled activity_service.go:1585 (BUS); the request itself is not recorded | N |
| POST /security/scanners/{id}/enable|disable, PUT /security/scanners/{id}/config, POST /security/scanners/install, DELETE /security/scanners/{id} | security_scanner.go:104/144/188 (routes :1393-1395,1410-1411) | scanner.Service.InstallScanner :961 / RemoveScanner :1080 / ConfigureScanner :1148 | **NO RECORD** (only EmitSecurityScannerChanged SSE event_bus.go:1049) | n/a |

### 1.3 Config & servers
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /servers | server.go:2553 handleAddServer (:1162); calls controller.AddServer server.go:2735 | Server.AddServer server.go:2350 -> createServerCommitted config_cow.go:80; emit server.go:2379 | config_change action=server_added, new_values via serverAddedValues (BUS) | Y |
| POST /servers/import, /import/json, /import/path | import.go:337 / 410 / 258 (:1163,1164,1167); runImport import.go:459 -> controller.AddServer import.go:615 per server | same as above | config_change server_added per imported server (BUS). Preview = no write | Y |
| PATCH /servers/{id} | server.go:3104 (:1191); controller.PatchServer (server.go:3147-3162) | PatchServer → commit; delta computed and emitted after Commit returns (server.go:2543-2588) only when changedFields non-empty | config_change server_updated with redacted previous/new values (BUS) | Y |
| DELETE /servers/{id} | server.go:2773 (:1192); controller.RemoveServer :2783 | Server.RemoveServer server.go:2606; emit :2621 | config_change server_removed + metadata.purged (BUS) | Y |
| POST /servers/{id}/enable, /disable | server.go:3381 / 3446 (:1194/1195); mgmtSvc.EnableServer management/service.go:720 (fallback s.enableServer surface.go:105) | Runtime.EnableServerCtx lifecycle.go:2418; emit :2437 | config_change server_enabled / server_disabled (BUS) | Y |
| POST /servers/enable_all, /disable_all | server.go:3570 / 3600 (:1171/1172); mgmt EnableAll service.go:885 / DisableAll :979 | Runtime.BulkEnableServers lifecycle.go:2567 (one Commit "bulk_enable_toggle") | **NO RECORD** (no per-server config_change) | n/a |
| POST /servers/restart_all, /restart, /reconnect, /discover-tools, /refresh | server.go:3540,3633,3509,3758,3807 | mgmt RestartAll/RestartServer; controller.ForceReconnectAllServers; DiscoverServerTools | **NO RECORD** (runtime-only actions) | n/a |
| POST /servers/{id}/login, /logout (upstream OAuth) | server.go:3876 / 3986 (:1197/1198) | mgmt TriggerOAuthLoginQuick service.go:1133 / TriggerOAuthLogout :1162 | **NO RECORD** (only servers.changed SSE) | n/a |
| POST /config/apply | server.go:6103 (:1327) | s.mutateConfig config_funnel.go:113 -> Runtime.MutateConfig config_funnel.go:65 (ChangeHint{}) -> applyConfigWith runtime.go:2273 | **NO config_change.** MutateConfig only writes profile_change rows for diffs in `profiles[]` / `anonymous_profile` (profile_change_diff.go:45-100). Servers list, api_key, listen, sensitive-data, telemetry, docker isolation, registries etc. edited through this door are **NO RECORD** | Y only for the profile_change rows |
| PATCH /config | server.go:6247 (:1328) | same funnel | same as above | same |
| PATCH /config/docker-isolation | server.go:6187 (:1329) | same funnel | **NO RECORD** (no profile diff) | n/a |
| POST /config/validate | server.go (route :1326) | read-only | n/a | n/a |
| POST /registries | server.go:6882 (:1335) | Server.AddRegistrySourceRef add_registry_source.go:193 -> Commit "registry_source_add" :135 | **NO RECORD** | n/a |
| PUT /registries/{id} | server.go:6962 (:1336) | EditRegistrySourceRef edit_registry_source.go:144 -> Commit :104 | **NO RECORD** | n/a |
| DELETE /registries/{id} | server.go:6923 (:1337) | RemoveRegistrySourceRef remove_registry_source.go:79 -> Commit :49 | **NO RECORD** | n/a |
| POST /registries/{id}/refresh | server.go:7001 (:1339) | RefreshRegistryCache server.go:4978 | **NO RECORD** (cache only — exempt, bookkeeping) | n/a |
| POST /registries/{id}/servers/{serverId}/add | server.go:6804 (:1340); AddServerFromRegistryRef add_from_registry.go:150 | AddServerFromRegistry :88 -> s.AddServer(ctx) :134 | config_change server_added (BUS) | Y |
| POST /onboarding/mark | onboarding.go:181 (:1385) | controller.UpdateOnboardingState runtime.go:4999 | **NO RECORD** | n/a |
| POST /telemetry/update-failure | update_failure.go:50 (:1283) | controller.RecordUpdateFailure (counter) | **NO RECORD** (telemetry counter, not a settings change) | n/a |
| POST /diagnostics/fix | diagnostics_fix.go:59 (:1274) | diagnostics.InvokeFixer; only RecordFixAttempt counter (:137-141); two registered fixers: log tail (read-only) and upstream OAuth re-auth (diagnostics_fixers.go:69-86,164-168) | **NO RECORD** | n/a |
| POST /feedback | feedback.go:31 (:1370) | outbound feedback submit | **NO RECORD** (not a rules change) | n/a |
| Telemetry / sensitive-data settings | only reachable via /config/apply, PATCH /config (above) or CLI `telemetry enable/disable` (section 3) | | **NO RECORD** | n/a |

### 1.4 Profiles & clients (profiles/connect)
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /profiles | profiles_crud.go:57 (:1135) | ProfilesService.Create profiles_service.go:474 -> Runtime.MutateConfig config_funnel.go:65 -> writeProfileChanges :164 | profile_change create (SYNC) | Y |
| PUT /profiles/{name} | profiles_crud.go:88 (:1140) | ProfilesService.Update :535 | profile_change update (+diff) (SYNC) | Y |
| DELETE /profiles/{name} | profiles_crud.go:161 (:1141) | ProfilesService.Delete :643 (reassign_to moves token pins) | profile_change delete (SYNC); token pin moves ride the same MutateConfig (no separate per-token row; UNCONFIRMED whether diff names moved tokens) | Y |
| POST /profiles/{name}/rename | profiles_crud.go:127 (:1142) | ProfilesService.Rename :567 | profile_change rename (SYNC) | Y |
| (profile classify / anonymous) | no REST route in this inventory (MCP profiles tool + CLI `profile classify/anonymous` -> REST profiles update). ProfilesService.Classify :~700, SetAnonymous :~760 | MutateConfig | profile_change classify / anonymous (SYNC) | Y |
| POST /profiles/try | profiles_crud.go:200 (:1137) | read-only | n/a | n/a |
| PUT /profiles/active | profiles.go:110 (:1151) | in-memory s.activeProfile (profiles.go:136-139) + SSE EmitActiveProfileChanged event_bus.go:437 | **NO RECORD** (server-level shared view state; not persisted) | n/a |
| POST /connect/{client} | connect.go:269 (:1379) | connect.Service.ConnectWithOptions (writes client's config file) -> minter Commit clients_service_connect.go:325 when a credential is issued -> recordMint clients_service.go:971 | profile_change assign when a credential is minted (SYNC). The client-config-file write itself and keyless connects: **NO RECORD** | Y (actorOf intent) |
| POST /connect/{client}/undo | connect.go:562 (:1380) | connect.Service.UndoWithIntent | profile_change forget/restore only if credential changes (clients_service_connect.go:392/426); file restore: NO RECORD | Y when row exists |
| DELETE /connect/{client} | connect.go:405 (:1381) | connect.Service.Disconnect; revoke via forget connect.go:474 -> ClientsService.Forget | profile_change forget (SYNC) when a credential was held; file edit itself NO RECORD | Y |

### 1.5 Skills
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /servers/{id}/skills/approve | skills_approval.go:126 (:1211) | controller.ApproveSkillsReviewedAt skills_approval.go(runtime):74 -> mutateSkillsAt :166; emit loop :221 | skill_quarantine_change (BUS; admin-only type storage/activity_models.go:108) | Y (ctx; approved_by="api" skills_approval.go(httpapi):29) |
| POST /servers/{id}/skills/block, /unblock | skills_approval.go:194 / 215 (:1212/1213) -> handleSetSkillsBlocked :220 | Runtime.SetSkillsBlockedAt skills_approval.go(runtime):121 -> mutateSkillsAt -> :221 | skill_quarantine_change (BUS) | Y |
| POST /servers/{id}/skills/refresh | skills.go:267 (:1209) | Runtime.RefreshServerSkills skills_catalog.go:291 ; transitions via emitSkillTransition skills_catalog.go:686 (context.WithoutCancel(ctx)) | skill_quarantine_change for status transitions (BUS). Comment says "refresh has no caller: actor_kind system" but WithoutCancel keeps ctx values, so an API-triggered refresh may still carry the REST actor (UNCONFIRMED) | system or Y |

### 1.6 TPA bundle / scanners
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /security/bundle/rollback (overrides downgrade watermark) | security_bundle.go:125 (:1404) | scanner.Service.RollbackBundle service.go:488 | **NO RECORD** | n/a |
| POST /security/bundle/unpin | security_bundle.go:180 (:1405) | UnpinBundle :493 | **NO RECORD** | n/a |
| POST /security/bundle/reload | security_bundle.go:203 (:1406) | ReloadBundle :499 | **NO RECORD** | n/a |
| POST /security/bundle/refresh | security_bundle.go:221 (:1407) | RefreshBundle :506 | **NO RECORD** | n/a |
(verified: no SaveActivity / EmitActivity in scanner bundle code path; only `go grep` of internal/security/scanner/bundle*.go found no activity symbols.)

### 1.7 OAuth login (Spec 132 browser login)
| Door | Handler | Funnel | Row today | Actor? |
|---|---|---|---|---|
| POST /oauth/authorizations/{id}/preview | oauth_login.go:167 (route :36) | read-only | n/a | n/a |
| POST /oauth/authorizations/{id}/approve | oauth_login.go:206 (:37) | OAuthLoginService.Approve oauth_login_consent.go:850 -> writeChangeRecord :901 | profile_change change=approve (SYNC) | Y (RequestActor oauth_login_consent.go:~870) |
| POST /oauth/authorizations/{id}/deny | oauth_login.go:236 (:38) | OAuthLoginService.Deny :914 -> :928 | profile_change change=deny (SYNC) | Y |
| DELETE /clients/{client}/logins/{grant_id} | see 1.1 | | profile_change revoke | Y |
| (token exchange/refresh grants, background) | oauth_login_tokens.go:306,316 (writeChangeRecord with context.Background()) | | profile_change rows, actor from the service's actor | Y (no request_id) |

### 1.8 Execution doors that are NOT rule changes (listed for completeness)
POST /tools/call (server.go:6520), POST /code/exec (:1314), POST /preflight (preflight.go:209; sync row type preflight), POST /tool-calls/{id}/replay (server.go:5859): produce tool_call/internal_tool_call/preflight rows, not admin rows.

---

## 2. Server-edition-only routes (//go:build server) -- internal/serveredition
Core httpapi differences: registerClientRoutes is a no-op (internal/httpapi/clients_routes_server.go:12), so /clients* and /oauth/authorizations* do not exist; browser login is off (internal/server/oauth_login_edition_server.go:6). Everything else in section 1 still applies.
Server-edition handlers write **no activity rows** except credential_broker; they log via zap only. They are **not** under httpapi's `TagRequestMeta`/`actorContextMiddleware`: setup.go mounts a sibling chi group with its own auth middleware (setup.go:343-350), and the SSO callback sits on a public route outside it (setup.go:275-286). Handlers that call runtime funnels with r.Context() fall back to RequestActor's AuthContext derivation (surface "api").
| Door | Handler | Funnel | Row today |
|---|---|---|---|
| POST /auth/token (+prefix) | api/auth_endpoints.go:156 generateToken (routes :47/:53) | teamsauth.GenerateBearerToken | **NO RECORD** (zap only :44). JWT mint |
| POST /api/v1/auth/logout | auth/oauth_handler.go:623 HandleLogout (setup.go:345) | session revoke | Not in activity log; one `auth_event` JSONL line via audit.Sink (auth/auth_event.go:28-60) |
| (login callbacks) | auth/oauth_handler.go | | `auth_event` JSONL audit line only |
| POST /user/servers | api/user_handlers.go:432 createServer (routes :177,:195) | userStore.CreateUserServer :514 | **NO RECORD** |
| PUT /user/servers/{name} | :588 updateServer | userStore.UpdateUserServer :653 | **NO RECORD** |
| DELETE /user/servers/{name} | :667 deleteServer | userStore.DeleteUserServer :703 | **NO RECORD** |
| POST /user/servers/{name}/enable | :715 enableServer | SetSharedServerPref :742 / UpdateUserServer :768 | **NO RECORD** |
| POST /user/tokens | :1490 createUserToken (routes :185,:201) | tokenStore.CreateAgentToken :1561 directly (NOT CredentialsService) | **NO RECORD** |
| DELETE /user/tokens/{name} | :1612 revokeUserToken | RevokeAgentTokenForOwner :1633 | **NO RECORD** |
| DELETE /user/tokens/{name}/permanent | :1644 deleteUserToken | DeleteAgentTokenForOwner :1663 | **NO RECORD** |
| POST /user/tokens/{name}/regenerate | :1674 regenerateUserToken | RegenerateAgentTokenForOwner :1724 | **NO RECORD** |
| DELETE /user/credentials/{server} | api/credential_handlers.go:215 deleteCredential (routes :97,:106) | broker store.Delete :18 | **NO RECORD** |
| GET /user/credentials/{server}/connect and /callback (state-changing OAuth connect) | api/credential_handlers.go:246 / :284 | broker connector | credential_broker rows via activityAuditSink (api/broker_audit.go:42; SaveActivityAsync :70) -- no actor keys, has UserID/ServerName/RequestID, no tokens |
| POST /admin/users/{id}/disable, /enable | api/admin_handlers.go:251 / :304 (routes :114-115,:132-133) | userStore.SetUserDisabled + session revoke + RevokeAgentTokensForOwner | **NO RECORD** (zap :`user disabled`) |
| POST /admin/users/{id}/tokens/revoke, /tokens/{name}/revoke | api/admin_tokens.go:66 / :55 (routes :111-112,:129-130) | agentTokens.RevokeAgentTokenForOwner | **NO RECORD** (zap "admin revoked agent token" with actor_user_id/target_user_id) |
| POST /admin/servers/{name}/shared | api/admin_handlers.go:561 toggleSharedServer | sharingUpdater or config.SaveConfig | **NO RECORD** |
| POST /admin/servers/{name}/enable, /disable | :695 / :728 | managementSvc.EnableServer(r.Context(), ...) -> Runtime.EnableServerCtx -> config_change server_enabled/disabled (BUS) | config_change; actor from RequestActor(AuthContext) if no middleware stamp (UNCONFIRMED) |
| POST /admin/servers/{name}/restart | :761 | managementSvc.RestartServer | **NO RECORD** |
Other server-edition notes: internal/serveredition/multiuser/activity.go is a read filter; internal/audit (Spec 107) JSONL lines are a separate sink (attempt + auth_event), not the activity log.

---

## 3. MCP built-in admin tools (internal/server)
Registration: upstream_servers mcp.go:1786, quarantine_security :1863, set_profile :1714 (profile_tool.go:34), profiles :1721 (mcp_profiles_tool.go:84), credentials :1727 (mcp_credentials_tool_schema.go:90), search_servers :1889 / list_registries :1913 (read-only discovery). Every call also writes an internal_tool_call row (BUS, no actor keys; attribution only) via mcp.go:1394.

### 3.1 upstream_servers (handler mcp.go:4783; gates: ReadOnly/DisableManagement/AllowServerAdd/AllowServerRemove, shared agent-op denylist ~:4850-4860)
| Operation | Handler | Funnel | Rows today | Actor? |
|---|---|---|---|---|
| add | handleAddUpstream mcp.go:6264 | upstreamWriter.createServer -> Server.createServer; explicit emit mcp.go:6550 | config_change server_added (redacted, BUS) + internal_tool_call | config_change Y (mcpActorContext, surface=mcp) |
| remove | handleRemoveUpstream :6615 | writer.removeServer; emit :6642 | config_change server_removed + purged (BUS) + internal_tool_call | Y |
| update | handleUpdateUpstream :6728 (mergeUpstreamPatch) | commitUpstreamWriter.updateServer mcp_upstream_writer.go:71 -> Runtime.Commit("mcp_update_server") | **No config_change.** Only internal_tool_call with args.config_diff = redactedConfigDiff (mcp.go:4906-4911) | N |
| patch | handlePatchUpstream :6803 | same writer.updateServer | same: **No config_change**, only internal_tool_call.config_diff | N |
| enable / disable | handleEnableUpstream :5652 | mgmtSvc.EnableServer(mcpActorContext) :5661 -> lifecycle.go:2418/2437 | config_change server_enabled/disabled (BUS) + internal_tool_call | Y |
| restart | handleRestartUpstream :5693 | mgmtSvc.RestartServer(mcpActorContext) :5702 | internal_tool_call only | N |
| refresh | handleRefreshUpstream :5731 | DiscoverServerTools | internal_tool_call only | N |
| add_from_registry | handleAddServerFromRegistry :4698 | AddServerFromRegistryRef(mcpActorContext) :4721 -> Server.AddServer :2379 | config_change server_added (BUS) + internal_tool_call | Y |
| list, tail_log | read-only | | n/a | n/a |

### 3.2 quarantine_security (handler mcp.go:4941; denies non-admin/agent tokens :4970; read-only/DisableManagement refusals emit internal_tool_call with withPolicyRefusal)
| Operation | Handler | Funnel | Rows today | Actor? |
|---|---|---|---|---|
| quarantine_server / quarantine | handleQuarantineUpstream mcp.go:6227 | writer.quarantineServer(mcpActorContext) :6241 -> Runtime.QuarantineServerCtx lifecycle.go:2459 -> :2553 | quarantine_change (BUS) + internal_tool_call | Y |
| approve_tool | handleApproveToolByName mcp.go:5168 | runtime.ApproveTools(.., "mcp") :5179 | tool_quarantine_change tool_approved (BUS) + internal_tool_call | N on the tool row |
| approve_all_tools | :5187 | runtime.ApproveAllTools | same | N |
| block_tool / block_all_tools | :5202 / :5226 | BlockTools / BlockAllTools(..,"mcp") | tool_quarantine_change tool_blocked | N |
| enable_tool / disable_tool | handleSetToolEnabledByName :5240 | SetToolEnabled(..,"mcp") | tool_quarantine_change tool_enabled/disabled | N |
| approve_prompt / approve_all_prompts | prompt_quarantine.go:555 / :571 -> ApprovePrompt :372 / ApproveAllPrompts :421 (SavePromptApproval) | | **NO RECORD** except internal_tool_call | N |
| scan_server | mcp_quarantine_scan.go:64 | scanner StartScan | security_scan result row only | N |
| list_quarantined, inspect_quarantined, inspect_tools, inspect_prompts, get_scan_report | read-only | | n/a | n/a |
(There is no MCP op that un-quarantines a server; unquarantine is REST/CLI/tray only.)

### 3.3 profiles (handler mcp_profiles_tool.go:169; admin only)
| Operation | Funnel | Row | Actor? |
|---|---|---|---|
| create / update / delete / rename / classify | ProfilesService.Create/Update/Delete/Rename/Classify (mcp_profiles_tool.go:288/297/311/321/341) -> MutateConfig -> writeProfileChanges | profile_change (SYNC) + internal_tool_call | Y (profilesActor, surface=mcp) |
| assign (single client) | ClientsService.SetBinding mcp_profiles_tool.go:450 | profile_change | Y |
| assign (bulk from_profile/to_profile) | ClientsService.BulkAssign :465 | profile_change per client | Y |
| list, get, effective_tools, suggest | read-only | n/a | n/a |
| list_clients | shares the REST list path, which can reconcile client rotations (mcp_profiles_tool.go:493-505, clients.go:139-145) | profile_change rotate when it reconciles | Y |

### 3.4 credentials (handler mcp_credentials_tool.go:159; admin only; secret-shaped input screened -> screenedSummary :221)
| Operation | Funnel | Row | Actor? |
|---|---|---|---|
| create_client | CredentialsService.IssueClient :427 | profile_change issue (SYNC) + internal_tool_call | Y |
| create_token | CredentialsService.IssueToken :442 | profile_change issue | Y |
| revoke | CredentialsService.Revoke :405 | profile_change revoke | Y |
| list, get | read-only | n/a | n/a |
(no regenerate/rotate op found in the tool; UNCONFIRMED for browser_login revoke-by-grant path via ref.Grant -> revokeGrantLocked)

### 3.5 set_profile (profile_tool.go:34; handler :91)
Changes only the calling MCP session's selected profile (session state), guarded by the binding guard. **NO RECORD** (no emit in profile_tool.go). Not a config write.

---

## 4. CLI commands (cmd/mcpproxy)
REST-backed (go through section 1; actor = whatever the API key/socket yields, surface=cli via X-MCPProxy-Client cli/): token create/revoke/delete/regenerate (token_cmd.go:188/510/558/607; newTokenCLIClient :169 requires daemon), profile * (restDo, "Every command needs a running daemon" profile_cmd.go:50), client set-profile/add/rotate/forget/upgrade-admin-key-holders/revoke-login (client_binding_cmd.go, client_logins_cmd.go), review approve/reject, skills approve/refresh (cliclient), credential rm/connect (server edition), registry *, doctor fix, security scanners/bundle, upstream enable/disable/restart/add/remove (daemon mode).
| Offline/direct door | File:line | What it writes | Row today |
|---|---|---|---|
| `auth tokens encrypt` / `decrypt` | auth_tokens_cmd.go:172 / :181 (openTokensStorage :43; authTokensEncrypt/Decrypt) | seals/unseals upstream OAuth token rows in config.db directly | **NO RECORD** (no SaveActivity in file) |
| `db compact` | db_cmd.go:179 (compactDatabase :380; offline exclusive lock :333) | rewrites config.db | **NO RECORD** (maintenance) |
| `db stats` | db_cmd.go databaseStats | read-only | n/a |
| `secrets set / del / migrate` | secrets_cmd.go:83 runSecretsSet (resolver.Store :131), del :226, migrate :350 | OS keyring directly (no daemon) | **NO RECORD** |
| `telemetry enable` / `disable` | telemetry_cmd.go:251 / :282 (config.SaveConfig) | edits config file; daemon picks it up by watcher | **NO RECORD** (and the file-watcher reload has none either, see 5) |
| `upstream add/remove/import` config-mode (daemon down) | upstream_cmd.go:1834 (SaveConfig in runUpstreamAddConfigMode :1780), :1942 (remove config mode :1915), :2662 (import config mode :2654) | edits config file directly | **NO RECORD** at write time; on next daemon load the admission gate may write quarantine_change (actor-less) for new servers; no server_added row |
| `connect` / `disconnect` offline backend | connect_credential.go:247-310 (offlineConnectBackend; Activity: sm.SaveActivity :265) | client config file + credential via ClientsService with StrictOfflineBindingGuard | profile_change (SYNC) with actor_kind=cli_offline, surface=cli (:277,:306) for credential changes; file edits unrecorded |
| `trust-cert` | trust_cert_cmd.go | OS trust store | **NO RECORD** (not an mcpproxy rule) |
| `upstream patch` | upstream_cmd.go:2906 | refuses without daemon | n/a |

---

## 5. Non-request changes
| Change | Where | Row today |
|---|---|---|
| Config file watcher hot reload | internal/runtime/config_watcher.go (fsnotify :53, loop :78) -> Runtime.ReloadConfiguration lifecycle.go:2270 (Commit commitReasonFileReload; ReplaceConfig; tx.noDisk) | **NO RECORD of the reload or of what changed** (only emitConfigReloaded SSE event_bus.go:413 + logs). Hand edits of servers, api_key, telemetry, sensitive-data, profiles are all silent. Only side-effect rows: admission-gate quarantine_change for newly seen servers (below), tool_quarantine_change from re-discovery |
| Admission gate at load / every commit | applyConfigLoadAdmissionGate config_load_admission_gate.go:146, invoked inside Commit commit.go:684 (every commit, incl. file reload and boot via gateInitialConfig :395-409) | quarantine_change (quarantined=true, reason first-seen / never-approved) emitted via EmitActivityQuarantineChange (no ctx) from a detached goroutine (:263-268). BUS, no actor keys (no RequestActor in Background), no request_id. Retained-quarantine case (:~249 "Retaining recorded quarantine") emits nothing |
| Startup client-rotation reconcile | ClientsService.Reconcile clients_service_reconcile.go:33 -> finalizeLocked(reconcilerActor) :81/:100 | profile_change rotate finalized (SYNC), actor_kind=system, surface=api |
| Skill refresh transitions (scheduled/startup) | skills_catalog.go:671/686 | skill_quarantine_change (BUS); actor_kind=system when ctx has no caller |
| Tool discovery / auto-approve sweeps | tool_quarantine.go:1219,1264,1468,2514,2645,2650; tool_quarantine_scan_approve.go:120 | tool_quarantine_change tool_discovered/tool_auto_approved/tool_description_changed (BUS, no actor) |
| Telemetry startup outcome | server.go:1744 -> Runtime.UpdateTelemetry runtime.go:1060 | **NO RECORD** |
| `serve` generating and persisting an API key when none is configured | cmd/mcpproxy/main.go:567-594 | **NO RECORD** (mapped: `config.update`, `internal`/`startup`, `api_key` withheld) |
| Server auto-quarantine by scanner verdict | EmitActivityQuarantineChange (no ctx) callers: config_load_admission_gate.go:265 only (others are Ctx variants) | see above |
| System start/stop | server.go:1494/3945 EmitActivitySystemStart, :1601 Stop | system_start / system_stop (BUS) |

---

## 6. Gap summary (what is NOT recorded or lacks actor)
NO RECORD: DELETE /tokens/{name}/permanent; POST /clients/{client}/rotate (stage); POST /secrets, DELETE /secrets/{name}, /secrets/migrate, CLI secrets; POST /servers/enable_all|disable_all; /servers/restart_all, /reconnect, /restart, /refresh, /discover-tools; upstream OAuth login/logout; POST /config/apply, PATCH /config, PATCH /config/docker-isolation (except profile diffs); registries add/edit/remove/refresh; scanners install/remove/configure; TPA bundle rollback/unpin/reload/refresh; security/reject; PUT /profiles/active; onboarding mark; diagnostics fix; file-watcher reload; CLI telemetry enable/disable, auth tokens encrypt/decrypt, db compact, upstream config-mode add/remove/import; MCP upstream_servers restart/refresh, approve_prompt(s), set_profile; ALL server-edition user/admin handlers except enable/disable-server (via config_change) and credential_broker.
Row exists but NO actor: every tool_quarantine_change (approve/block/enable/disable/auto), admission-gate quarantine_change, MCP upstream_servers update/patch (only internal_tool_call.config_diff), security_scan, credential_broker.
Droppable (bus): config_change, quarantine_change, tool_quarantine_change, skill_quarantine_change, internal_tool_call. Sync: profile_change only.

---

## 7. Corrections and additions from the round-1 cross-review (verified)

- `POST /secrets/migrate` is a dry run (`internal/httpapi/server.go:5113-5127`, `dry_run: true` always); CLI `secrets migrate` stops at "not yet implemented" (`cmd/mcpproxy/secrets_cmd.go:390-407`). Both are exempt, not writers.
- `POST /servers/{id}/config-to-secret` stores the keyring entry before patching the config and can return 500 after the store (`server.go:3297-3309,3338-3353`): a partial-failure door.
- Production REST PATCH and config-to-secret call `PatchServer`, not `UpdateServer` (`server.go:3147-3162,3338-3347`).
- The server-edition routes are a sibling chi group with their own auth middleware (`internal/serveredition/setup.go:343-350`), outside `TagRequestMeta` / `actorContextMiddleware` (`internal/httpapi/server.go:1100-1111`).
- `GET /user/credentials/{server}/callback` persists the brokered credential (`internal/serveredition/api/credential_handlers.go:280-331`); `GET /clients…` and `GET /connect…` can reconcile client rotations on read (`internal/httpapi/clients.go:139-175`, `connect.go:140-142,207-214`). State-changing `GET`s exist.
- MCP `quarantine_security` accepts the legacy alias `quarantine` (`internal/server/mcp.go:5001-5004`). MCP `credentials revoke` has three branches: `token`, `client` (key + cascade to every login), `client`+`grant` (`mcp_credentials_tool.go:369-405`); the MCP tool has no rotate operation.
- The MCP admin tools and Connect mint a correlation id that never reaches the mutation context (`mcp_credentials_tool.go:174-180,345-351`; `clients_service_connect.go:217-233,328-363` use `context.Background()`).
- Connect undo can revoke a credential and restore a binding (`internal/connect/undo.go:317-331`). Supported-client rotation goes through Connect, not `ClientsService.Rotate` (`client_bindings.go:307-344`).
- CLI writes missing above: `status --reset-key` (`cmd/mcpproxy/status_cmd.go:150-155,560-572`), `telemetry enable|disable` (`telemetry_cmd.go:251,282`), `upstream add-json` offline branch and `upstream add` keyring flags written before the backend split (`upstream_cmd.go:123-131,1579-1601,2034-2042`), standalone `auth login|logout` (`auth_cmd.go:767-785,977-988`).
- Server edition: `POST /auth/token` is stateless JWT signing (`api/auth_endpoints.go:177-203`); SSO callbacks can create a user or change its role (`auth/oauth_handler.go:461-474,511-525`).
- Admission emits its activity goroutine before persistence (`config_load_admission_gate.go:253-270`); `EnableServerCtx` emits even on a no-op with `previous = !enabled` (`lifecycle.go:2423-2437`); `applyConfigWith` discards the `CommitResult` (`runtime.go:2273-2329`).
- Scanner routes `/install` and `DELETE /security/scanners/{id}` are aliases of enable/disable (`server.go:1393-1395,1409-1411`).
