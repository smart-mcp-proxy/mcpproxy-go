# Contract: `oauth.skip_issuer_validation` (Spec 135)

```json
{ "name": "legacy-provider", "url": "https://mcp.legacy.example/mcp", "protocol": "http",
  "oauth": { "skip_issuer_validation": true } }
```

- Type bool, default false, per server only. No global or env equivalent.
- Skips: RFC 9207 `iss` check (FR-003/FR-005) and RFC 8414 metadata issuer equality (FR-006), each with a WARN log line per skipped check.
- Never skips: rejection of repeated callback parameters (FR-001), recording of the issuer (FR-008), refusal to send stored credentials to another issuer (FR-009).
- Not credential identity: excluded from `config.OAuthConfigChanged`; toggling it never clears a grant or moves the OAuth epoch. Hot-reloadable.
- Surfaces: REST server config (OAS), Web UI server editor (OAuth section, with help text naming the risk), macOS server editor, doctor warning `issuer_validation_disabled`.
