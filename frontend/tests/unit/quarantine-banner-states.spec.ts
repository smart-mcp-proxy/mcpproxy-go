import { describe, it, expect } from 'vitest'
import {
  deriveQuarantineBannerState,
  type QuarantineBannerAction,
  type QuarantineBannerServer,
  type QuarantineBannerStateName,
} from '@/utils/quarantineBanner'

// Spec 088 US3 (FR-013/FR-014/FR-015, research D2, data-model "QuarantineBannerState").
//
// The banner distinguishes four situations using ONLY facts the server payload
// already carries — `trust_mode` (fail-closed effective value), `quarantined`
// and the optional `security_scan` summary. Priority order, first match wins:
//
//   1. scan-running   quarantined && effective==scan && status=='scanning'
//   2. scan-failed    quarantined && status=='failed'            (any mode)
//   3. scan-blocked   quarantined && effective==scan && status in dangerous|warnings
//   4. manual-review  every other quarantined server
//
// Two facts are server-internal and must NEVER leak into the copy:
//   - admission-window provenance (was this the admission scan or a later
//     re-scan? `HasApprovalBaseline` is not exposed to the page), and
//   - eligibility for automatic approval (the page cannot promise it).
//
// "No scan has ever run" is signalled by the ABSENCE of `security_scan` — the
// backend omits the field entirely when `GetScanSummary` returns nil — so the
// Run-scan CTA keys on absence, never on a `not_scanned` status string (which
// the REST payload does not normally emit; it is only tolerated as an alias).

type Status = string

function withScan(status: Status, extra: Record<string, unknown> = {}) {
  return {
    status,
    risk_score: 0,
    finding_counts: { dangerous: 0, warning: 0, info: 0, total: 0 },
    ...extra,
  } as QuarantineBannerServer['security_scan']
}

function quarantined(overrides: Partial<QuarantineBannerServer> = {}): QuarantineBannerServer {
  return { quarantined: true, ...overrides }
}

describe('deriveQuarantineBannerState — not quarantined (spec 088 FR-013)', () => {
  it('returns null for a server that is not quarantined, whatever its scan says', () => {
    const scans = [undefined, withScan('scanning'), withScan('failed'), withScan('dangerous'), withScan('clean')]
    for (const security_scan of scans) {
      expect(deriveQuarantineBannerState({ quarantined: false, trust_mode: 'scan', security_scan })).toBeNull()
      expect(deriveQuarantineBannerState({ quarantined: false, trust_mode: 'manual', security_scan })).toBeNull()
    }
  })

  it('returns null when the quarantine flag is missing or the server itself is absent', () => {
    expect(deriveQuarantineBannerState({})).toBeNull()
    expect(deriveQuarantineBannerState({ trust_mode: 'scan', security_scan: withScan('failed') })).toBeNull()
    expect(deriveQuarantineBannerState(null)).toBeNull()
    expect(deriveQuarantineBannerState(undefined)).toBeNull()
  })
})

