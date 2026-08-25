/**
 * One date/time format for the whole UI (UX audit F35).
 */
import { describe, it, expect } from 'vitest'
import {
  DATE_TIME_FORMAT_HINT,
  formatDate,
  formatDateTime,
  formatDateTimeShort,
  formatRelative,
  formatTime,
} from '@/utils/datetime'

// Built from local parts so the assertions hold in any CI timezone.
const sample = new Date(2026, 7, 25, 6, 51, 38)

describe('house date/time format', () => {
  it('prints the CLI-compatible stamp, not a locale-dependent one', () => {
    expect(formatDateTime(sample)).toBe('2026-08-25 06:51:38')
    expect(formatDateTimeShort(sample)).toBe('2026-08-25 06:51')
    expect(formatDate(sample)).toBe('2026-08-25')
    expect(formatTime(sample)).toBe('06:51:38')
  })

  it('is zero-padded and 24-hour', () => {
    const afternoon = new Date(2026, 0, 3, 14, 5, 9)
    expect(formatDateTime(afternoon)).toBe('2026-01-03 14:05:09')
    const midnight = new Date(2026, 11, 31, 0, 0, 0)
    expect(formatDateTime(midnight)).toBe('2026-12-31 00:00:00')
  })

  it('accepts the ISO strings the API returns', () => {
    const iso = new Date(2026, 7, 25, 6, 51, 38).toISOString()
    expect(formatDateTime(iso)).toBe('2026-08-25 06:51:38')
  })

  it('treats a date-only string as a calendar date, not UTC midnight', () => {
    // `new Date('2026-08-25')` is UTC midnight, which prints as the 24th in any
    // timezone west of UTC.
    expect(formatDate('2026-08-25')).toBe('2026-08-25')
    expect(formatDateTime('2026-08-25')).toBe('2026-08-25 00:00:00')
  })

  it('rejects an impossible date instead of rolling it over', () => {
    // `new Date(2026, 1, 31)` is the 3rd of March, which is not what the caller
    // was handed.
    expect(formatDate('2026-02-31')).toBe('-')
    expect(formatDate('2026-13-01')).toBe('-')
    expect(formatDate('2026-02-28')).toBe('2026-02-28')
    expect(formatDate('2028-02-29')).toBe('2028-02-29')
    // 2100 is not a leap year; 2000 was.
    expect(formatDate('2100-02-29')).toBe('-')
    expect(formatDate('2000-02-29')).toBe('2000-02-29')
    // …and the same applies once a time is attached.
    expect(formatDateTime('2026-02-31T12:00:00')).toBe('-')
    expect(formatDateTime('2026-04-31T12:00:00Z')).toBe('-')
    expect(formatDateTime('2026-02-28T12:00:00')).toBe('2026-02-28 12:00:00')
  })

  it('never renders "Invalid Date" for missing or broken input', () => {
    for (const value of [null, undefined, '', 'not-a-date']) {
      expect(formatDateTime(value)).toBe('-')
      expect(formatDate(value)).toBe('-')
      expect(formatTime(value)).toBe('-')
      expect(formatDateTimeShort(value)).toBe('-')
    }
    // Callers that prefer to echo the raw value can pass their own fallback.
    expect(formatDateTime('not-a-date', 'not-a-date')).toBe('not-a-date')
  })

  it('formats relative times for the secondary line', () => {
    const now = new Date(2026, 7, 25, 12, 0, 0)
    expect(formatRelative(new Date(2026, 7, 25, 11, 59, 50), now)).toBe('just now')
    expect(formatRelative(new Date(2026, 7, 25, 11, 55, 0), now)).toBe('5m ago')
    expect(formatRelative(new Date(2026, 7, 25, 9, 0, 0), now)).toBe('3h ago')
    expect(formatRelative(new Date(2026, 7, 23, 12, 0, 0), now)).toBe('2d ago')
    expect(formatRelative(null, now)).toBe('-')
  })

  it('states the same order in the native-input hint', () => {
    expect(DATE_TIME_FORMAT_HINT).toContain('YYYY-MM-DD')
  })
})
