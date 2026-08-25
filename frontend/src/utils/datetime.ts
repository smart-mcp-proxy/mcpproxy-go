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

/** `2026-08-25`, optionally followed by a time — the ISO shapes the API emits. */
const ISO_DATE_PART = /^(\d{4})-(\d{2})-(\d{2})(?:$|[T ])/

/** Does `YYYY-MM-DD` name a day that exists? `2026-02-31` does not. */
function isRealCalendarDate(year: number, month: number, day: number): boolean {
  if (month < 1 || month > 12 || day < 1) return false
  const leap = (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0
  const lengths = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  return day <= lengths[month - 1]
}

function toDate(value: string | number | Date | null | undefined): Date | null {
  if (value === null || value === undefined || value === '') return null
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value
  if (typeof value === 'string') {
    const text = value.trim()
    const m = ISO_DATE_PART.exec(text)
    if (m) {
      const year = Number(m[1])
      const month = Number(m[2])
      const day = Number(m[3])
      // `new Date('2026-02-31T12:00:00')` silently rolls over into March. An
      // impossible day is bad input, not the 3rd of the next month.
      if (!isRealCalendarDate(year, month, day)) return null
      // A date-only string is a calendar date, not an instant: parsing it as UTC
      // and printing it with local getters shifts it a day west of UTC.
      if (text.length === 10) {
        const d = new Date(year, month - 1, day)
        return Number.isNaN(d.getTime()) ? null : d
      }
    }
  }
  const d = new Date(value)
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