describe('deriveQuarantineBannerState — derivation table (spec 088 FR-013, data-model)', () => {
  const cases: Array<{
    name: string
    server: QuarantineBannerServer
    state: QuarantineBannerStateName
  }> = [
    // 1. scan-running — scan mode only
    {
      name: 'scan mode, scan in progress',
      server: quarantined({ trust_mode: 'scan', security_scan: withScan('scanning') }),
      state: 'scan-running',
    },
    {
      name: 'manual mode with a scan in progress is NOT the scan-running state',
      server: quarantined({ trust_mode: 'manual', security_scan: withScan('scanning') }),
      state: 'manual-review',
    },
    {
      name: 'auto mode with a scan in progress is NOT the scan-running state',
      server: quarantined({ trust_mode: 'auto', security_scan: withScan('scanning') }),
      state: 'manual-review',
    },
    {
      name: 'unset trust mode (effective manual) with a scan in progress',
      server: quarantined({ security_scan: withScan('scanning') }),
      state: 'manual-review',
    },
    {
      name: 'invalid trust mode fails closed to manual, so a running scan is not scan-running',
      server: quarantined({ trust_mode: 'Scan', security_scan: withScan('scanning') }),
      state: 'manual-review',
    },
    // 2. scan-failed — every mode, outranks blocked and manual
    {
      name: 'scan mode, failed scan',
      server: quarantined({ trust_mode: 'scan', security_scan: withScan('failed') }),
      state: 'scan-failed',
    },
    {
      name: 'manual mode, failed scan (rule 2 is mode-independent)',
      server: quarantined({ trust_mode: 'manual', security_scan: withScan('failed') }),
      state: 'scan-failed',
    },
    {
      name: 'unset trust mode, failed scan',
      server: quarantined({ security_scan: withScan('failed') }),
      state: 'scan-failed',
    },
    {
      name: 'invalid trust mode, failed scan',
      server: quarantined({ trust_mode: 'bogus', security_scan: withScan('failed') }),
      state: 'scan-failed',
    },
    // 3. scan-blocked — scan mode + non-clean verdict
    {
      name: 'scan mode, dangerous verdict',
      server: quarantined({ trust_mode: 'scan', security_scan: withScan('dangerous', { risk_score: 90 }) }),
      state: 'scan-blocked',
    },
    {
      name: 'scan mode, warnings verdict',
      server: quarantined({ trust_mode: 'scan', security_scan: withScan('warnings', { risk_score: 40 }) }),
      state: 'scan-blocked',
    },
    {
      name: 'manual mode with a dangerous verdict awaits manual review (no scan gate to block)',
      server: quarantined({ trust_mode: 'manual', security_scan: withScan('dangerous') }),
      state: 'manual-review',
    },
    {
      name: 'auto mode with a warnings verdict awaits manual review',
      server: quarantined({ trust_mode: 'auto', security_scan: withScan('warnings') }),
      state: 'manual-review',
    },
    // 4. manual-review — everything else
    {
      name: 'scan mode, clean verdict but still quarantined',
      server: quarantined({ trust_mode: 'scan', security_scan: withScan('clean') }),
      state: 'manual-review',
    },
    {
      name: 'scan mode, no scan summary at all (field omitted)',
      server: quarantined({ trust_mode: 'scan' }),
      state: 'manual-review',
    },
    {
      name: 'manual mode, no scan summary at all',
      server: quarantined({ trust_mode: 'manual' }),
      state: 'manual-review',
    },
    {
      name: 'scan mode, unrecognized status from a newer backend',
      server: quarantined({ trust_mode: 'scan', security_scan: withScan('quantum') }),
      state: 'manual-review',
    },
  ]

  it.each(cases)('$name → $state', ({ server, state }) => {
    expect(deriveQuarantineBannerState(server)!.state).toBe(state)
  })

  it('only ever returns one of the four documented states', () => {
    const known: QuarantineBannerStateName[] = ['scan-running', 'scan-failed', 'scan-blocked', 'manual-review']
    for (const mode of [undefined, '', 'auto', 'scan', 'manual', 'Scan', 'bogus']) {
      for (const status of [undefined, 'scanning', 'failed', 'dangerous', 'warnings', 'clean', 'not_scanned', 'weird']) {
        const banner = deriveQuarantineBannerState(
          quarantined({ trust_mode: mode, security_scan: status ? withScan(status) : undefined }),
        )
        expect(banner).not.toBeNull()
        expect(known).toContain(banner!.state)
      }
    }
  })
})

