import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SkillsPanel from '@/components/skills/SkillsPanel.vue'
import api from '@/services/api'
import { makeListing, makeSkill, ok } from './fixtures/skills125'

vi.mock('@/services/api', () => ({ default: {
  getServerSkills: vi.fn(), refreshServerSkills: vi.fn(), getSkillContent: vi.fn(),
  approveSkills: vi.fn(), blockSkills: vi.fn(), unblockSkills: vi.fn(),
} }))

const m = (f: unknown) => f as ReturnType<typeof vi.fn>
const mountPanel = (serverName = 'srv') => mount(SkillsPanel, { props: { serverName } })

describe('SkillsPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'body', offset: 0, size: 4, digest: 'd' }))
  })

  it('lists skills with a state chip each', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([
      makeSkill('plain'),
      makeSkill('changed', { status: 'changed', approved_hash: 'sk1-old' }),
      makeSkill('ok', { status: 'approved', available: true }),
      makeSkill('bad', { status: 'unusable', unusable_reason: 'dynamic' }),
      makeSkill('blk', { status: 'approved', blocked: true }),
    ])))
    const w = mountPanel(); await flushPromises()
    const chip = (p: string) => w.get(`[data-test="skill-chip-${p}"]`).text()
    expect([chip('plain'), chip('changed'), chip('ok'), chip('bad'), chip('blk')])
      .toEqual(['Awaiting review', 'Changed', 'Approved', 'Unusable', 'Blocked'])
    expect(w.get('[data-test="skill-row-ok"]').text()).toContain('Offered to agents')
    expect(w.get('[data-test="skill-row-plain"]').text()).toContain('Not offered')
  })

  it('shows the feature-off, unsupported and empty states', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([], { enabled: false })))
    let w = mountPanel(); await flushPromises()
    expect(w.find('[data-test="skills-empty-disabled"]').exists()).toBe(true)
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([], { supported: false })))
    w = mountPanel(); await flushPromises()
    expect(w.find('[data-test="skills-empty-unsupported"]').exists()).toBe(true)
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([])))
    w = mountPanel(); await flushPromises()
    expect(w.find('[data-test="skills-empty-none"]').exists()).toBe(true)
  })

  it('shows a load failure with a retry', async () => {
    m(api.getServerSkills).mockResolvedValueOnce({ success: false, error: 'boom' })
    const w = mountPanel(); await flushPromises()
    expect(w.get('[data-test="skills-error"]').text()).toContain('boom')
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')])))
    await w.get('[data-test="skills-retry"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-row-a"]').exists()).toBe(true)
  })

  it('approve is bound to the shown current_hash and incarnation', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a', { current_hash: 'sk1-shown' })], { incarnation: 9 })))
    m(api.approveSkills).mockResolvedValue(ok({ approved: 1 }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(api.approveSkills).toHaveBeenCalledTimes(1)
    const [server, skills, hashes, inc] = m(api.approveSkills).mock.calls[0]
    expect(server).toBe('srv'); expect(skills).toEqual(['a']); expect(inc).toBe(9)
    expect(JSON.parse(JSON.stringify(hashes))).toEqual({ a: 'sk1-shown' })
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/approved/i)
    expect(api.getServerSkills).toHaveBeenCalledTimes(2) // reloaded after the write
  })

  it('a 409 review_out_of_date reloads the list and says nothing was approved, with no retry', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a', { current_hash: 'sk1-1' })])))
    m(api.approveSkills).mockResolvedValue({ success: false, error: 'review_out_of_date: x', errorCode: 'review_out_of_date' })
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a', { current_hash: 'sk1-2', status: 'changed' })])))
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(api.approveSkills).toHaveBeenCalledTimes(1)
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/changed since you opened it/i)
    expect(w.get('[data-test="skill-chip-a"]').text()).toBe('Changed')
  })

  it('a 409 server_replaced reloads the list and says so', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    m(api.approveSkills).mockResolvedValue({ success: false, error: 'server_replaced: x', errorCode: 'server_replaced' })
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(api.approveSkills).toHaveBeenCalledTimes(1) // no retry
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/removed and added again/i)
  })

  it('a 422 skill_unusable shows the message', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    m(api.approveSkills).mockResolvedValue({ success: false, error: 'skill_unusable: a is dynamic', errorCode: 'skill_unusable' })
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-notice"]').text()).toContain('skill_unusable: a is dynamic')
  })

  it('does not offer approve for a blocked, unusable or approved skill', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([
      makeSkill('blk', { status: 'changed', blocked: true }),
      makeSkill('bad', { status: 'unusable', unusable_reason: 'dynamic' }),
      makeSkill('ok', { status: 'approved', approved_hash: 'sk1-ok', current_hash: 'sk1-ok' }),
    ])))
    const w = mountPanel(); await flushPromises()
    for (const p of ['blk', 'bad', 'ok']) {
      await w.get(`[data-test="skill-row-${p}"]`).trigger('click'); await flushPromises()
      expect(w.find('[data-test="skill-approve"]').exists()).toBe(false)
    }
  })

  it('block and unblock send the incarnation and reload', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a'), makeSkill('b', { blocked: true })], { incarnation: 4 })))
    // The real client's methods use `this`: a detached call must fail here as it does there.
    const bound = (result: unknown) => vi.fn(function (this: unknown) { if (this !== api) throw new TypeError('detached call'); return Promise.resolve(result) })
    ;(api as any).blockSkills = bound(ok({ changed: 1 }))
    ;(api as any).unblockSkills = bound(ok({ changed: 1 }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-block"]').trigger('click'); await flushPromises()
    expect(api.blockSkills).toHaveBeenCalledWith('srv', ['a'], 4)
    await w.get('[data-test="skill-row-b"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-unblock"]').trigger('click'); await flushPromises()
    expect(api.unblockSkills).toHaveBeenCalledWith('srv', ['b'], 4)
    expect(api.getServerSkills).toHaveBeenCalledTimes(3)
  })

  it('refresh replaces the listing; a 409 refresh_in_progress shows a notice', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    m(api.refreshServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a'), makeSkill('new')])))
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-row-new"]').exists()).toBe(true)
    m(api.refreshServerSkills).mockResolvedValueOnce({ success: false, error: 'refresh_in_progress: x', errorCode: 'refresh_in_progress' })
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/already running/i)
  })

  it('reloads on mcpproxy:skills-changed for its own server only', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'other' } }))
    await flushPromises()
    expect(api.getServerSkills).toHaveBeenCalledTimes(1)
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'srv' } }))
    await flushPromises()
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    w.unmount()
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'srv' } }))
    await flushPromises()
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
  })

  it('a late response for the previous server never overwrites the new one, and the selection resets', async () => {
    let releaseA: (v: unknown) => void = () => {}
    m(api.getServerSkills).mockImplementation((name: string) =>
      name === 'a' ? new Promise((r) => { releaseA = r }) : Promise.resolve(ok(makeListing([makeSkill('only_b')]))))
    const w = mountPanel('a')
    await w.setProps({ serverName: 'b' }); await flushPromises()
    releaseA(ok(makeListing([makeSkill('only_a')]))); await flushPromises()
    expect(w.find('[data-test="skill-row-only_a"]').exists()).toBe(false)
    expect(w.find('[data-test="skill-row-only_b"]').exists()).toBe(true)
  })

  it('drops the selection when its skill disappears from the reloaded list', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-detail"]').exists()).toBe(true)
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('b')])))
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'srv' } }))
    await flushPromises()
    expect(w.find('[data-test="skill-detail"]').exists()).toBe(false)
  })

  // Review finding 1: approval is bound to the version the reviewer opened.
  it('a reload that changes the open skill shows a notice, keeps Approve off until re-open, then approves the new hash', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a', { current_hash: 'sk1-read' })], { incarnation: 3 })))
    m(api.approveSkills).mockResolvedValue(ok({ approved: 1 }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-stale"]').exists()).toBe(false)
    // A skills.changed reload replaces the entry; the chip still says Awaiting review.
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a', { current_hash: 'sk1-unread' })], { incarnation: 3 })))
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'srv' } }))
    await flushPromises()
    expect(w.get('[data-test="skill-chip-a"]').text()).toBe('Awaiting review')
    expect(w.get('[data-test="skill-stale"]').text()).toMatch(/This skill changed while you were reviewing it/)
    expect(w.get('[data-test="skill-approve"]').attributes('disabled')).toBeDefined()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(api.approveSkills).not.toHaveBeenCalled()
    // Re-opening the new version resets the review and binds it.
    await w.get('[data-test="skill-reopen"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-stale"]').exists()).toBe(false)
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(api.approveSkills).toHaveBeenCalledTimes(1)
    const [, , hashes, inc] = m(api.approveSkills).mock.calls[0]
    expect(JSON.parse(JSON.stringify(hashes))).toEqual({ a: 'sk1-unread' })
    expect(inc).toBe(3)
  })

  it('a reload with a new incarnation marks the open review stale too', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')], { incarnation: 3 })))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')], { incarnation: 4 })))
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'srv' } }))
    await flushPromises()
    expect(w.find('[data-test="skill-stale"]').exists()).toBe(true)
    expect(w.get('[data-test="skill-approve"]').attributes('disabled')).toBeDefined()
  })

  it('a reload with the same hash keeps the review and shows the new state (e.g. blocked)', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a', { blocked: true })])))
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'srv' } }))
    await flushPromises()
    expect(w.find('[data-test="skill-stale"]').exists()).toBe(false)
    expect(w.find('[data-test="skill-unblock"]').exists()).toBe(true)
  })

  // Review finding 3: a late write answer is not shown on another skill.
  it('a late approve answer after switching skill shows no notice on the new one, and still reloads', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a'), makeSkill('b')])))
    let release: (v: unknown) => void = () => {}
    m(api.approveSkills).mockReturnValue(new Promise(r => { release = r }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-row-b"]').trigger('click'); await flushPromises()
    release({ success: false, error: 'review_out_of_date: x', errorCode: 'review_out_of_date' }); await flushPromises()
    expect(w.find('[data-test="skills-notice"]').exists()).toBe(false)
    expect(w.get('[data-test="skill-name"]').text()).toBe('b')
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    expect(w.get('[data-test="skill-approve"]').attributes('disabled')).toBeUndefined()
  })

  it('a viewer part of another version reloads the listing', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    m(api.getSkillContent).mockResolvedValue(ok({ text: 'x', offset: 0, size: 1, digest: 'sha256:moved' }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-view-SKILL.md"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-viewer-error"]').text()).toMatch(/This file changed/)
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
  })
})
