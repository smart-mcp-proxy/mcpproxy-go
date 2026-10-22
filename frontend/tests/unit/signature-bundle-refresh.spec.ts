import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { formatSignatureBundle, type SignatureBundle } from '@/utils/signatureBundle'
import SignatureBundlePanelBody from '@/components/security/SignatureBundlePanelBody.vue'

// Spec 087 FR-017 / Spec 101 US3 (tasks B15, B16): the bundle panel answers
// "is my corpus current, when did it last check, and is the refresh working?".

const base: SignatureBundle = { source: 'file', path: '/opt/tpa/b.json', runnable_rules: 12, signature_verified: true }

function rows(b: SignatureBundle): Record<string, string> {
  const out = formatSignatureBundle(b)!
  return Object.fromEntries(out.identity.map((r) => [r.label, r.value]))
}

describe('formatSignatureBundle — refresh and freshness rows', () => {
  const full: SignatureBundle = {
    ...base,
    source: 'fetch',
    staleness_days: 9,
    last_checked_at: '2026-10-21T06:00:00Z',
    last_activated_at: '2026-10-20T06:00:00Z',
    refresh: {
      fetch_enabled: true,
      fetch_host: 'mirror.example.test',
      interval_seconds: 86400,
      last_trigger: 'timer',
      last_outcome: 'fetch_unavailable',
      last_reason: 'rate_limited',
      consecutive_failures: 1,
      next_check_at: '2026-10-22T06:00:00Z',
    },
    reprotect: { state: 'idle', servers_scanned: 4, servers_with_hits: 1 },
  }

  it('emits Last checked, Last activated, Refresh and Re-protection rows next to Age', () => {
    const out = formatSignatureBundle(full)!
    const labels = out.identity.map((r) => r.label)
    for (const l of ['Age', 'Last checked', 'Last activated', 'Refresh', 'Re-protection']) {
      expect(labels).toContain(l)
    }
    expect(labels.indexOf('Last checked')).toBeGreaterThan(labels.indexOf('Age'))
    const r = rows(full)
    expect(r['Last checked']).toBe('2026-10-21 06:00 UTC')
    expect(r['Last activated']).toBe('2026-10-20 06:00 UTC')
    expect(r['Refresh']).toMatch(/fetch unavailable/i)
    expect(r['Refresh']).toMatch(/rate limited/i)
    expect(r['Refresh']).toContain('next check 2026-10-22 06:00 UTC')
    expect(r['Re-protection']).toMatch(/idle/i)
    expect(r['Re-protection']).toContain('1 of 4')
  })

  it('shows the fetch host only while fetch is enabled and a host is known', () => {
    expect(rows(full)['Fetch host']).toBe('mirror.example.test')
    const off = rows({ ...full, refresh: { ...full.refresh!, fetch_enabled: false } })
    expect(off['Fetch host']).toBeUndefined()
    const none = rows({ ...full, refresh: { ...full.refresh!, fetch_host: undefined } })
    expect(none['Fetch host']).toBeUndefined()
  })

  it('labels the closed outcome vocabulary and keeps an unknown outcome visible', () => {
    const outcome = (o: string, reason?: string) =>
      rows({ ...base, refresh: { fetch_enabled: false, interval_seconds: 86400, last_outcome: o, last_reason: reason } })['Refresh']
    expect(outcome('activated')).toMatch(/activated/i)
    // Neutral wording: neither outcome proves the serving corpus is current.
    expect(outcome('unchanged')).toBe('No change')
    expect(outcome('not_modified')).toBe('Remote not modified')
    expect(outcome('unchanged')).not.toMatch(/up to date/i)
    expect(outcome('not_modified')).not.toMatch(/up to date/i)
    expect(outcome('rejected')).toMatch(/refused/i)
    expect(outcome('channel_invalid')).toMatch(/channel/i)
    expect(outcome('superseded')).toMatch(/superseded/i)
    expect(outcome('disabled', 'no_trusted_signer')).toMatch(/trusted signer/i)
    expect(outcome('some_future_outcome')).toContain('some future outcome')
  })

  it('describes a suspended re-protection sweep with its reason', () => {
    const r = rows({ ...base, reprotect: { state: 'suspended', reason: 'auto_baseline_scan_off', servers_scanned: 0, servers_with_hits: 0 } })
    expect(r['Re-protection']).toMatch(/suspended/i)
    expect(r['Re-protection']).toMatch(/baseline scan/i)
  })

  it('emits none of the new rows for a legacy descriptor', () => {
    const r = rows({ source: 'embedded', runnable_rules: 6, staleness_days: 4 })
    for (const l of ['Last checked', 'Last activated', 'Refresh', 'Re-protection', 'Fetch host']) {
      expect(r[l]).toBeUndefined()
    }
    expect(r['Age']).toBe('4 days')
  })

  it('leaves an unparsable timestamp as sent rather than inventing a date', () => {
    expect(rows({ ...base, last_checked_at: 'not-a-date' })['Last checked']).toBe('not-a-date')
  })
})

