// Shared formatting helpers + colour palette for the Usage graphs (Spec 069 B2).
// Kept dependency-free so the usage chart components can import them without
// pulling in Dashboard internals.

/** Compact number: 1234 -> "1.2K", 2_500_000 -> "2.5M". */
export function formatNumber(num: number): string {
  if (!Number.isFinite(num)) return '0'
  if (Math.abs(num) >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`
  if (Math.abs(num) >= 1_000) return `${(num / 1_000).toFixed(1)}K`
  return String(num)
}

/** Human byte size: 0 -> "0 B", 2048 -> "2.0 KB". */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = bytes
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

/** Latency in ms: 0 -> "0 ms", 1500 -> "1.5 s". */
export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms <= 0) return '0 ms'
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`
  return `${Math.round(ms)} ms`
}

/**
 * A latency PERCENTILE, which is not a measurement.
 *
 * p50/p95 are read off a fixed histogram, so the number is the upper bound of
 * the bucket the percentile fell in. Printed bare, every sub-10ms tool reported
 * exactly "10 ms" — the old first bucket bound — while the Activity Log showed
 * 3/4/5 ms for those same calls (audit finding F22, #1046). The bounds now
 * resolve the sub-10ms band, and the value is rendered AS a bound so it can
 * never be read as a stopwatch reading again.
 *
 * `exceeds` marks the overflow bucket, where the histogram has no upper bound
 * and the value is a floor instead: "> 10 s", not "≤ 10 s".
 */
export function formatLatencyBound(
  ms: number | null | undefined,
  exceeds = false
): string {
  if (ms == null || !Number.isFinite(ms) || ms <= 0) return '—'
  return `${exceeds ? '>' : '≤'} ${formatLatency(ms)}`
}

/** The window's headline counts, as the Usage tiles print them. */
export interface UsageHeadline {
  calls: number
  errors: number
  /** Percentage, one decimal, as a string — "0.0" when the window is empty. */
  errorRate: string
}

/**
 * Headline counts for the selected window.
 *
 * These come from the response, not from summing `tools`. Summing `tools`
 * client-side was the Usage half of audit finding F1 (#1046): that list is
 * lifetime-cumulative (the window only filters which tools appear), counts
 * upstream tools only (so mcpproxy's own retrieve_tools and describe_tool calls
 * vanished), and is truncated to top-N (so a high-cardinality log silently lost
 * the tail). The result was a "Tool calls" tile that disagreed with the
 * "Activity over time" chart printed directly beneath it AND with the Activity
 * Log. `total_calls` / `total_errors` are the sum of that same chart.
 */
export function usageHeadline(
  data?: { total_calls?: number; total_errors?: number } | null
): UsageHeadline {
  const calls = data?.total_calls ?? 0
  const errors = data?.total_errors ?? 0
  return {
    calls,
    errors,
    errorRate: calls > 0 ? ((errors / calls) * 100).toFixed(1) : '0.0',
  }
}

// --- unresolved tools ---------------------------------------------------------
//
// "Calls per tool" and "Token sinks" listed `everything:doesnotexist` and
// `broken-remote:whatever` as first-class tools with 100% error rates (audit
// finding F22, #1046). Those names came from FAILED calls: a typo the agent
// made, and a server that was never reachable. No such tool exists on any
// upstream, and charting them invents a catalog out of the agent's mistakes.
//
// The aggregate cannot ask the index whether a name resolves — it folds records
// as they arrive — but the record set answers it well enough: a tool that has
// never once completed a call is not something this proxy can vouch for. It
// belongs where failures belong (the Errors chart), not in a ranking of what
// your agents use or what is costing you tokens. If it ever succeeds, it
// reappears on its own.

/** The minimum a tool row needs for the never-completed test. */
export interface UsageToolCompletion {
  calls: number
  errors: number
}

/**
 * True when nothing this tool was asked for ever came back: every recorded call
 * failed, or there were no executed calls at all (only blocked or shed
 * attempts).
 */
export function neverCompleted(tool: UsageToolCompletion): boolean {
  return (tool.calls ?? 0) - (tool.errors ?? 0) <= 0
}

/**
 * Split a tool list into the tools that have completed at least one call and
 * the ones that never have. Charts about USE take the first; the errors chart
 * keeps both, because a tool failing 100% of the time is exactly its subject.
 */
export function partitionUsageTools<T extends UsageToolCompletion>(
  tools: T[]
): { completed: T[]; unresolved: T[] } {
  const completed: T[] = []
  const unresolved: T[] = []
  for (const tool of tools) {
    if (neverCompleted(tool)) unresolved.push(tool)
    else completed.push(tool)
  }
  return { completed, unresolved }
}

/** A short, readable label for a (server, tool) pair. */
export function toolLabel(server: string, tool: string): string {
  return `${server}:${tool}`
}

/** Stable, colour-blind-friendly palette shared across the usage charts. */
export const USAGE_PALETTE = [
  '#3b82f6', '#10b981', '#f59e0b', '#ec4899', '#8b5cf6',
  '#06b6d4', '#ef4444', '#14b8a6', '#f97316', '#a855f7',
  '#6366f1', '#84cc16', '#f43f5e', '#0ea5e9', '#22c55e', '#eab308',
]

export function paletteColor(index: number): string {
  return USAGE_PALETTE[index % USAGE_PALETTE.length]
}
