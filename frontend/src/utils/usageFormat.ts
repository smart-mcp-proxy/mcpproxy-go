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
