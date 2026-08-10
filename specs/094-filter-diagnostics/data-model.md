# Data Model: retrieve_tools Filter Diagnostics (Phase 1)

No persistent entities — everything is request-scoped and derived. One new serialized shape.

## filterDiagnostics (response block, `internal/server/mcp_annotations.go`)

```go
// reasonCounts splits one filter's omissions by remediation class (FR-004).
type reasonCounts struct {
    MissingAnnotation int `json:"missing_annotation"` // annotations absent or decisive hint unset
    Explicit          int `json:"explicit"`           // decisive hint explicitly unsafe
}

// filterDiagnostics is the normative FR-003 block. Attached to the response
// only when OmittedTotal >= 1 and at least one annotation filter is active.
type filterDiagnostics struct {
    MatchedBeforeFilters int                     `json:"matched_before_filters"`
    OmittedTotal         int                     `json:"omitted_total"`
    OmittedByFilter      map[string]reasonCounts `json:"omitted_by_filter"` // keys: filter param names; encoder sorts alphabetically
    Suggestion           string                  `json:"suggestion"`
}
```

Invariants (enforced by construction + unit tests):
- `OmittedByFilter` contains an entry only for active filters with ≥1 omission; values' fields always both present (0 allowed).
- `OmittedTotal == Σ (MissingAnnotation + Explicit)` and `MatchedBeforeFilters == OmittedTotal + len(filtered results)`.
- `Suggestion` built from compile-time constants over `[a-zA-Z0-9 .,:;()'_-]`, ≤200 chars, names only responsible filters.
- Serialized compact size ≤ 500 bytes at the maximal reachable fixture (SC-003 test).

## excludeReason (attribution function)

```go
// excludeReason mirrors shouldExclude exactly (shouldExclude delegates to it).
// Returns the FIRST failing filter (evaluation order: read_only_only →
// exclude_destructive → exclude_open_world), whether the decisive hint was
// explicit (vs missing), and whether the tool is excluded at all.
func excludeReason(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) (filterKey string, explicit bool, excluded bool)
```

Classification per decisive field (spec FR-004):

| filterKey | explicit=false (missing) | explicit=true |
|---|---|---|
| `read_only_only` | annotations nil OR ReadOnlyHint nil | `*ReadOnlyHint == false` |
| `exclude_destructive` | annotations nil OR DestructiveHint nil (read-only shortcut already passed the tool) | `*DestructiveHint == true` |
| `exclude_open_world` | annotations nil OR OpenWorldHint nil | `*OpenWorldHint == true` |

## filterByAnnotationsWithDiagnostics

```go
// Replaces the filterByAnnotations call inside handleRetrieveTools's
// annotationFilterActive branch. filterByAnnotations remains (delegating) for
// any other callers/tests.
func filterByAnnotationsWithDiagnostics(tools []annotatedSearchResult, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) (kept []annotatedSearchResult, diag *filterDiagnostics)
```

`diag` is fully populated (including suggestion selection by missing-cause precedence, FR-006); the handler attaches it iff `diag.OmittedTotal >= 1`.

## Suggestion selection (FR-006)

```
responsible := keys of OmittedByFilter (alphabetical, joined ", ")   // named ONCE per template
if Σ MissingAnnotation >= 1:
    "Tools matched but lack upstream annotations required by " + responsible +
    "; publish annotations or retry without these filters."
else:
    "Omitted tools are explicitly marked unsafe for " + responsible +
    "; retry without these filters to inspect them."
```

Worst case (all three filter names, 55 chars): missing-template = 55 + 108 = 163 chars; explicit-template = 55 + 94 = 149 chars — both ≤200. A table-driven test iterates **all 7 non-empty filter subsets × both templates** asserting length ≤200, charset conformance, and that no unrelated filter name appears.

## State transitions

None. No storage, no lifecycle, no events.
