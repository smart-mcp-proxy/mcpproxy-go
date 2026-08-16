# Tasks: describe_tool Check Mode (099)

- [ ] T001 Schema: `check` + `filters` params via a shared option builder on both describe_tool registrations (default surface + retrieve_tools mode); tokenized budget test constant → ≤250 (FR-015)
- [ ] T002 Handler check-mode branch in `internal/server/mcp_describe_tool.go`: FR-012a strict validation (incl. reserved `expect_hashes` error, filters-without-check error), 50-raw cap, normalize→dedup→first-occurrence order, evaluate via the 098 glue with session scope ∩ token scope ∩ pin at forced agent-token tier (FR-009/FR-009a), verdict-only payload with `verdict`/`checked_at`/`request_id` (FR-004), MCP-error mapping for 400/503 classes (FR-012); unit tests per FR cell
- [ ] T003 Activity: synchronous preflight record with surface marker `mcp-check`; suppress plain-mode `internal_tool_call` record for check runs; write-failure fails the call (FR-013); tests
- [ ] T004 Plain-mode disclosure fix: `invisible` → `not_found` (both code paths), spec-085 contract amendment + release note; byte-identity replay test with the single enumerated delta (FR-011)
- [ ] T005 FR-018a: REST `disclosureTier` maps `AuthTypeUser` to agent-token tier; disclosure tests both editions
- [ ] T006 Goldens: enumerated-delta snapshot test conversion; regenerate the two describe_tool surface goldens deliberately (FR-014); code_execution golden must NOT move
- [ ] T007 Sabotage matrix: mcp-check + mcp-plain rows (13 observable codes + scope/unconfigured collapse + filter cells + cap boundary + reserved-field + filters-without-check), reflection-gate exemptions for hash_mismatch/server_not_in_scope, mid_indexing note correction + never-indexed-while-connecting row (FR-016); parity test in-band vs REST at agent-token tier (FR-017)
- [ ] T008 Docs (FR-018/FR-002): feature page in-band section + agent-loop example, spec-085 contract update, naming-divergence note, interim story for code_execution/direct; release notes breaking-change entry
- [ ] T009 Gates: CI="" go test -race ./..., server-edition build/test/lint, golangci both tag sets, swagger/generate-types diff-clean (contracts untouched expected), frontend build if touched
- [ ] T010 opencode review of full diff (≤5 rounds), fix genuine findings; PR; green; merge per standing procedure
