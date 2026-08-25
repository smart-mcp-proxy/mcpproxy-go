/**
 * One date/time format for the whole Web UI.
 *
 * The house format is the one the CLI and the log files already use —
 * `YYYY-MM-DD HH:mm:ss`, 24-hour, in the viewer's local timezone
 * (see `cmd/mcpproxy/*_cmd.go`, which formats with Go's "2006-01-02 15:04:05").
 *
 * `toLocaleString()` was previously called ad hoc in a dozen components, so the
 * same screen could print `8/25/2026, 6:51:38 AM` in a table next to a
 * `dd/mm/yyyy` native date input. An ISO-ordered, locale-independent stamp is
 * unambiguous for every reader and sorts the way it reads.
 */

const pad = (n: number, width = 2) => String(n).padStart(width, '0')

function toDate(value: string | number | Date | null | undefined): Date | null {
  if (value === null || value === undefined || value === '') return null
  const d = value instanceof Date ? value : new Date(value)
  return Number.isNaN(d.getTime()) ? null : d
}

/** `2026-08-25` — local date. */
export function formatDate(value: string | number | Date | null | undefined, fallback = '-'): string {
  const d = toDate(value)
  if (!d) return fallback
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** `06:51:38` — local wall-clock time. */
export function formatTime(value: string | number | Date | null | undefined, fallback = '-'): string {
  const d = toDate(value)
  if (!d) return fallback
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** `2026-08-25 06:51` — the compact stamp for dense tables. */
export function formatDateTimeShort(
  value: string | number | Date | null | undefined,
  fallback = '-',
): string {
  const d = toDate(value)
  if (!d) return fallback
  return `${formatDate(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** `2026-08-25 06:51:38` — the house stamp. */
export function formatDateTime(
  value: string | number | Date | null | undefined,
  fallback = '-',
): string {
  const d = toDate(value)
  if (!d) return fallback
  return `${formatDate(d)} ${formatTime(d)}`
}

/** `just now` / `5m ago` / `3h ago` / `2d ago` — for the secondary line. */
export function formatRelative(
  value: string | number | Date | null | undefined,
  now: Date = new Date(),
  fallback = '-',
): string {
  const d = toDate(value)
  if (!d) return fallback
  const seconds = Math.round((now.getTime() - d.getTime()) / 1000)
  if (seconds < 0) return 'in the future'
  if (seconds < 45) return 'just now'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.round(hours / 24)
  return `${days}d ago`
}

/** The format hint shown next to native date/time inputs, which render in OS locale. */
export const DATE_TIME_FORMAT_HINT = 'YYYY-MM-DD HH:mm (local time)'
