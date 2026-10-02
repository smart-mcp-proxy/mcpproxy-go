import { describe, it, expect } from 'vitest'
import { serversStepView, awaitingReviewSentence, importSummary } from '@/utils/onboardingServersStep'

// Spec 109 US7-4 / FR-043 (fix-usertest-web T173): one completion state after
// an import, and copy that reads as a sentence.

describe('serversStepView', () => {
  const base = { sourcesWithServers: 0, hasUsableServer: false, awaitingReview: 0, importedThisSession: 0 }

  it('chooses while something is left to import', () => {
    expect(serversStepView({ ...base, sourcesWithServers: 2, awaitingReview: 3, importedThisSession: 1 })).toBe('choose')
  })
  it('reviews when nothing is left, nothing is usable and servers wait', () => {
    expect(serversStepView({ ...base, awaitingReview: 4 })).toBe('review')
    expect(serversStepView({ ...base, awaitingReview: 4, importedThisSession: 2 })).toBe('review')
  })
  it('shows the imported completion when this session imported and nothing waits or one is usable', () => {
    expect(serversStepView({ ...base, importedThisSession: 2, hasUsableServer: true })).toBe('imported')
    expect(serversStepView({ ...base, importedThisSession: 2, awaitingReview: 0 })).toBe('imported')
    expect(serversStepView({ ...base, importedThisSession: 2, hasUsableServer: true, awaitingReview: 3 })).toBe('imported')
  })
  it('is empty otherwise', () => {
    expect(serversStepView(base)).toBe('empty')
    expect(serversStepView({ ...base, hasUsableServer: true })).toBe('empty')
  })
})

describe('awaitingReviewSentence', () => {
  it.each([
    [1, 0, '1 server is waiting in quarantine — review its tools before it can run.'],
    [2, 0, '2 servers are waiting in quarantine — review their tools before they can run.'],
    [6, 2, '6 servers are waiting in quarantine, including the 2 you just imported — review their tools before they can run.'],
    [2, 2, 'The 2 servers you just imported are waiting in quarantine — review their tools before they can run.'],
    [1, 1, 'The server you just imported is waiting in quarantine — review its tools before it can run.'],
    [3, 1, '3 servers are waiting in quarantine, including the one you just imported — review their tools before they can run.'],
  ])('(%i, %i)', (n, imported, expected) => {
    expect(awaitingReviewSentence(n, imported)).toBe(expected)
  })
})

describe('importSummary', () => {
  it('does not report an unselected (filtered_out) skip', () => {
    expect(importSummary({ imported: 2, renamed: 0, skipped: [{ reason: 'filtered_out' }] })).toBe('2 servers imported')
  })
  it('reports renamed and real skips', () => {
    expect(
      importSummary({ imported: 1, renamed: 1, skipped: [{ reason: 'already_exists' }, { reason: 'self_reference' }] }),
    ).toBe('1 server imported · 1 renamed · 1 skipped (already configured) · 1 skipped (connects to mcpproxy itself)')
  })
  it('counts repeated reasons once per reason', () => {
    expect(importSummary({ imported: 1, skipped: [{ reason: 'already_exists' }, { reason: 'already_exists' }] })).toBe(
      '1 server imported · 2 skipped (already configured)',
    )
  })
  it('says so when nothing was imported', () => {
    expect(importSummary({ imported: 0, skipped: [] })).toBe('No servers imported')
  })
})
