# Implementation Plan: describe_tool Check Mode (099)

**Branch**: `099-describe-check-mode` | **Date**: 2026-08-16 | **Spec**: [spec.md](spec.md)

## Summary

One optional `check: true` parameter on the existing `describe_tool` (default + retrieve_tools surfaces): verdict-only results from `preflight.Evaluate` via the same glue seam REST uses, batch cap 50, whole surface pinned to the agent-token tier, synchronous preflight activity record with a surface marker. Plus two inherited 098 errata (REST AuthTypeUser tier, matrix mid_indexing note) and the plain-mode `invisible`→`not_found` disclosure fix.

## Technical Context

Go 1.24 toolchain, existing deps only. All state via the shipped `internal/preflight` evaluator + `internal/server/preflight_glue.go` seam — no new state sources. Registration touches `retrieveToolsDetailOption`-style shared builders on the two describe_tool surfaces only; code_execution/direct goldens stay byte-identical.

## Constitution Check

PASS on all six principles (same posture as 098: stat-only reads, no new goroutines, no config fields, security tiers fail-closed, TDD via matrix rows, docs in scope). No Complexity Tracking entries.

## Structure

```
internal/server/mcp_describe_tool.go     # check-mode branch: parse/validate (FR-012a), 50-cap, dedup, evaluate, verdict payload, request_id surfacing
internal/server/mcp.go / mcp_routing.go  # describe_tool schema: check + filters params on BOTH surfaces (shared option builder)
internal/server/preflight_glue.go        # reuse RunPreflight with session scope + forced agent-token tier; surface marker
internal/httpapi/preflight.go            # FR-018a: disclosureTier — AuthTypeUser ⇒ agent-token tier
internal/server/toolslist_snapshot_test.go + testdata/toolslist_goldens/  # enumerated-delta conversion + deliberate golden regen (2 surfaces)
internal/server/testdata/preflight_sabotage_matrix.json  # mcp-check/mcp-plain rows + mid_indexing note fix
internal/server/preflight_e2e_test.go / preflight_matrix_test.go  # matrix driver extension + reflection-gate exemptions (hash_mismatch, server_not_in_scope)
docs/features/tools-preflight.md, docs (spec-085 contract), release notes  # FR-018
```

## Key decisions (locked, see spec)

Trimmed `expect_hashes` (reserved-field error); `filters` naming; agent-token tier everywhere in-band; budget ≤250 (tokenized test + golden pin); one activity record per check run (no duplicate internal_tool_call); plain-mode byte-identity with the single enumerated `invisible`→`not_found` delta + release note.
