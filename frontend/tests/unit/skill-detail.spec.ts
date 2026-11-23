import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SkillDetail from '@/components/skills/SkillDetail.vue'
import api from '@/services/api'
import { makeSkill, skillFile, ok } from './fixtures/skills125'

vi.mock('@/services/api', () => ({ default: { getSkillContent: vi.fn() } }))
const m = (f: unknown) => f as ReturnType<typeof vi.fn>
/** The digest makeSkill lists for SKILL.md. */
const D = 'sha256:SKILL.md'
const CHANGED = /This file changed; reload to review the new version/
const deferred = () => { let resolve: (v: unknown) => void = () => {}; const p = new Promise(r => { resolve = r }); return { p, resolve } }
const mountDetail = (skill = makeSkill('a'), busy = false) =>
  mount(SkillDetail, { props: { serverName: 'srv', skill, busy } })

describe('SkillDetail', () => {
  beforeEach(() => vi.clearAllMocks())

  it('lists files and offers a viewer for text files only', async () => {
    const w = mountDetail(makeSkill('a', { files: [skillFile('SKILL.md'), skillFile('bin/x.png', 'sha256:b', 9, false)] }))
    expect(w.find('[data-test="skill-file-SKILL.md"]').exists()).toBe(true)
    expect(w.get('[data-test="skill-view-SKILL.md"]').text()).toBe('Read SKILL.md')
    expect(w.get('[data-test="skill-file-SKILL.md"]').find('button').exists()).toBe(false)
    expect(w.find('[data-test="skill-file-bin/x.png"]').exists()).toBe(true)
    expect(w.find('[data-test="skill-view-bin/x.png"]').exists()).toBe(false)
    expect(w.get('[data-test="skill-file-bin/x.png"]').text()).toMatch(/binary/i)
  })

  it.each([
    { description: 'missing', files: [skillFile('notes.md')] },
    { description: 'binary', files: [skillFile('SKILL.md', D, 9, false)] },
  ])('omits the SKILL.md shortcut when the file is $description', ({ files }) => {
    const w = mountDetail(makeSkill('a', { files }))
    expect(w.find('[data-test="skill-view-SKILL.md"]').exists()).toBe(false)
    expect(api.getSkillContent).not.toHaveBeenCalled()
  })

  it('opens the instructions in one action and restores focus on Close', async () => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'Instructions', offset: 0, size: 12, digest: D }))
    const w = mount(SkillDetail, { attachTo: document.body, props: { serverName: 'srv', skill: makeSkill('a') } })
    try {
      const shortcut = w.get('[data-test="skill-view-SKILL.md"]')
      expect(api.getSkillContent).not.toHaveBeenCalled()
      await shortcut.trigger('click'); await flushPromises()
      expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('Instructions')
      expect(document.activeElement).toBe(w.get('[data-test="skill-viewer-heading"]').element)
      await w.get('[data-test="skill-viewer-close"]').trigger('click'); await flushPromises()
      expect(w.find('[data-test="skill-viewer"]').exists()).toBe(false)
      expect(document.activeElement).toBe(shortcut.element)
    } finally {
      w.unmount()
    }
  })

  it('places the full digest in a closed disclosure outside the file inventory', () => {
    const w = mountDetail()
    const hashes = w.get('[data-test="skill-file-hashes"]')
    expect(hashes.element.tagName).toBe('DETAILS')
    expect(hashes.attributes('open')).toBeUndefined()
    expect(hashes.get('[data-test="skill-hash-SKILL.md"]').text()).toBe(D)
    expect(w.get('[data-test="skill-file-SKILL.md"]').text()).not.toContain(D)
  })

  it('closes metadata disclosures when a different skill snapshot replaces the card', async () => {
    const approved = (name: string) => makeSkill(name, { approved_files: [skillFile('SKILL.md')] })
    const w = mountDetail(approved('a'))
    const selectors = ['[data-test="skill-file-hashes"]', '[data-test="skill-unchanged-files"]']
    for (const selector of selectors) (w.get(selector).element as HTMLDetailsElement).open = true
    await w.setProps({ skill: approved('b') })
    for (const selector of selectors) expect((w.get(selector).element as HTMLDetailsElement).open).toBe(false)
  })

  it.each([
    { copy: 'current', selector: 'skill-view-notes.md', files: [skillFile('SKILL.md'), skillFile('notes.md')], approvedFiles: undefined },
    { copy: 'approved', selector: 'skill-view-approved-notes.md', files: [skillFile('SKILL.md')], approvedFiles: [skillFile('SKILL.md'), skillFile('notes.md')] },
  ])('focuses the viewer and returns to a supporting $copy copy action', async ({ copy, selector, files, approvedFiles }) => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'Notes', offset: 0, size: 5, digest: 'sha256:notes.md' }))
    const w = mount(SkillDetail, { attachTo: document.body, props: { serverName: 'srv', skill: makeSkill('a', { files, approved_files: approvedFiles }) } })
    try {
      const open = w.get(`[data-test="${selector}"]`)
      await open.trigger('click'); await flushPromises()
      expect(api.getSkillContent).toHaveBeenCalledWith('srv', { skill: 'a', file: 'notes.md', copy, offset: 0 })
      expect(document.activeElement).toBe(w.get('[data-test="skill-viewer-heading"]').element)
      await w.get('[data-test="skill-viewer-close"]').trigger('click'); await flushPromises()
      expect(document.activeElement).toBe(open.element)
    } finally {
      w.unmount()
    }
  })

  it('views a file and pages it with Load more, joining the parts', async () => {
    m(api.getSkillContent)
      .mockResolvedValueOnce(ok({ text: 'part one ', offset: 0, next_offset: 9, size: 17, digest: D }))
      .mockResolvedValueOnce(ok({ text: 'part two', offset: 9, size: 17, digest: D }))
    const w = mountDetail()
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    expect(api.getSkillContent).toHaveBeenNthCalledWith(1, 'srv', { skill: 'a', file: 'SKILL.md', copy: 'current', offset: 0 })
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('part one')
    await w.get('[data-test="skill-load-more"]').trigger('click'); await flushPromises()
    expect(api.getSkillContent).toHaveBeenNthCalledWith(2, 'srv', { skill: 'a', file: 'SKILL.md', copy: 'current', offset: 9 })
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('part one part two')
    expect(w.find('[data-test="skill-load-more"]').exists()).toBe(false)
  })

  it('can switch the viewer to the approved copy when one exists', async () => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'x', offset: 0, size: 1, digest: D }))
    const w = mountDetail(makeSkill('a', { status: 'changed', approved_hash: 'sk1-old', approved_files: [skillFile('SKILL.md')] }))
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-copy-approved"]').trigger('click'); await flushPromises()
    expect(m(api.getSkillContent).mock.calls.at(-1)![1]).toMatchObject({ copy: 'approved', offset: 0 })
  })

  it('shows a viewer failure', async () => {
    m(api.getSkillContent).mockResolvedValue({ success: false, error: 'stored_copy_damaged: x', errorCode: 'stored_copy_damaged' })
    const w = mountDetail()
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-error"]').text()).toContain('stored_copy_damaged')
  })

  it('shows the file-set diff of the approved copy against the current one', () => {
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md', 'd1'), skillFile('gone.txt', 'g'), skillFile('same.md', 's')],
      files: [skillFile('SKILL.md', 'd2'), skillFile('new.txt', 'n'), skillFile('same.md', 's')],
    }))
    const state = (p: string) => w.get(`[data-test="skill-diff-${p}"]`).attributes('data-state')
    expect([state('SKILL.md'), state('gone.txt'), state('new.txt'), state('same.md')]).toEqual(['changed', 'removed', 'added', 'same'])
    const unchanged = w.get('[data-test="skill-unchanged-files"]')
    expect(unchanged.attributes('open')).toBeUndefined()
    expect(unchanged.get('[data-test="skill-diff-same.md"]').text()).toBe('same.md')
    expect(unchanged.find('[data-test="skill-diff-gone.txt"]').exists()).toBe(false)
  })

  it('shows no diff section without an approved copy', () => {
    expect(mountDetail().find('[data-test="skill-diff"]').exists()).toBe(false)
  })

  it('loads both copies of a changed text file and shows the line diff', async () => {
    m(api.getSkillContent).mockImplementation(async (_s: string, q: any) =>
      ok({ text: q.copy === 'approved' ? 'a\nb\nc' : 'a\nc\nd', offset: 0, size: 5, digest: q.copy === 'approved' ? 'd1' : 'd2' }))
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md', 'd1', 5)], files: [skillFile('SKILL.md', 'd2', 5)],
    }))
    await w.get('[data-test="skill-line-diff-SKILL.md"]').trigger('click'); await flushPromises()
    const ops = w.findAll('[data-test="skill-diff-line"]').map(l => `${l.attributes('data-op')}:${l.text()}`)
    expect(ops).toEqual(['same:a', 'del:b', 'same:c', 'add:d'])
  })

  it('does not fetch a line diff for a file over 256 KiB and says so', async () => {
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md', 'd1', 300 * 1024)], files: [skillFile('SKILL.md', 'd2', 10)],
    }))
    await w.get('[data-test="skill-line-diff-SKILL.md"]').trigger('click'); await flushPromises()
    expect(api.getSkillContent).not.toHaveBeenCalled()
    expect(w.get('[data-test="skill-line-diff-note"]').text()).toMatch(/too large/i)
  })

  it('renders the scan verdict and findings per file, with the truncation note', () => {
    const w = mountDetail(makeSkill('a', { scan: {
      verdict: 'dangerous', complete: false, incomplete: 'size_cap', findings_truncated: true,
      findings: [{ file: 'SKILL.md', offset: 12, rule: 'phrase.injection', severity: 'high', evidence: 'ignore previous' }],
    } }))
    const scan = w.get('[data-test="skill-scan"]').text()
    expect(scan).toContain('Dangerous scan findings')
    expect(scan).toMatch(/incomplete/i)
    expect(scan).toContain('size_cap')
    const f = w.get('[data-test="skill-finding"]').text()
    expect(f).toContain('SKILL.md'); expect(f).toContain('phrase.injection'); expect(f).toContain('high'); expect(f).toContain('ignore previous')
    expect(w.get('[data-test="skill-findings-truncated"]').exists()).toBe(true)
    const approve = w.get('[data-test="skill-approve"]').element
    expect(w.get('[data-test="skill-scan"]').element.compareDocumentPosition(approve) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('labels a clean but incomplete scan as incomplete before Approve changes', () => {
    const skill = makeSkill('a', { status: 'changed' })
    skill.scan = { ...skill.scan, complete: false, incomplete: 'coverage' }
    const w = mountDetail(skill)
    const scan = w.get('[data-test="skill-scan"]')
    expect(scan.text()).toContain('Scan incomplete')
    expect(scan.text()).toContain('coverage')
    expect(scan.text()).not.toContain('Scan clean')
    const approve = w.get('[data-test="skill-approve"]')
    expect(approve.text()).toBe('Approve changes')
    expect(scan.element.compareDocumentPosition(approve.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('preserves unrecognized verdicts and omits timestamps without a verdict', async () => {
    const skill = makeSkill('a')
    const scan = { ...skill.scan, scanned_at: '2026-11-21T14:00:00Z' }
    const w = mountDetail({ ...skill, scan: { ...scan, verdict: 'other-verdict' } })
    const result = w.get('[data-test="skill-scan"]')
    expect(result.text()).toContain('other-verdict')
    await w.setProps({ skill: { ...skill, scan: { ...scan, verdict: '' } } })
    expect(result.text()).toBe('Not scanned')
  })

  it('emits approve, block and unblock for the skill and disables them while busy', async () => {
    const w = mountDetail(makeSkill('a'))
    await w.get('[data-test="skill-approve"]').trigger('click')
    await w.get('[data-test="skill-block"]').trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
    expect(w.emitted('block')).toHaveLength(1)
    const b = mountDetail(makeSkill('a', { blocked: true }), true)
    expect(b.get('[data-test="skill-unblock"]').attributes('disabled')).toBeDefined()
  })

  it('resets the viewer when the skill content changes under it', async () => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'old', offset: 0, size: 3, digest: D }))
    const w = mountDetail(makeSkill('a', { current_hash: 'sk1-1' }))
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-viewer"]').exists()).toBe(true)
    await w.setProps({ skill: makeSkill('a', { current_hash: 'sk1-2' }) })
    expect(w.find('[data-test="skill-viewer"]').exists()).toBe(false)
  })

  // Review finding 2: every part must be the version listed on screen.
  it('drops the viewer text when a later part is of another version, says so and asks for a reload', async () => {
    m(api.getSkillContent)
      .mockResolvedValueOnce(ok({ text: 'old part one ', offset: 0, next_offset: 13, size: 30, digest: D }))
      .mockResolvedValueOnce(ok({ text: 'NEW part two', offset: 13, size: 30, digest: 'sha256:moved' }))
    const w = mountDetail()
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-load-more"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('')
    expect(w.get('[data-test="skill-viewer-error"]').text()).toMatch(CHANGED)
    expect(w.find('[data-test="skill-load-more"]').exists()).toBe(false)
    expect(w.emitted('content-changed')).toHaveLength(1)
  })

  it('refuses a first part whose digest is not the listed one (the viewer never shows another copy)', async () => {
    m(api.getSkillContent).mockResolvedValueOnce(ok({ text: 'other copy', offset: 0, size: 10, digest: 'sha256:other' }))
    const w = mountDetail()
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('')
    expect(w.get('[data-test="skill-viewer-error"]').text()).toMatch(CHANGED)
    expect(w.emitted('content-changed')).toHaveLength(1)
  })

  it('binds the approved copy to the approved digest, not the current one', async () => {
    m(api.getSkillContent).mockImplementation(async (_s: string, q: any) =>
      ok({ text: q.copy, offset: 0, size: 5, digest: q.copy === 'approved' ? 'sha256:appr' : D }))
    const w = mountDetail(makeSkill('a', { status: 'changed', approved_hash: 'sk1-old', approved_files: [skillFile('SKILL.md', 'sha256:appr')] }))
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-copy-approved"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('approved')
    expect(w.find('[data-test="skill-viewer-error"]').exists()).toBe(false)
  })

  it('refuses a part that answers another offset', async () => {
    m(api.getSkillContent)
      .mockResolvedValueOnce(ok({ text: 'part one ', offset: 0, next_offset: 9, size: 17, digest: D }))
      .mockResolvedValueOnce(ok({ text: 'art two', offset: 10, size: 17, digest: D }))
    const w = mountDetail()
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-load-more"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('')
    expect(w.get('[data-test="skill-viewer-error"]').text()).toMatch(CHANGED)
  })

  it('the line diff refuses a copy of another version and asks for a reload', async () => {
    m(api.getSkillContent).mockImplementation(async (_s: string, q: any) =>
      ok({ text: 'x', offset: 0, size: 5, digest: q.copy === 'approved' ? 'd1' : 'sha256:moved' }))
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md', 'd1', 5)], files: [skillFile('SKILL.md', 'd2', 5)],
    }))
    await w.get('[data-test="skill-line-diff-SKILL.md"]').trigger('click'); await flushPromises()
    expect(w.findAll('[data-test="skill-diff-line"]')).toHaveLength(0)
    expect(w.get('[data-test="skill-line-diff-note"]').text()).toMatch(CHANGED)
    expect(w.emitted('content-changed')).toHaveLength(1)
  })

  // Review finding 4: a removed text file can be read from the approved copy.
  it('offers View approved for a removed text file and reads the approved copy', async () => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'gone body', offset: 0, size: 9, digest: 'sha256:g' }))
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md'), skillFile('gone.txt', 'sha256:g', 9), skillFile('gone.bin', 'sha256:b', 9, false)],
      files: [skillFile('SKILL.md')],
    }))
    expect(w.find('[data-test="skill-view-approved-gone.bin"]').exists()).toBe(false)
    await w.get('[data-test="skill-view-approved-gone.txt"]').trigger('click'); await flushPromises()
    expect(api.getSkillContent).toHaveBeenCalledWith('srv', { skill: 'a', file: 'gone.txt', copy: 'approved', offset: 0 })
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('gone body')
    // The file has no current copy, so the viewer offers no Current toggle for it.
    expect(w.find('[data-test="skill-copy-current"]').exists()).toBe(false)
  })

  // Review finding 8: late content responses are dropped.
  it('drops a late content response after the skill was switched', async () => {
    const late = deferred()
    m(api.getSkillContent).mockReturnValueOnce(late.p)
    const w = mountDetail(makeSkill('a'))
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.setProps({ skill: makeSkill('b') }); await flushPromises()
    late.resolve(ok({ text: 'text of a', offset: 0, size: 9, digest: D })); await flushPromises()
    expect(w.find('[data-test="skill-viewer"]').exists()).toBe(false)
    expect(w.text()).not.toContain('text of a')
  })

  it('drops a late content response after the copy was switched', async () => {
    const late = deferred()
    m(api.getSkillContent)
      .mockReturnValueOnce(late.p)
      .mockResolvedValueOnce(ok({ text: 'approved text', offset: 0, size: 13, digest: D }))
    const w = mountDetail(makeSkill('a', { status: 'changed', approved_hash: 'sk1-old', approved_files: [skillFile('SKILL.md')] }))
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-copy-approved"]').trigger('click'); await flushPromises()
    late.resolve(ok({ text: 'current text', offset: 0, size: 12, digest: D })); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('approved text')
  })

  // Round-2 finding 6: a text file replaced by a binary file at the same path.
  it('offers View approved when the approved copy is text and the current file is binary', async () => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'old text', offset: 0, size: 8, digest: 'sha256:t' }))
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md'), skillFile('notes', 'sha256:t', 8, true)],
      files: [skillFile('SKILL.md'), skillFile('notes', 'sha256:bin', 8, false)],
    }))
    expect(w.get('[data-test="skill-diff-notes"]').attributes('data-state')).toBe('changed')
    // No line diff (the current copy is binary), but the approved copy can be read.
    expect(w.find('[data-test="skill-line-diff-notes"]').exists()).toBe(false)
    await w.get('[data-test="skill-view-approved-notes"]').trigger('click'); await flushPromises()
    expect(api.getSkillContent).toHaveBeenCalledWith('srv', { skill: 'a', file: 'notes', copy: 'approved', offset: 0 })
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('old text')
    // Round-3 finding 5: the current copy is binary (the content route answers 415), so it is not offered.
    expect(w.find('[data-test="skill-copy-current"]').exists()).toBe(false)
    expect(w.get('[data-test="skill-viewer-copy"]').text()).toBe('approved copy')
  })

  it('does not offer the approved copy when it is binary and the current file is text', async () => {
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'new text', offset: 0, size: 8, digest: 'sha256:t' }))
    const w = mountDetail(makeSkill('a', {
      status: 'changed', approved_hash: 'sk1-old',
      approved_files: [skillFile('SKILL.md'), skillFile('notes', 'sha256:bin', 8, false)],
      files: [skillFile('SKILL.md'), skillFile('notes', 'sha256:t', 8, true)],
    }))
    expect(w.find('[data-test="skill-view-approved-notes"]').exists()).toBe(false)
    await w.get('[data-test="skill-view-notes"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('new text')
    expect(w.find('[data-test="skill-copy-approved"]').exists()).toBe(false)
    expect(w.get('[data-test="skill-viewer-copy"]').text()).toBe('current copy')
  })

  // Round-3 finding 4: a reload that moved nothing (same server, path and hashes)
  // keeps the open viewer and the pages loaded into it.
  it('keeps an open paged viewer across an unchanged reload, and drops it on a real change', async () => {
    m(api.getSkillContent)
      .mockResolvedValueOnce(ok({ text: 'part1', offset: 0, size: 10, digest: D, next_offset: 5 }))
      .mockResolvedValueOnce(ok({ text: 'part2', offset: 5, size: 10, digest: D }))
    const w = mountDetail(makeSkill('a'))
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-load-more"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('part1part2')
    // The same version again as a new object (e.g. a skills.changed for another skill, or a block).
    await w.setProps({ skill: makeSkill('a', { blocked: true }) }); await flushPromises()
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe('part1part2')
    await w.setProps({ skill: makeSkill('a', { blocked: true, current_hash: 'sk1-moved' }) }); await flushPromises()
    expect(w.find('[data-test="skill-viewer"]').exists()).toBe(false)
  })

  // Round-2 finding 11: an entry without a scan block (the agent shape) does not throw.
  it('renders an entry without a scan block', () => {
    const s = makeSkill('a') as any
    delete s.scan
    const w = mountDetail(s)
    expect(w.get('[data-test="skill-scan"]').text()).toContain('Not scanned')
  })

  it('disables Approve while the review is stale', () => {
    const w = mount(SkillDetail, { props: { serverName: 'srv', skill: makeSkill('a'), busy: false, stale: true } })
    expect(w.get('[data-test="skill-approve"]').attributes('disabled')).toBeDefined()
  })
})
