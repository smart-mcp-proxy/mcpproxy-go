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
- **Protection result**: every login the switch lets through is stored as `mix_up_protected: false`. That includes a login with a matching callback `iss` whose metadata comparison was skipped.
- **Never skipped**: the strict callback parse (FR-001); a missing or ambiguous metadata issuer (FR-006); same-origin metadata retrieval (FR-006a); the refusal of origin fallback for a path issuer (FR-004a); refusal to send credentials across bindings (FR-009/009a); the redirect policy (FR-010); the extras classes (FR-010b); trust-on-first-use rules (FR-011/011a); one binding per row and static-credential provenance (FR-012); the target and source-class comparison (FR-013).
- **Binding while on**: `{issuer, target, source}` is still stored in full. Only the declared-issuer comparison is dropped, so turning the switch off again keeps continuity.
- **Not credential identity**: excluded from `config.OAuthConfigChanged`. Toggling it never clears a grant or moves the OAuth epoch. It hot-reloads. Validation results are partitioned by policy (FR-007), so a strict server that shares cache keys is unaffected.
- **Redaction**: a `ServerFieldMaskDecisions` entry marks it non-secret (`internal/oauth/serverfields.go:221`).
- **Surfaces**: the REST server config (OAS); the Web UI server editor (OAuth section, help text naming the risk); the macOS server editor; the doctor and `GET /api/v1/diagnostics` warning `issuer_validation_disabled`; the server's OAuth detail (REST, Web UI, tray); `mcpproxy auth status --server=<name>`.