describe('deriveQuarantineBannerState — actions (spec 088 FR-014/FR-015)', () => {
  const ALL: QuarantineBannerAction[] = ['approve', 'view-report', 'retry-scan', 'run-scan']

  function actionsFor(server: QuarantineBannerServer): QuarantineBannerAction[] {
    return deriveQuarantineBannerState(server)!.actions
  }

  it('offers no action while a scan is running — the result decides the next step', () => {
    expect(actionsFor(quarantined({ trust_mode: 'scan', security_scan: withScan('scanning') }))).toEqual([])
  })

  it('offers retry + manual approval on a failed scan, and no report link (a failed scan produced no verdict)', () => {
    const actions = actionsFor(quarantined({ trust_mode: 'scan', security_scan: withScan('failed') }))
    expect(actions).toContain('retry-scan')
    expect(actions).toContain('approve')
    expect(actions).not.toContain('view-report')
    expect(actions).not.toContain('run-scan')
  })

  it('offers the report and approval when a verdict blocked automatic approval', () => {
    const actions = actionsFor(quarantined({ trust_mode: 'scan', security_scan: withScan('dangerous') }))
    expect(actions).toContain('view-report')
    expect(actions).toContain('approve')
    expect(actions).not.toContain('run-scan')
    expect(actions).not.toContain('retry-scan')
  })

  it('offers a scan alongside approval when the payload carries NO security_scan field (FR-015)', () => {
    const actions = actionsFor(quarantined({ trust_mode: 'manual' }))
    expect(actions).toContain('run-scan')
    expect(actions).toContain('approve')
    expect(actions).not.toContain('view-report')
  })

  it('offers the report instead of a scan CTA once any verdict exists', () => {
    for (const status of ['clean', 'warnings', 'dangerous']) {
      const actions = actionsFor(quarantined({ trust_mode: 'manual', security_scan: withScan(status) }))
      expect(actions).toContain('view-report')
      expect(actions).toContain('approve')
      expect(actions).not.toContain('run-scan')
    }
  })

  it('offers approval only while a scan runs on a manual-mode server (no verdict yet, no CTA)', () => {
    expect(actionsFor(quarantined({ trust_mode: 'manual', security_scan: withScan('scanning') }))).toEqual(['approve'])
  })

  it('treats a literal not_scanned status as equivalent to the absent field', () => {
    const actions = actionsFor(quarantined({ trust_mode: 'manual', security_scan: withScan('not_scanned') }))
    expect(actions).toContain('run-scan')
    expect(actions).not.toContain('view-report')
  })

  it('never offers both running a scan and viewing a report, and never repeats an action', () => {
    for (const mode of [undefined, 'auto', 'scan', 'manual', 'bogus']) {
      for (const status of [undefined, 'scanning', 'failed', 'dangerous', 'warnings', 'clean', 'not_scanned', 'weird']) {
        const actions = actionsFor(
          quarantined({ trust_mode: mode, security_scan: status ? withScan(status) : undefined }),
        )
        for (const action of actions) expect(ALL).toContain(action)
        expect(new Set(actions).size).toBe(actions.length)
        expect(actions.includes('run-scan') && actions.includes('view-report')).toBe(false)
        expect(actions.includes('run-scan') && actions.includes('retry-scan')).toBe(false)
      }
    }
  })

  it('offers run-scan ONLY when no scan summary exists', () => {
    for (const mode of [undefined, 'auto', 'scan', 'manual']) {
      for (const status of ['scanning', 'failed', 'dangerous', 'warnings', 'clean', 'weird']) {
        expect(actionsFor(quarantined({ trust_mode: mode, security_scan: withScan(status) }))).not.toContain('run-scan')
      }
    }
  })
})

describe('deriveQuarantineBannerState — copy constraints (spec 088 FR-013)', () => {
  const fixtures: Array<{ label: string; server: QuarantineBannerServer }> = [
    { label: 'scan-running', server: quarantined({ trust_mode: 'scan', security_scan: withScan('scanning') }) },
    { label: 'scan-failed', server: quarantined({ trust_mode: 'scan', security_scan: withScan('failed') }) },
    { label: 'scan-blocked', server: quarantined({ trust_mode: 'scan', security_scan: withScan('dangerous') }) },
    { label: 'manual-review', server: quarantined({ trust_mode: 'manual', security_scan: withScan('clean') }) },
    { label: 'manual-review (no scan)', server: quarantined({ trust_mode: 'manual' }) },
  ]

  function copyOf(server: QuarantineBannerServer): string {
    const banner = deriveQuarantineBannerState(server)!
    return `${banner.headline} ${banner.detail}`.toLowerCase()
  }

  it.each(fixtures)('$label has a non-empty headline and detail', ({ server }) => {
    const banner = deriveQuarantineBannerState(server)!
    expect(banner.headline.trim().length).toBeGreaterThan(0)
    expect(banner.detail.trim().length).toBeGreaterThan(0)
  })

  // The page cannot see `HasApprovalBaseline`, so it must not claim whether this
  // was the admission scan or a later re-scan.
  it.each(fixtures)('$label never claims admission-window provenance', ({ server }) => {
    const copy = copyOf(server)
    for (const banned of [/admission/, /admitted/, /\badmit\b/, /original scan/, /first scan/, /when it was added/]) {
      expect(copy).not.toMatch(banned)
    }
  })

  // Eligibility for automatic approval is server-internal: never promise it.
  it.each(fixtures)('$label never promises automatic approval', ({ server }) => {
    const copy = copyOf(server)
    for (const banned of [
      /\bwill\b[^.]*\bapprov/,
      /\bshall\b[^.]*\bapprov/,
      /\bautomatically\s+approv/,
      /\bauto-?approve[sd]?\b/,
      /\bif\b[^.]*\bclean\b[^.]*\bapprov/,
      /\bwill be (released|admitted|unquarantined|allowed)/,
    ]) {
      expect(copy).not.toMatch(banned)
    }
  })

  it.each(fixtures)('$label never names a deprecated legacy config flag', ({ server }) => {
    const copy = copyOf(server)
    expect(copy).not.toContain('skip_quarantine')
    expect(copy).not.toContain('auto_approve_tool_changes')
  })

  it('gives every state distinct copy (no placeholder duplication)', () => {
    const headlines = fixtures.map((f) => deriveQuarantineBannerState(f.server)!.headline)
    const details = fixtures.map((f) => deriveQuarantineBannerState(f.server)!.detail)
    // Four states, plus a distinct manual-review variant when no scan has run.
    expect(new Set(headlines).size).toBe(4)
    expect(new Set(details).size).toBe(fixtures.length)
  })
})

