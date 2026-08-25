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
