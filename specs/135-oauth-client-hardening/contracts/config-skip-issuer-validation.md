# Contract: `oauth.skip_issuer_validation` (Spec 135)

```json
{ "name": "legacy-provider", "url": "https://mcp.legacy.example/mcp", "protocol": "http",
  "oauth": { "skip_issuer_validation": true } }
```

- **Type and scope**: bool, default false, per server only. There is no global setting and no environment-variable equivalent. Config validation rejects any non-bool value.
- **Outcomes the switch allows** (FR-014). Each applies to every issuer source, `fallback` included, and each skipped check logs one WARN with the server name and scrubbed values:
  - a callback `iss` that is present and differs from the expected issuer;
  - a callback with no `iss` while the issuer advertises support;
  - the FR-005 ordering, so the provider's `error` is reported;
  - a metadata `issuer` that fails FR-006's equality compare.
- **Protection result**: having the switch on does not by itself change the result. A login is stored as `mix_up_protected: false` when a check was **actually waived**: the callback `iss` was wrong or absent, or the metadata comparison failed and was skipped. That includes a login whose callback `iss` matches but whose metadata comparison was skipped. A login whose metadata and `iss` both match is stored as `true`, as in strict mode, provided its source is eligible.
- **Never skipped**: the strict callback parse (FR-001); a missing or ambiguous metadata issuer (FR-006); same-origin metadata retrieval (FR-006a); the refusal of origin fallback for a path issuer (FR-004a); refusal to send credentials across bindings (FR-009/009a); the redirect policy (FR-010); the extras classes (FR-010b); trust-on-first-use rules (FR-011/011a); one binding per row and static-credential provenance (FR-012); the target and source-class comparison (FR-013).
- **Binding while on**: `{issuer, target, source}` is still stored in full. Only the declared-issuer comparison is dropped, so turning the switch off again keeps continuity. A refresh or registration reuse that is allowed under skip keeps the full stored binding and never relabels it. A transition happens only when a new registration or login writes a binding that is not `Equal`.
- **Not credential identity**: excluded from `config.OAuthConfigChanged`. Toggling it never clears a grant or moves the OAuth epoch. It hot-reloads. Validation results are partitioned by policy (FR-007), so a strict server that shares cache keys is unaffected.
- **Redaction**: a `ServerFieldMaskDecisions` entry marks it non-secret (`internal/oauth/serverfields.go:221`).
- **Write route**: the existing per-server update (`PATCH` at `internal/httpapi/server.go:2356-2362`, used by `frontend/src/views/ServerDetail.vue` and `native/macos/.../ServerDetailView.swift`) today rejects any non-nil `oauth` block. It is changed to accept an `oauth` object whose **only** key is `skip_issuer_validation`, merged into the stored block. Any other OAuth key is still rejected, as today. An omitted field leaves the value unchanged, and an explicit `false` sets it. Tests: omitted, `true` and `false` against a live grant, each asserting that the row and epoch are unchanged.
- **Surfaces**: the REST server config (OAS); the Web UI server editor (OAuth section, help text naming the risk); the macOS server editor; the doctor and `GET /api/v1/diagnostics` warning `issuer_validation_disabled`; the server's OAuth detail (REST, Web UI, tray); `mcpproxy auth status --server=<name>`.