describe('deriveQuarantineBannerState — per-state copy intent (spec 088 US3 scenarios)', () => {
  it('scenario 1: a running scan is announced neutrally, with its result deciding the next step', () => {
    const banner = deriveQuarantineBannerState(
      quarantined({ trust_mode: 'scan', security_scan: withScan('scanning') }),
    )!
    expect(banner.headline.toLowerCase()).toMatch(/scan/)
    expect(banner.headline.toLowerCase()).toMatch(/progress|running/)
    expect(banner.detail.toLowerCase()).toMatch(/result/)
    expect(banner.tone).toBe('info')
  })

  it('scenario 3: a failed scan reads as an incomplete scan held as a precaution, never as a threat verdict', () => {
    const banner = deriveQuarantineBannerState(
      quarantined({ trust_mode: 'scan', security_scan: withScan('failed') }),
    )!
    expect(banner.tone).toBe('precaution')
    expect(banner.tone).not.toBe('threat')
    expect(banner.headline.toLowerCase()).toMatch(/could not complete|did not complete/)
    // The headline must carry no threat vocabulary at all…
    for (const word of ['threat', 'danger', 'malicious', 'attack', 'unsafe', 'compromis']) {
      expect(banner.headline.toLowerCase()).not.toContain(word)
    }
    // …and the detail must explicitly disclaim a verdict and offer a retry.
    expect(banner.detail.toLowerCase()).toMatch(/not a threat verdict/)
    expect(banner.detail.toLowerCase()).toMatch(/precaution/)
    expect(banner.detail.toLowerCase()).toMatch(/retry|run it again/)
  })

  it('scenario 2: a non-clean verdict is described as blocking automatic approval, pointing at the findings', () => {
    const banner = deriveQuarantineBannerState(
      quarantined({ trust_mode: 'scan', security_scan: withScan('dangerous') }),
    )!
    const copy = `${banner.headline} ${banner.detail}`.toLowerCase()
    expect(copy).toMatch(/verdict/)
    expect(copy).toMatch(/automatic approval|blocked/)
    expect(copy).toMatch(/finding|report/)
    expect(banner.tone).toBe('threat')
  })

  it('scenario 2: a warnings verdict is not styled as hard as a dangerous one', () => {
    const banner = deriveQuarantineBannerState(
      quarantined({ trust_mode: 'scan', security_scan: withScan('warnings') }),
    )!
    expect(banner.state).toBe('scan-blocked')
    expect(banner.tone).toBe('warning')
  })

  it('scenario 4: any other quarantined server is described as awaiting manual review', () => {
    const banner = deriveQuarantineBannerState(
      quarantined({ trust_mode: 'manual', security_scan: withScan('clean') }),
    )!
    expect(banner.headline.toLowerCase()).toMatch(/review/)
    expect(banner.detail.toLowerCase()).toMatch(/approv/)
    expect(banner.tone).toBe('neutral')
  })

  it('scenario 5: with no scan history the copy says so and suggests running one', () => {
    const banner = deriveQuarantineBannerState(quarantined({ trust_mode: 'manual' }))!
    expect(banner.state).toBe('manual-review')
    expect(banner.detail.toLowerCase()).toMatch(/no security scan has run|never been scanned|not been scanned/)
    expect(banner.actions).toContain('run-scan')
  })
})
