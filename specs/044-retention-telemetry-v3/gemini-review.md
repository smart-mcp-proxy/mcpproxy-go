# Gemini Cross-Review — Spec 044 (Diagnostics Taxonomy + Retention Telemetry v3)

**Tool**: Gemini CLI v0.38.1 (`@google/gemini-cli`)
**Model**: `gemini-3.1-pro-preview`
**Mode**: `--yolo` (auto-approve tool calls), non-interactive headless
**Date**: 2026-04-24
**Invocation**:

```bash
cat /tmp/gemini-review-prompt.md | gemini --yolo \
  --model gemini-3.1-pro-preview \
  --include-directories /Users/user/repos/mcpproxy-go-diagnostics-taxonomy,\
/Users/user/repos/mcpproxy-go-retention-telemetry,\
/Users/user/repos/mcpproxy-telemetry,\
/Users/user/repos/mcpproxy-dash \
  -p "Please execute the review. You now have read access to all 4 repos via --include-directories. READ the actual source files before critiquing — do not speculate about contents."
```

**Repos reviewed** (read-only, via `--include-directories`):
- `mcpproxy-go-diagnostics-taxonomy` (branch `feat/diagnostics-taxonomy`, PR #400)
- `mcpproxy-go-retention-telemetry` (branch `044-retention-telemetry-v3`, PR #401)
- `mcpproxy-telemetry` (Cloudflare Worker, PR #1 merged)
- `mcpproxy-dash` (SvelteKit dashboard, PR #3)

**Notes**: The first invocation (without `--include-directories`) was refused at the file-read level (Gemini's workspace sandbox rejected paths outside the cwd), producing a speculative review. This committed version is the second, grounded run where Gemini read the actual source files — line citations are from real code. Both initial attempts hit transient 429 "MODEL_CAPACITY_EXHAUSTED" on the cloudcode-pa backend but succeeded after built-in exponential backoff.

The review content below is verbatim Gemini output, unedited.

---

## Critical findings (P1 — must fix before merge)
- Finding: **Vue ErrorPanel hides the execute button and forces dry-run for non-destructive actions.**
  - Evidence: `/Users/user/repos/mcpproxy-go-diagnostics-taxonomy/frontend/src/components/diagnostics/ErrorPanel.vue:59-79`
  - Why it matters: The primary button always renders as "Preview (dry-run)" and sends `mode='dry_run'`, while the "Execute" button is hidden via `v-if="step.destructive"`. Users literally cannot execute non-destructive fixes (like "Show last server log lines" or checking DNS). Clicking the only available button merely triggers a preview toast.
  - Suggested fix: Change the primary button to be the explicit execute action when `!step.destructive`, omitting the dry-run/preview concept entirely for those steps. Remove the `v-if` gate on the actual execution path.

- Finding: **`env_kind` decision tree misclassifies real-human Cloud IDEs as CI.**
  - Evidence: `/Users/user/repos/mcpproxy-go-retention-telemetry/internal/telemetry/env_kind.go:132-135`
  - Why it matters: The `switch` checks `case markers.HasCIEnv:` before `case markers.HasCloudIDEEnv:`. Environments like GitHub Codespaces and Gitpod routinely set `CI=true`. Real humans using these interactive cloud sessions will be misclassified as `ci` (bots), artificially deflating the Cloud IDE retention numbers and skewing the funnel.
  - Suggested fix: Reorder the decision tree so `HasCloudIDEEnv` is evaluated before `HasCIEnv`.

- Finding: **Activation funnel query drops users who previously configured/connected a server but later deleted it.**
  - Evidence: `/Users/user/repos/mcpproxy-dash/src/lib/server/queries/activation-funnel.ts:146-160`
  - Why it matters: The query isolates the *latest* heartbeat per `anonymous_id` and computes `server_configured` by checking if `h.server_count > 0` on that single row. If a user configured a server on day 1 but deleted it by day 5, their latest heartbeat has `server_count = 0`. They incorrectly fall out of the lifetime funnel, which fundamentally undercounts true historical activation.
  - Suggested fix: Query `MAX(server_count) > 0` and `MAX(connected_server_count) > 0` grouped by `anonymous_id` across *all* heartbeats instead of relying exclusively on the point-in-time state of the latest row.

- Finding: **Autostart reader caches a missing sidecar file as `nil` for an hour, poisoning the critical first heartbeat.**
  - Evidence: `/Users/user/repos/mcpproxy-go-retention-telemetry/internal/telemetry/autostart.go:88-92`
  - Why it matters: If the core process starts milliseconds before the tray process creates the `tray-autostart.json` file (a common race condition on boot), `os.ReadFile` returns `fs.ErrNotExist`. The code immediately sets `r.cachedOnce = true` and caches `nil`. Telemetry will falsely report `autostart_enabled: null` for the entire first hour of the user's session.
  - Suggested fix: Do not set `r.cachedOnce = true` on `fs.ErrNotExist`. Return `nil` but allow subsequent heartbeats (or a much shorter retry TTL) to attempt reading the file again once the tray has settled.

## Important findings (P2 — should fix in a follow-up)
- Finding: **The 409 Conflict logic for missing explicit execute modes on destructive fixes is unreachable dead code.**
  - Evidence: `/Users/user/repos/mcpproxy-go-diagnostics-taxonomy/internal/httpapi/diagnostics_fix.go:82-96`
  - Why it matters: The API attempts to block destructive execution when `mode=""` by returning a 409. However, the block `if mode == ""` explicitly defaults `mode = ModeDryRun` when `step.Destructive` is true. The subsequent check `if step.Destructive && mode == ModeExecute` can therefore never evaluate to true. The server silently performs a dry run instead of returning the intended conflict, masking client-side implementation errors.
  - Suggested fix: If the intent is a safe default to `dry_run`, remove the 409 check entirely. If the intent is to force explicit intent, return the 409 immediately if `step.Destructive && body.Mode == ""`.

- Finding: **macOS tray diagnostic badge relies exclusively on color to convey severity.**
  - Evidence: `/Users/user/repos/mcpproxy-go-diagnostics-taxonomy/native/macos/MCPProxy/MCPProxy/Menu/TrayIcon.swift:39-44`
  - Why it matters: Distinguishing an Error (`.red`) from a Warning (`.orange`) using only a `Circle().fill(...)` fails basic accessibility guidelines. Red-green colorblind users cannot easily tell if the proxy is in a terminal failure state or just warning them about a deprecated config.
  - Suggested fix: Overlay distinct SF Symbols (e.g., `xmark.circle.fill` vs `exclamationmark.triangle.fill`) in the badge instead of relying on a solid circle of color.

- Finding: **Tokens-saved estimator metric is fundamentally disconnected from actual LLM context savings.**
  - Evidence: `/Users/user/repos/mcpproxy-go-retention-telemetry/internal/server/mcp.go:1137-1138`
  - Why it matters: The estimator calculates savings *only* when the `retrieve_tools` discovery call is made. But mcpproxy's real value is omitting those schemas from *every subsequent conversational turn* the LLM takes. By only counting the discovery call, the metric drastically underestimates true context savings and acts as an arbitrary, misleading vanity number.
  - Suggested fix: Either rename the metric to clarify it measures "schema tokens omitted during discovery queries", or drop it entirely, as mcpproxy is blind to LLM conversation turns and cannot accurately estimate true context savings.

- Finding: **Classifier free-text string matching is brittle and masks upstream error-wrapping regressions.**
  - Evidence: `/Users/user/repos/mcpproxy-go-diagnostics-taxonomy/internal/diagnostics/classifier.go:121-131`
  - Why it matters: Falling back to `strings.Contains(lmsg, "no such file or directory")` will fail on non-English locales (e.g., French `Aucun fichier ou dossier de ce type`) or alternative libc implementations, causing legitimate stdio errors to silently drop into `UnknownUnclassified`.
  - Suggested fix: Fix the upstream manager to use `%w` (error wrapping) instead of string formatting (`%v`) for the raw `exec.Error` so `errors.Is(err, syscall.ENOENT)` reliably catches the failure across all localizations.

## Nice-to-have / polish (P3)
- Finding: Rate limiter for fixes is purely in-memory and resets on process restart. While an attacker *could* write a loop to bounce the process and bypass the 1/s limit, the limit exists primarily to prevent UI spam/accidental clicks rather than hard security, making it acceptable for a local client.
- Finding: The diagnostics catalog (`registry.go`) is currently missing several key failure states such as TLS handshake timeouts, TLS protocol version mismatches, Container OOM kills, and `OAuthTokenRevoked` explicitly (though 403 covers the latter). Adding these would deepen the taxonomy's coverage.

## Things we got right
- The `ciFilterClause` migration to `json_each(?)` elegantly avoids blowing up Cloudflare D1's per-statement bound-parameter budget while securely passing the CI exclusion list.
- Validating v3 payloads defensively by checking for boolean leaks in `env_markers` effectively stops accidental PII exposure from rogue client modifications.
- Ground-truth `env_kind` logic correctly caches its decision at process startup (instead of recalculating per heartbeat), efficiently saving CPU and disk I/O.
- Using BBolt transactional updates for the monotonic `first_*_ever` activation flags guarantees that first-run metrics can never flip back to false, even under concurrent load.

## Open questions for the authors
- If the token estimator is only ever calculated when `retrieve_tools` is actively called, are you comfortable with it missing the massive cumulative savings from the unseen conversational turns that follow?
- Why does the `httpapi` diagnostics handler attempt to enforce an explicit `execute` mode via a 409 conflict while simultaneously documenting (and implementing) a safe fallback to `dry_run`? Pick one contract paradigm and stick to it.
