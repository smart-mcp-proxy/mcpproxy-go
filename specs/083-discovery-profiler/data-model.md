# Data Model: Discovery Effectiveness Profiler

**Date**: 2026-07-14 · Entities extend the existing `bench` package types (`Tool`, `Corpus`, `GoldenQuery`, `Label`, `ModeResult`, `LiveReport`).

## Entities

### EncodingArm (interface)
| Field/Method | Type | Notes |
|---|---|---|
| `Name()` | string | unique registry key: `baseline_json`, `compact_sig`, `tscg`, `toon_listing`, `toon_results`, `tron_dedup` |
| `IndexAltering()` | bool | true → retrieval scoring required (FR-008) |
| `EncodeTool(t Tool)` | (string, error) | deterministic; error → skip counted (FR-009/010) |
| `EncodeListing(ts []Tool)` | (string, error) | whole-response rendering (formats with shared preamble/dedup, e.g. TRON, amortize here) |
| `EncodeIndexMetadata(t Tool)` | (config.ToolMetadata, error) | **index-altering arms only**: the exact `Name`/`Description`/`ParamsJSON` the production index ingests for this arm — the single mapping the armindex builder and the `IndexAltering()` contract test both use (no implementer guessing). Rendering-only arms return the tool unchanged. |

Validation: registry rejects duplicate names; golden-output fixture tests pin encoded bytes per arm.

### ArmResult
| Field | Type | Notes |
|---|---|---|
| `Arm` | string | |
| `CorpusID` | string | e.g. `corpus_v2@<sha>`, `toolret@<snapshot>` |
| `TotalTokens`, `MeanTokens`, `P95Tokens` | int | FR-005 |
| `SavingsVsBaselinePct` | float64 | negative allowed |
| `SkippedTools` | int + `[]SkipExample` | FR-009 |
| `Skipped` | bool + `SkipReason` string | arm-level (runtime absent) |
| `Quality` | *RetrievalScore | nil ⇔ quality-neutral (FR-008) |
| `HeaviestTools` | []ToolTokenEntry (N=10) | FR-020 |
| `PayloadClass` | enum `listing`\|`results` | FR-007: `toon_results` rows carry `results` |
| `FixtureID` | string | results rows: snapshot ID of result_fixtures_v1 |
| `TabularCount`/`NonTabularCount` | int | results rows only (FR-007 classification split) |

### RetrievalScore (flat report DTO)
Flat fields `recall_at_1/3/5/10`, `mrr`, `ndcg_at_10`, `map`, `metric_note` (gain formula string, FR-012) — a new DTO **mapped from** the existing `bench.RetrievalMetrics` (which nests `recall_at` as a map); the mapping function is the single conversion point so the report schema stays flat and stable.

### DiscoveryResponseMeasurement
| Field | Type | Notes |
|---|---|---|
| `QueryID` | string | golden-set query |
| `TotalTokens` | int | tokenized full MCP text content |
| `Components` | map[string]int | `input_schemas`, `descriptions`, `usage_instructions`, `metadata`, `other` — **invariant: sum == TotalTokens by construction**, via span-based attribution: the canonical response text is partitioned into contiguous byte spans labeled by component; each token is attributed to the component owning its starting byte (BPE is not additive across fields, so per-field re-tokenization is forbidden) (FR-002) |
| `ResultCount` | int | tools returned |
| `LatencyMs` | float64 | client-side (FR-023) |

Aggregate: `ResponseCostSummary{P50, P95, Max, Mean int; PerQuery []DiscoveryResponseMeasurement}`.

### BreakEvenAnalysis
| Field | Type | Notes |
|---|---|---|
| `NaiveFullMenuTokens` | int | FR-004 |
| `ProxyMenuTokens` | int | FR-004 |
| `MeanResponseTokens` | int | |
| `BreakEvenCalls` | float64 | formula per FR-003 |
| `NoBreakEven` | bool | numerator ≤ 0 case |

### SessionCostEstimate
| Field | Type | Notes |
|---|---|---|
| `Arm` | string | |
| `CallsPerSession` | int | {1,3,5,10} |
| `RetryRate` | float64 | documented default per arm (research D8) |
| `EstimatedTokens` | int | provenance = ESTIMATE (FR-019) |

### CorpusDescriptor
| Field | Type | Notes |
|---|---|---|
| `ID`, `Name`, `Version` | string | snapshot sha / HF revision (FR-012) |
| `ToolCount` | int | drift warning vs expected (edge case) |
| `License`, `Attribution` | string | FR-013 |
| `Committed` | bool | ToolRet=false |
| `DegenerateDescriptions` | int + rule list | FR-020 |

### LapVerdict
| Field | Type | Notes |
|---|---|---|
| `Executed` | bool + `SkipReason` | SC-006 |
| `Version` | string | pinned 0.8.0 |
| `MenuTokens` | int | LAP bucket A |
| `InHouseMenuTokens` | int | |
| `DivergencePct` | float64 | warn > ±15% (FR-016) |
| `Grade` | string | LAP letter grade |
| `ArtifactPath` | string | |

### ReportV2 (envelope)
`report_version: 2`, `GeneratedAt`, `Tokenizer` (+caveat string), `ProxyVersion`, `Config` (tools_limit, routing_mode), `Corpora []CorpusDescriptor`, `Arms []ArmResult`, `ResponseCost ResponseCostSummary`, `BreakEven BreakEvenAnalysis`, `SessionEstimates []SessionCostEstimate`, `Latency LatencyStats` (existing), `Lap LapVerdict`, `Provenance map[metric]enum{measured,computed,estimated}` (SC-005), `SubsetSeed/SubsetSize` (FR-014).

## Relationships

- ReportV2 1—N ArmResult; each ArmResult references exactly one CorpusDescriptor.
- ResponseCostSummary and BreakEvenAnalysis are live-run only (require MCP calls); ArmResults are offline-computable on corpus_v2.
- RetrievalScore attaches to ArmResult only when `IndexAltering()`; baseline arm's score on the in-house golden set gates SC-003.

## State/Flow

1. Offline: load corpus (v1/v2/public) → run arms → ArmResults (+arm index → scores).
2. Live: boot proxy → menu counts → per-query MCP retrieve_tools → ResponseCostSummary → BreakEven → SessionEstimates.
3. CI: offline + live + LAP → ReportV2 → dashboard render → artifacts.
