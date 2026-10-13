# Contracts: MCP 2026-07-28 Upgrade

This feature adds **no REST endpoint and no new MCP tool**, so there is no OpenAPI delta and `make swagger` produces no change. Its contracts are protocol-level and behavioural, and each one below is stated as an assertion a test can make.

Two of them are contracts mcpproxy must *keep* (regression guards for the bump), and the rest are contracts it must *newly satisfy* once the era pins lift.

## C1 — The bump is inert on the wire (Phase B gate)

| Assertion | How it is checked |
|---|---|
| A client that does not pin a version negotiates `2025-11-25` against mcpproxy | wire capture of the `initialize` response |
| mcpproxy's `initialize` to an upstream requests `2025-11-25` | wire capture on the upstream hop |
| `GET`/`DELETE` on the MCP endpoint behave exactly as on `main` | status-code parity test |
| The three frozen tool-surface goldens are byte-identical | existing golden tests |

## C2 — Tool-identity hash is library-version-independent (Phase B gate)

For each of four schema fixtures, the Spec-032 approval hash must be equal under the old and new decoder:

| Fixture | Expectation |
|---|---|
| draft-07 `definitions` + `#/definitions/X` refs | hashes equal (requires the normalizer fix) |
| plain schema, no `$defs`/`$ref` | hashes equal |
| 2019-09 native `$defs` + `#/$defs/` refs | hashes equal |
| `#/definitions/X` ref with no `definitions` block | hashes equal |

Rationale: without this, a library upgrade re-flags unchanged tools as rug-pulls.

## C3 — `*/list` never varies by connection state (FR-012)

| Assertion | Era |
|---|---|
| Two clients calling `tools/list` receive byte-identical results | both |
| `prompts/list` does not change after `set_profile` | both — **this fails on `main` today** |
| Direct-mode `tools/list` does not change after `set_profile` | both |
| Per-profile filtering still applies when driven by `/mcp/p/<slug>` | both |

## C4 — Profile selection is request-carried (FR-011, D1)

| Assertion |
|---|
| A modern request to `/mcp/p/<slug>` is scoped to that profile with no session id bound |
| A modern `set_profile` returns a structured error naming the `/mcp/p/<slug>` form |
| An unknown slug reports "unknown profile", not the session error, in both eras |
| A modern **stdio** request cannot persist a profile selection, despite a non-empty session id |
| An agent-token pin still wins over a conflicting URL slug (403) |

## C5 — Identity scoping survives statelessness (FR-013)

| Assertion |
|---|
| Two agent tokens with different `allowed_servers` each see only their own servers, with no session |
| Server edition: two OAuth users with different entitlements are isolated, with no session — **currently untested** |

## C6 — Cross-client isolation (R1)

| Assertion |
|---|
| A `notifications/cancelled` naming request id N from client A does not cancel client B's in-flight request with the same id |

Must hold before the client-facing pin lifts.

## C7 — Upstream hop carries required headers (FR-007, re-scoped to requests)

| Assertion |
|---|
| Every forwarded **request** carries `Mcp-Method` and `MCP-Protocol-Version` |
| `Mcp-Name` is present on `tools/call`, `resources/read`, `prompts/get` |
| A header/body version mismatch is rejected with `-32020` and not forwarded |

Notifications are explicitly out of scope: the library provides no header path for them and mcpproxy originates none.
