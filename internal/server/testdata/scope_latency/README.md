# Spec 105 PR H1 — scope latency fixtures (FR-011, T112/T112a)

The four FR-011 operations' "frozen" fixtures:

- **`retrieve_tools` / `tools/list`**: the existing 527-tool LiveMCPBench
  snapshot at `../mcp_routing_deferred_tokens_test.go`'s
  `deferredLargeCorpusPath` (shared with the Spec 102/083 fleet-scale
  tests — not duplicated here).
- **`read_cache`**: a 10-page cache entry.
- **`prompts/list`**: a 50-prompt set.

The `read_cache` and `prompts/list` fixtures are generated **deterministically
in Go code** inside `../scope_latency_test.go` (`recordID`/`itoaPadded`
helpers), not as separate serialized files in this directory. This is a
deliberate choice, not an oversight: `.github/workflows/scope-latency.yml`
(T112a) copies HEAD's `scope_latency_test.go` (and this directory, if it
holds any files) over the merge-base checkout before comparing both
revisions, so a fixture defined in the test file's own source is carried
across the comparison exactly as a serialized file under this directory
would be — with the added benefit that there is no risk of the code and a
separate data file drifting apart. Any future PR that DOES add a serialized
fixture under this directory (e.g. a captured real LiveMCPBench prompt
export) should keep it here so the same copy-across-revisions mechanism
picks it up unchanged.
