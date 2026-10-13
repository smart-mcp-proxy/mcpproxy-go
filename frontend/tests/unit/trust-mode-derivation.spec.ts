import { describe, it, expect } from 'vitest'
import {
  effectiveTrustMode,
  deriveTrustModeState,
  TRUST_MODES,
  type TrustMode,
} from '@/utils/trustMode'

// Spec 088 (FR-001/FR-002/FR-003, research D7): the Web UI mirrors the backend
// fail-closed rule `ServerConfig.EffectiveTrustMode()` (internal/config/config.go)
// for DISPLAY only. The raw configured value may be absent (unset), a migrated
// legacy value (indistinguishable from an explicit one — no provenance labeling),
// or an invalid hand-edited string. Matching is case-sensitive and untrimmed,
// exactly like the Go accessor, so "Scan" / " scan " fail closed to manual.

interface DerivationCase {
  name: string
  raw: string | undefined
  effective: TrustMode
  isDefault: boolean
  isInvalid: boolean
}

const cases: DerivationCase[] = [
  { name: 'unset (field omitted)', raw: undefined, effective: 'manual', isDefault: true, isInvalid: false },
  { name: 'empty string', raw: '', effective: 'manual', isDefault: true, isInvalid: false },
  { name: 'explicit auto', raw: 'auto', effective: 'auto', isDefault: false, isInvalid: false },
  { name: 'explicit scan', raw: 'scan', effective: 'scan', isDefault: false, isInvalid: false },
  { name: 'explicit manual', raw: 'manual', effective: 'manual', isDefault: false, isInvalid: false },
  { name: 'unrecognized value', raw: 'bogus', effective: 'manual', isDefault: false, isInvalid: true },
  { name: 'wrong case is invalid', raw: 'Scan', effective: 'manual', isDefault: false, isInvalid: true },
  { name: 'untrimmed value is invalid', raw: ' scan ', effective: 'manual', isDefault: false, isInvalid: true },
]

describe('effectiveTrustMode (spec 088 FR-001)', () => {
  it.each(cases)('$name → $effective', ({ raw, effective }) => {
    expect(effectiveTrustMode(raw)).toBe(effective)
  })

  it('never returns a value outside the three recognized modes', () => {
    for (const raw of [undefined, '', 'auto', 'scan', 'manual', 'bogus', 'Scan', 'AUTO', 'off']) {
      expect(['auto', 'scan', 'manual']).toContain(effectiveTrustMode(raw))
    }
  })
})

describe('deriveTrustModeState (spec 088 FR-001)', () => {
  it.each(cases)('$name → effective=$effective isDefault=$isDefault isInvalid=$isInvalid', (c) => {
    const state = deriveTrustModeState(c.raw)
    expect(state.effective).toBe(c.effective)
    expect(state.isDefault).toBe(c.isDefault)
    expect(state.isInvalid).toBe(c.isInvalid)
  })

  it('preserves the raw configured value verbatim so the UI can show it alongside the effective mode', () => {
    // US1 scenario 4: a hand-edited "bogus" must be shown as-is, never hidden
    // or silently rewritten.
    expect(deriveTrustModeState('bogus').raw).toBe('bogus')
    expect(deriveTrustModeState('Scan').raw).toBe('Scan')
    expect(deriveTrustModeState('scan').raw).toBe('scan')
    expect(deriveTrustModeState(undefined).raw).toBeUndefined()
    expect(deriveTrustModeState('').raw).toBe('')
  })

  it('never marks a value as both default and invalid', () => {
    for (const raw of [undefined, '', 'auto', 'scan', 'manual', 'bogus', 'Scan']) {
      const state = deriveTrustModeState(raw)
      expect(state.isDefault && state.isInvalid).toBe(false)
    }
  })
})

describe('TRUST_MODES metadata table (spec 088 FR-002/FR-003)', () => {
  it('offers exactly the three recognized modes in selector order', () => {
    expect(TRUST_MODES.map((m) => m.mode)).toEqual(['auto', 'scan', 'manual'])
  })

  it('gives every mode a non-empty label, description and admission note', () => {
    for (const meta of TRUST_MODES) {
      expect(meta.label.trim().length).toBeGreaterThan(0)
      expect(meta.description.trim().length).toBeGreaterThan(0)
      expect(meta.admissionNote.trim().length).toBeGreaterThan(0)
    }
  })

  it('warns on selection for auto only (least-safe mode, FR-003)', () => {
    const warning = TRUST_MODES.filter((m) => m.warnsOnSelect).map((m) => m.mode)
    expect(warning).toEqual(['auto'])
  })

  // FR-002: the copy must cover BOTH behaviors each mode governs — tool changes
  // AND new-server admission — so the operator is never surprised at add time.
  const copyCases: Array<{
    mode: TrustMode
    descriptionIncludes: RegExp[]
    admissionIncludes: RegExp[]
  }> = [
    {
      mode: 'auto',
      // tool changes trusted without scanning (rug-pull risk)
      descriptionIncludes: [/chang/, /trust/, /without/, /scan/],
      // admitted without quarantine or scan at add time
      admissionIncludes: [/add/, /quarantin/, /scan/, /\bno\b|without/],
    },
    {
      mode: 'scan',
      // auto-approved only on a clean offline scan verdict, otherwise held
      descriptionIncludes: [/chang/, /clean/, /scan/, /held|hold/],
      // quarantined on add with a fail-closed automatic scan
      admissionIncludes: [/add/, /quarantin/, /scan/],
    },
    {
      mode: 'manual',
      // every change held
      descriptionIncludes: [/chang/, /every/, /held|hold/],
      // quarantined on add for human review (secure default)
      admissionIncludes: [/add/, /quarantin/, /review|approv/],
    },
  ]

  it.each(copyCases)('$mode description covers tool-change behavior', ({ mode, descriptionIncludes }) => {
    const meta = TRUST_MODES.find((m) => m.mode === mode)
    expect(meta).toBeDefined()
    const text = meta!.description.toLowerCase()
    for (const pattern of descriptionIncludes) {
      expect(text).toMatch(pattern)
    }
  })

  it.each(copyCases)('$mode admission note covers new-server admission behavior', ({ mode, admissionIncludes }) => {
    const meta = TRUST_MODES.find((m) => m.mode === mode)
    expect(meta).toBeDefined()
    const text = meta!.admissionNote.toLowerCase()
    for (const pattern of admissionIncludes) {
      expect(text).toMatch(pattern)
    }
  })

  it('gives each mode distinct copy (no placeholder duplication)', () => {
    const descriptions = new Set(TRUST_MODES.map((m) => m.description))
    const admissions = new Set(TRUST_MODES.map((m) => m.admissionNote))
    const labels = new Set(TRUST_MODES.map((m) => m.label))
    expect(descriptions.size).toBe(TRUST_MODES.length)
    expect(admissions.size).toBe(TRUST_MODES.length)
    expect(labels.size).toBe(TRUST_MODES.length)
  })

  it('never recommends the deprecated skip_quarantine setting (FR-021/SC-006)', () => {
    for (const meta of TRUST_MODES) {
      const blob = `${meta.label} ${meta.description} ${meta.admissionNote}`.toLowerCase()
      expect(blob).not.toContain('skip_quarantine')
      expect(blob).not.toContain('auto_approve_tool_changes')
    }
  })

  it('covers every mode effectiveTrustMode can return', () => {
    for (const raw of [undefined, '', 'auto', 'scan', 'manual', 'bogus']) {
      const mode = effectiveTrustMode(raw)
      expect(TRUST_MODES.some((m) => m.mode === mode)).toBe(true)
    }
  })
})
