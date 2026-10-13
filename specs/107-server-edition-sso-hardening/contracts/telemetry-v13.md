# Telemetry payload v13 (FR-038, PR-B)

`internal/telemetry/telemetry.go`: `const SchemaVersion = 13`; the v3…v12 change-log comment gains:

```
// v13 (Spec 107): feature_flags.server_edition_enabled (bool), feature_flags.idp_provider
//   (closed enum google|github|microsoft|oidc|none — never an issuer), user_count_bucket
//   (closed enum 0|1-10|11-100|101-1000|1000+; "0" when no user counter is installed — the personal
//   edition — and omitted only when the installed counter errors).
//   env_markers.is_container unchanged. No worker migration: fields ride in payload_json.
```

| Field | Location | Type / vocabulary | Source | Personal edition value |
|---|---|---|---|---|
| `feature_flags.server_edition_enabled` | `FeatureFlagSnapshot` (`feature_flags.go:13-51`, built in `BuildFeatureFlagSnapshot :122-170`) | bool | `config.ServerEditionEnabled(cfg)` (build-tagged accessor) | `false` |
| `feature_flags.idp_provider` | same | `google`\|`github`\|`microsoft`\|`oidc`\|`none` | `config.IdPProviderFamily(cfg)`; `none` when disabled/unset | `"none"` |
| `user_count_bucket` | `HeartbeatPayload` (`telemetry.go:141-310`), spliced in `BuildPayload` (`:1201`, after `:1391-1398`) | `0`\|`1-10`\|`11-100`\|`101-1000`\|`1000+` (`bucketUpstream` vocabulary, `registry.go:642-655`) | nil-safe `userCounter func() (int, error)` accessor installed by server-edition wiring (pattern `telemetry.go:427-456`); error → field omitted | `"0"` when the accessor is nil (spec US7.1) |
| `env_markers.is_container` | unchanged (`env_markers.go:22`) | bool | unchanged | as detected |

Privacy (Spec 042 contract): `TestPayloadHasNoForbiddenSubstrings` and `ScanForPII` run with a fixture whose issuer host (`login.corp.example`), group names, admin emails and server names are the forbidden substrings; `ScanForPII` rule 3 (`anonymity.go:604-615`, `DisallowUnknownFields` on `env_markers`) is unaffected because no new `env_markers` key is added. `docs/features/telemetry.md` "What is collected" lists the three fields; "What is NOT collected" keeps "User identity, email", "Server names/URLs" and adds "IdP issuer, group names".

Test sites pinned at v12 → 13 (eleven, per FR-038): `payload_v7_test.go:20,81,112`, `payload_v2_test.go:91,138`, `telemetry_test.go:315,323`, `payload_privacy_test.go:147`, `current_error_codes_test.go:268`, `tpa_scanner_test.go:267`, `tpa_funnel_v9_test.go:263`. Guard: `schema_version_guard_test.go` walks the package files and asserts zero hits for the v12 patterns — the regex is assembled from fragments (`"schema_version\":" + "12"`, `"!= " + "12"`, …) and the guard skips its own file, otherwise its literal self-matches and the guard can never pass; the server/personal accessor values are proven by tagged fixture files (`payload_v13_fixture_{server,personal}_test.go`).

Worker (`~/repos/mcpproxy-telemetry`): no change — ingest stores `payload_json` wholesale and `validateV*Payload` accepts unknown top-level keys; dashboards query `json_extract(payload_json,'$.feature_flags.idp_provider')`.
