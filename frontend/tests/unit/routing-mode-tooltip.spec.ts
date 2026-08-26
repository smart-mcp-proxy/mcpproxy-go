import { describe, it, expect } from 'vitest'
import { routingModeMeta } from '@/utils/routingMode'

// Audit F31: the header's `Mode: Retrieve` badge is the most consequential
// setting on the page — it decides what an agent sees on connect — and it was
// rendered as a bare word with nothing to explain it.

describe('routingModeMeta (audit F31)', () => {
  it('explains every mode it labels', () => {
    for (const mode of ['retrieve_tools', 'direct', 'code_execution']) {
      const meta = routingModeMeta(mode)
      expect(meta.label.length).toBeGreaterThan(0)
      expect(meta.description.length).toBeGreaterThan(20)
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
    expect(routingModeMeta('something_new').description).toContain('retrieve_tools')
  })
})