describe('formatSignatureBundle — refreshProblem', () => {
  const activated = '2026-10-20T06:00:00Z'
  const newer = '2026-10-21T01:00:00Z'
  const older = '2026-10-10T01:00:00Z'

  it('is null for a healthy bundle and for a legacy descriptor', () => {
    expect(formatSignatureBundle({ ...base, staleness_days: 3 })!.refreshProblem).toBeNull()
    expect(formatSignatureBundle({ source: 'embedded', runnable_rules: 6 })!.refreshProblem).toBeNull()
  })

  it('flags a stale external or fetched bundle, naming age and threshold', () => {
    for (const source of ['file', 'fetch']) {
      const p = formatSignatureBundle({ ...base, source, staleness_days: 81, stale: true, stale_after_days: 30 })!.refreshProblem
      expect(p).not.toBeNull()
      expect(p!.kind).toBe('stale')
      expect(p!.explanation).toContain('81')
      expect(p!.explanation).toContain('30')
    }
  })

  it('keeps a stale embedded corpus status-only unless fetch is enabled', () => {
    const stale: SignatureBundle = { source: 'embedded', runnable_rules: 6, staleness_days: 81, stale: true, stale_after_days: 30 }
    expect(formatSignatureBundle(stale)!.refreshProblem).toBeNull()
    expect(formatSignatureBundle({ ...stale, refresh: { fetch_enabled: false, interval_seconds: 86400 } })!.refreshProblem).toBeNull()
    const on = formatSignatureBundle({ ...stale, refresh: { fetch_enabled: true, interval_seconds: 86400 } })!.refreshProblem
    expect(on?.kind).toBe('stale')
  })

  it('flags a rejection newer than the last activation but not one in the quiet set', () => {
    const stuck = formatSignatureBundle({
      ...base, last_activated_at: activated, last_rejection: { reason: 'gate_failure', detail: 'x', at: newer },
    })!.refreshProblem
    expect(stuck?.kind).toBe('stuck')
    expect(stuck!.explanation).toMatch(/last-known-good/i)

    for (const reason of ['downgrade', 'deny_listed']) {
      expect(formatSignatureBundle({
        ...base, last_activated_at: activated, last_rejection: { reason, at: newer },
      })!.refreshProblem).toBeNull()
    }
    expect(formatSignatureBundle({
      ...base, last_activated_at: activated, last_rejection: { reason: 'tamper', at: older },
    })!.refreshProblem).toBeNull()
  })

  it('compares an embedded bundle’s rejection against process start (loaded_at)', () => {
    const e: SignatureBundle = { source: 'embedded', runnable_rules: 6, loaded_at: activated }
    expect(formatSignatureBundle({ ...e, last_rejection: { reason: 'tamper', at: newer } })!.refreshProblem?.kind).toBe('stuck')
    expect(formatSignatureBundle({ ...e, last_rejection: { reason: 'tamper', at: older } })!.refreshProblem).toBeNull()
  })

  it('flags three or more consecutive fetch failures, not two', () => {
    const mk = (n: number) => formatSignatureBundle({
      ...base, refresh: { fetch_enabled: true, interval_seconds: 86400, last_outcome: 'fetch_unavailable', consecutive_failures: n },
    })!.refreshProblem
    expect(mk(2)).toBeNull()
    expect(mk(3)?.kind).toBe('stuck')
  })

  it('flags fetch enabled but inactive for the three configuration reasons only', () => {
    const mk = (reason: string) => formatSignatureBundle({
      source: 'embedded', runnable_rules: 6,
      refresh: { fetch_enabled: true, interval_seconds: 86400, last_outcome: 'disabled', last_reason: reason, fetch_state: 'disabled', fetch_reason: reason },
    })!.refreshProblem
    for (const reason of ['no_url', 'invalid_url', 'no_trusted_signer']) {
      expect(mk(reason)?.kind).toBe('fetch_inactive')
    }
    expect(mk('fetch_off')).toBeNull()
    expect(mk('state_unavailable')).toBeNull()
  })

  it('still flags an inactive fetch leg behind a file activation that owns the cycle outcome', () => {
    const out = formatSignatureBundle({
      source: 'file', runnable_rules: 6,
      refresh: {
        fetch_enabled: true, interval_seconds: 86400, last_outcome: 'activated',
        fetch_state: 'disabled', fetch_reason: 'no_trusted_signer',
      },
    })!
    expect(out.refreshProblem?.kind).toBe('fetch_inactive')
    // The merged outcome alone is not enough: a disabled cycle outcome without
    // the fetch leg's own state is not reported.
    const merged = formatSignatureBundle({
      source: 'embedded', runnable_rules: 6,
      refresh: { fetch_enabled: true, interval_seconds: 86400, last_outcome: 'disabled', last_reason: 'no_trusted_signer' },
    })!
    expect(merged.refreshProblem).toBeNull()
  })

  it('lists every condition in refreshProblems, the first one in refreshProblem', () => {
    const out = formatSignatureBundle({
      ...base, source: 'fetch', staleness_days: 81, stale: true, stale_after_days: 30,
      last_activated_at: activated, last_rejection: { reason: 'key_not_trusted', at: newer },
      refresh: { fetch_enabled: true, interval_seconds: 86400, last_outcome: 'disabled', last_reason: 'no_trusted_signer', fetch_state: 'disabled', fetch_reason: 'no_trusted_signer', consecutive_failures: 4 },
    })!
    expect(out.refreshProblems.map((p) => p.kind)).toEqual(['stale', 'stuck', 'fetch_inactive'])
    expect(out.refreshProblem).toEqual(out.refreshProblems[0])
  })

  it('never turns a refresh problem into degraded coverage or changes the tone', () => {
    const out = formatSignatureBundle({
      ...base, staleness_days: 81, stale: true, stale_after_days: 30, coverage_degraded: false,
    })!
    expect(out.refreshProblem).not.toBeNull()
    expect(out.degraded).toBeNull()
    expect(out.tone).toBe('')
  })
})

