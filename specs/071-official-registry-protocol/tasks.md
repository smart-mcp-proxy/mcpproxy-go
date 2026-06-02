# Tasks — 071 Official MCP registry v0.1 protocol

Dependency-ordered; TDD (failing test first) per task.

- [x] **T01** Golden fixture `testdata/official_v0.1_servers.json` (packages-only, remotes-only, hybrid, deprecated, non-latest, deleted).
- [x] **T02** `official_test.go` — failing tests: `parseOfficialPage` envelope + status/isLatest filter; classification matrix; `fetchOfficialServers` cursor pagination; `referenceServers` offline.
- [x] **T03** `official.go` — `parseOfficialPage` / `parseOfficialItems` / `officialServerToEntry` / `buildPackageCommand` + camelCase/snake_case-tolerant map helpers.
- [x] **T04** `official.go` — `fetchOfficialServers` cursor follow-loop, `buildOfficialURL` (version=latest, limit, search, cursor), versioned User-Agent (#566).
- [x] **T05** `reference.go` — built-in curated reference set (7 stdio servers), `referenceServers()`.
- [x] **T06** `search.go` — `fetchServers` short-circuits official + reference; thread `query`; route dispatch to `parseOfficialPage`; remove URL-synthesis loop.
- [x] **T07** `search.go` — remove dropped parsers (`parseMCPRun`, `parseMCPStore`, `parseFleur`, `buildFleurInstallCmd`, `parseRemoteMCPServers`, `parseAzureMCPDemoWithoutGuesser`, `constructServerURL`) + dead constants + their tests; keep `parseOpenAPIRegistry` as legacy-flat fallback.
- [x] **T08** `config.go` — `DefaultRegistries()` rewire (official + reference + docker; pulse/smithery opt-in).
- [x] **T09** Update `registry_data_test.go` + `integration_test.go` to the new default set.
- [x] **T10** `server/consistency_official_test.go` — cross-surface #567 regression (REST + CLI).
- [x] **T11** Docs: `docs/registries.md` (new) + README registry list; CLAUDE.md unchanged (40k gate).
- [x] **T12** Local green: `run-linter.sh`, `go test ./internal/... -race`, `test-api-e2e.sh`. Open PR (never merge); dual-AI review.
