# Contract: `oauth.skip_issuer_validation` (Spec 135)

```json
{ "name": "legacy-provider", "url": "https://mcp.legacy.example/mcp", "protocol": "http",
  "oauth": { "skip_issuer_validation": true } }
```

- Type bool, default false, per server only. No global or env equivalent.
- Skips: the RFC 9207 `iss` comparison (FR-003/FR-005) and the RFC 8414 metadata issuer equality compare (FR-006), each with a WARN log line per skipped check.
- Never skips: the strict callback parse (FR-001), refusal to send stored credentials to another issuer (FR-009), the redirect policy (FR-010), trust-on-first-use rules (FR-011), one issuer per row (FR-012).
- Binding identity while on: the discovery target (PRM `authorization_servers` entry or configured metadata URL), not the document's declared issuer (FR-013).
- Validation results are partitioned by policy, so a strict server sharing cache keys is unaffected (FR-007).
- Not credential identity: excluded from `config.OAuthConfigChanged`; toggling it never clears a grant or moves the OAuth epoch. Hot-reloadable.
- Surfaces: REST server config (OAS), Web UI server editor (OAuth section, with help text naming the risk), macOS server editor, doctor warning `issuer_validation_disabled`.
