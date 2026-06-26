# Quickstart: Deterministic Offline MCP Tool-Scanner v2

## What changes for a user

Nothing in the command surface. You still run:

```bash
mcpproxy security scan <server>          # or --all
```

The findings now carry a **confidence** value and a **signals** list (which checks fired), and the scanner catches structural attacks (hidden Unicode, cross-server shadowing, decode-to-shell) it previously missed. Hard-tier findings auto-quarantine; soft-tier findings are raised for review with severity by how many independent checks agreed.

## What changes for a developer

### Run the detector tests

```bash
go test -race ./internal/security/detect/...
go test -race ./internal/security/...
```

### Run the corpus eval gate locally

```bash
go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets --gate --min-recall 0.90 --max-fp 0.05
```

Expect a metrics breakdown and exit 0 when recall ≥ 0.90 and hard-negative FP ≤ 5%.

### Add a new check

1. Create `internal/security/detect/checks/<name>.go` implementing `Check`.
2. Write `<name>_test.go` first (TDD) with MUST-flag and MUST-NOT-flag cases from the contract table.
3. Register it in the engine's check set.
4. Add corpus entries exercising it; confirm the gate still passes.

## Acceptance smoke (maps to spec scenarios)

| Spec scenario | Quick check |
|---------------|-------------|
| US1 hidden-Unicode → quarantine | scan a fixture with zero-width chars → hard finding `unicode.hidden` |
| US1 shadowing | two servers, same tool name → hard finding `shadowing.cross_server` |
| US1 decode-to-shell | description with base64 of `curl x \| sh` → hard finding `payload.decoded`, evidence = decoded |
| US2 hard-negative | "detects prompts such as 'ignore previous instructions'" → no quarantine |
| US2 variant | "don't disclose" and "do not tell the user" both flagged |
| US3 gate | corpus eval fails build when recall < 0.90 |
| US4 transparency | multi-signal tool → finding lists check IDs + confidence; severity rises with count |
