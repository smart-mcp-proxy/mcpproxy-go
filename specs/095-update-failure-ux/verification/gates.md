# Gates — spec 095 (2026-08-13)

## Automated

| Gate | Command | Result |
|---|---|---|
| Go full race suite | `go test -race ./internal/...` | ✅ exit 0 (all packages ok) |
| Go targeted (new) | httpapi + telemetry + diagnostics + server, `-count=1` and `-race` | ✅ all ok; new tests: handler matrix (4×204, 11×400, 500, 401, 405), gate composition ×7, disable→record→re-enable, scanner ×12 violations + clean-pass, OAS enum pin |
| Lint (CI config) | `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` | ✅ 0 issues (exit 0) |
| Swagger artifacts | `make swagger` + committed regen | ✅ additive diff only (+ new path & schema) |
| Swift full suite (post-implementation) | `cd native/macos/MCPProxy && swift test` | ✅ `Executed 1013 tests, with 1 failure (0 unexpected)` — the 1 failure is the pre-existing environmental AppLifecycleTests case documented in baseline.md; +74 tests vs baseline |
| Swift SC-006 spot | Update* suites | ✅ 63 tests, 0 failures, assertions unmodified |
| Swift full suite (post-wiring-fix) | re-run after the MCPProxyApp recorder fix | ✅ same summary (see below) |

## REST smoke (isolated core `mcpproxy-095`, semver-stamped v0.99.0, port 18095, scratch home)

- 4 valid stages → **204** each; repeat → count increments (download=2)
- bad stage / extra field / trailing JSON → **400**; no API key (TCP) → **401**; GET → **405**
- payload preview: `{"MCPX_UPDATE_APPCAST_FAILED":1,"MCPX_UPDATE_DOWNLOAD_FAILED":2,"MCPX_UPDATE_INSTALL_FAILED":1,"MCPX_UPDATE_OTHER_FAILED":1}`
- **restart survival**: counts identical after core restart ✅
- **opt-out no-op**: restart with `MCPPROXY_TELEMETRY=false` → POST returns 204, count unchanged ✅
- **version skew (T028)**: covered by handler tests (404-shape single-attempt behavior asserted in APIClientUpdateFailureTests: 404/500/transport each → exactly one log line, no retry)

## Live rig

See [manual-protocol.md](./manual-protocol.md) — all steps pass; one real defect (modal-starved recorder) found and fixed during verification.
