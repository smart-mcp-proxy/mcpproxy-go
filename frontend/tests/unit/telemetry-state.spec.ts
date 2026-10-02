import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import type { TelemetryState } from '@/types'
import { lockedKeysChanged, telemetryNoticeMode, telemetryOffLine, telemetrySettingLock } from '@/utils/telemetryState'

// Spec 109 FR-044a. The same fixture drives the Go resolver test
// (internal/telemetry/effective_state_test.go) and the macOS XCTest
// (TelemetryNoticeTests), so the notice mode and the copy cannot drift.

type Case = {
  name: string
  want: TelemetryState
  notice: 'notice' | 'off_env' | 'hidden'
  off_line: string | null
  setting_lock: string | null
}
const cases = (
  JSON.parse(
    readFileSync(resolve(__dirname, '../../../internal/telemetry/testdata/effective_state_cases.json'), 'utf8')
  ) as { cases: Case[] }
).cases

describe('telemetry state helpers match the shared fixture', () => {
  it('has fixture cases', () => {
    expect(cases.length).toBeGreaterThan(5)
  })

  for (const c of cases) {
    it(c.name, () => {
      expect(telemetryNoticeMode(c.want)).toBe(c.notice)
      if (c.off_line !== null) {
        expect(telemetryOffLine(c.want)).toBe(c.off_line)
      }
      const lock = telemetrySettingLock(c.want)
      if (c.setting_lock === null) {
        expect(lock).toBeNull()
      } else {
        expect(lock).toEqual({ reason: c.setting_lock, value: false })
      }
    })
  }

  it('unknown state (older core, failed fetch) keeps the standard notice', () => {
    expect(telemetryNoticeMode(undefined)).toBe('notice')
    expect(telemetryNoticeMode(null)).toBe('notice')
    expect(telemetrySettingLock(undefined)).toBeNull()
  })
})

describe('lockedKeysChanged', () => {
  const locks = { 'telemetry.enabled': { reason: 'r' } }
  const stored = { telemetry: { enabled: true } }

  it('flags a changed locked key', () => {
    expect(lockedKeysChanged({ telemetry: { enabled: false } }, stored, locks)).toEqual(['telemetry.enabled'])
  })

  it('matches key names case-insensitively, as the backend decodes them', () => {
    expect(lockedKeysChanged({ Telemetry: { ENABLED: false } }, stored, locks)).toEqual(['telemetry.enabled'])
    expect(lockedKeysChanged({ TELEMETRY: { Enabled: true } }, stored, locks)).toEqual([])
  })

  it('catches a miscased duplicate that overrides an unchanged lowercase key', () => {
    const doc = JSON.parse('{"telemetry":{"enabled":true},"Telemetry":{"Enabled":false}}')
    expect(lockedKeysChanged(doc, stored, locks)).toEqual(['telemetry.enabled'])
  })

  // The backend round-trips the document through a map (sorted keys), so the
  // winner among case variants is decided by byte order, not document order.
  // Here the document-order "last" key matches the stored value but the sorted
  // winner ("telemetry" > "Telemetry") would turn telemetry off.
  it('refuses case-variant duplicates even when the document-order last one matches the stored value', () => {
    const doc = JSON.parse('{"telemetry":{"enabled":false},"Telemetry":{"enabled":true}}')
    expect(lockedKeysChanged(doc, stored, locks)).toEqual(['telemetry.enabled'])
    const leaf = JSON.parse('{"telemetry":{"enabled":false,"Enabled":true}}')
    expect(lockedKeysChanged(leaf, stored, locks)).toEqual(['telemetry.enabled'])
  })

  it('leaves an unchanged document alone', () => {
    expect(lockedKeysChanged({ telemetry: { enabled: true }, listen: 'x' }, stored, locks)).toEqual([])
  })
})
