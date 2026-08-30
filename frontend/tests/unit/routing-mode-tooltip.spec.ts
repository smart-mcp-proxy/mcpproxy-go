import { describe, it, expect } from 'vitest'
import { routingModeMeta } from '@/utils/routingMode'

// Audit F31: the header's `Mode: Retrieve` badge is the most consequential
// setting on the page — it decides what an agent sees on connect — and it was
// rendered as a bare word with nothing to explain it.

describe('routingModeMeta (audit F31)', () => {
  // Three lengths of the same fact: the chip, one short always-visible line,
  // and the full explanation that only ever appears as a hover hint. The
  // summary has to stay short or the dropdown starts scrolling.
  it('explains every mode it labels, at both lengths', () => {
    for (const mode of ['retrieve_tools', 'direct', 'code_execution']) {
      const meta = routingModeMeta(mode)
      expect(meta.label.length).toBeGreaterThan(0)
      expect(meta.summary.length).toBeGreaterThan(10)
      expect(meta.summary.length).toBeLessThanOrEqual(45)
      expect(meta.detail.length).toBeGreaterThan(60)
      expect(meta.endpoint).toMatch(/^\/mcp/)
    }
  })

  it('labels the modes the header used to hard-code', () => {
    expect(routingModeMeta('direct').label).toBe('Direct')
    expect(routingModeMeta('code_execution').label).toBe('Code Exec')
    expect(routingModeMeta('retrieve_tools').label).toBe('Retrieve')
  })

  it('falls back to Retrieve — the default — for unknown or missing modes', () => {
    expect(routingModeMeta(undefined).label).toBe('Retrieve')
    expect(routingModeMeta('').label).toBe('Retrieve')
    expect(routingModeMeta('something_new').label).toBe('Retrieve')
    expect(routingModeMeta('something_new').detail).toContain('retrieve_tools')
  })
})