describe('SignatureBundlePanelBody — refresh surface', () => {
  const stale: SignatureBundle = {
    ...{ source: 'fetch', runnable_rules: 12, signature_verified: true },
    staleness_days: 81, stale: true, stale_after_days: 30,
    last_checked_at: '2026-10-21T06:00:00Z',
    last_activated_at: '2026-10-20T06:00:00Z',
    refresh: { fetch_enabled: true, fetch_host: 'mirror.example.test', interval_seconds: 86400, last_outcome: 'unchanged' },
    reprotect: { state: 'idle', servers_scanned: 2, servers_with_hits: 0 },
  }

  it('renders the freshness rows and a refresh-problem notice', () => {
    const wrapper = mount(SignatureBundlePanelBody, { props: { summary: formatSignatureBundle(stale)! } })
    const text = wrapper.text()
    expect(text).toContain('Last checked')
    expect(text).toContain('Last activated')
    expect(text).toContain('Refresh')
    expect(text).toContain('Re-protection')
    const problem = wrapper.find('[data-test="bundle-refresh-problem"]')
    expect(problem.exists()).toBe(true)
    expect(problem.text()).toContain('81')
  })

  it('renders no problem notice for a healthy bundle', () => {
    const wrapper = mount(SignatureBundlePanelBody, {
      props: { summary: formatSignatureBundle({ ...stale, stale: false })! },
    })
    expect(wrapper.find('[data-test="bundle-refresh-problem"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Last checked')
  })

  it('has no mutation button and no unquarantine or approve reference', () => {
    const src = readFileSync(
      resolve(__dirname, '../../src/components/security/SignatureBundlePanelBody.vue'),
      'utf8'
    )
    expect(src).not.toMatch(/unquarantine/i)
    expect(src).not.toMatch(/approve/i)
    expect(src).not.toMatch(/\$api|fetch\(|axios|\/api\/v1/)
    // Only the two digest conveniences are buttons: no "Check now", no rollback.
    const buttons = src.match(/<button/g) ?? []
    expect(buttons.length).toBe(2)
    // No click handler other than the two digest conveniences (so no "Check
    // now", rollback, reload or refresh trigger).
    const handlers = [...src.matchAll(/@click="([^"]+)"/g)].map((m) => m[1])
    expect(handlers.length).toBe(2)
    expect(handlers.some((h) => !/showFullDigest|copyFullDigest/.test(h))).toBe(false)
    const wrapper = mount(SignatureBundlePanelBody, { props: { summary: formatSignatureBundle(stale)! } })
    expect(wrapper.findAll('button').every((b) => /digest|copy/i.test(b.text()))).toBe(true)
  })
})
