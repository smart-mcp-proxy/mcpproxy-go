import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SkillsPanel from '@/components/skills/SkillsPanel.vue'
import api from '@/services/api'
import { createPinia, setActivePinia } from 'pinia'
import { connectSkillsSSE, makeListing, makeSkill, ok } from './fixtures/skills125'

vi.mock('@/services/api', () => ({ default: {
  getServerSkills: vi.fn(), refreshServerSkills: vi.fn(), getSkillContent: vi.fn(),
  approveSkills: vi.fn(), blockSkills: vi.fn(), unblockSkills: vi.fn(),
} }))

const m = (f: unknown) => f as ReturnType<typeof vi.fn>
const mountPanel = (serverName = 'srv') => mount(SkillsPanel, { props: { serverName } })
// Round-3 finding 1: every skills.changed goes through the real store's re-dispatch of the core's frame.
let emitSkillsChanged: (server: string) => void = () => { throw new Error('not connected') }

describe('SkillsPanel', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    setActivePinia(createPinia())
    emitSkillsChanged = await connectSkillsSSE(api)
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

  // Round-2 finding 4: the backend sends refresh_superseded (never refresh_in_progress),
  // and the notice says the list was reloaded, so a failed refresh reloads it.
  it('refresh replaces the listing; a 409 refresh_superseded shows a notice and reloads the listing', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    m(api.refreshServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a'), makeSkill('new')])))
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-row-new"]').exists()).toBe(true)
    expect(api.getServerSkills).toHaveBeenCalledTimes(1)
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a'), makeSkill('after')])))
    m(api.refreshServerSkills).mockResolvedValueOnce({ success: false, error: 'server srv reconnected during the refresh', errorCode: 'refresh_superseded' })
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/reconnected during the refresh/i)
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/reloaded/i)
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    expect(w.find('[data-test="skill-row-after"]').exists()).toBe(true)
  })

  it('a refresh failing with skills_not_supported or a 500 also reloads the listing', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    m(api.refreshServerSkills).mockResolvedValueOnce({ success: false, error: 'server srv does not announce the skills extension', errorCode: 'skills_not_supported' })
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/does not announce the skills extension/i)
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    m(api.refreshServerSkills).mockResolvedValueOnce({ success: false, error: 'boom' })
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-notice"]').text()).toContain('boom')
    expect(api.getServerSkills).toHaveBeenCalledTimes(3)
  })

  // Round-2 finding 5: a write notice belongs to the review it answered.
  it('opening another skill clears the write notice', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a'), makeSkill('b')])))
    m(api.approveSkills).mockResolvedValue(ok({ approved: 1 }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-notice"]').text()).toMatch(/approved/i)
    await w.get('[data-test="skill-row-b"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skills-notice"]').exists()).toBe(false)
  })

  it('switching skill after the write answer but before its reload finishes shows no notice on the new one', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a'), makeSkill('b')])))
    m(api.approveSkills).mockResolvedValue(ok({ approved: 1 }))
    let releaseReload: (v: unknown) => void = () => {}
    m(api.getServerSkills).mockReturnValueOnce(new Promise(r => { releaseReload = r }))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    // The answer is in; the reload is still in flight.
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    await w.get('[data-test="skill-row-b"]').trigger('click'); await flushPromises()
    releaseReload(ok(makeListing([makeSkill('a', { status: 'approved' }), makeSkill('b')]))); await flushPromises()
    expect(w.get('[data-test="skill-name"]').text()).toBe('b')
    expect(w.find('[data-test="skills-notice"]').exists()).toBe(false)
  })

  // Round-2 finding 11: the agent-token listing shape has no scan block.
  it('renders an entry without a scan block (agent shape) without throwing', async () => {
    const s = makeSkill('a', { status: 'approved' }) as any
    delete s.scan
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([s])))
    const w = mountPanel(); await flushPromises()
    expect(w.get('[data-test="skill-chip-a"]').text()).toBe('Approved')
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-detail"]').exists()).toBe(true)
  })

  it('reloads on mcpproxy:skills-changed for its own server only', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    emitSkillsChanged('other')
    await flushPromises()
    expect(api.getServerSkills).toHaveBeenCalledTimes(1)
    emitSkillsChanged('srv')
    await flushPromises()
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
    w.unmount()
    emitSkillsChanged('srv')
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
    emitSkillsChanged('srv')
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
    emitSkillsChanged('srv')
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
    emitSkillsChanged('srv')
    await flushPromises()
    expect(w.find('[data-test="skill-stale"]').exists()).toBe(true)
    expect(w.get('[data-test="skill-approve"]').attributes('disabled')).toBeDefined()
  })

  it('a reload with the same hash keeps the review and shows the new state (e.g. blocked)', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a', { blocked: true })])))
    emitSkillsChanged('srv')
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
  // Round-3 finding 2: a thrown request (network error) never leaves an
  // unhandled rejection or a stuck spinner/disabled button.
  it('a thrown load shows an error with a retry and stops loading', async () => {
    m(api.getServerSkills).mockRejectedValueOnce(new Error('network'))
    const w = mountPanel(); await flushPromises()
    expect(w.get('[data-test="skills-error"]').text()).toContain('network')
    expect(w.find('.loading-spinner').exists()).toBe(false)
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')])))
    await w.get('[data-test="skills-retry"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="skill-row-a"]').exists()).toBe(true)
    expect(w.find('[data-test="skills-error"]').exists()).toBe(false)
  })

  it('a thrown reload keeps the listing on screen and shows the error', async () => {
    m(api.getServerSkills).mockResolvedValueOnce(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    m(api.getServerSkills).mockRejectedValueOnce(new Error('network'))
    emitSkillsChanged('srv'); await flushPromises()
    expect(w.find('[data-test="skill-row-a"]').exists()).toBe(true)
    expect(w.get('[data-test="skills-load-error"]').text()).toContain('network')
  })

  it('a thrown refresh resets the Refresh button, says so and reloads', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    const w = mountPanel(); await flushPromises()
    m(api.refreshServerSkills).mockRejectedValueOnce(new Error('network'))
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skills-refresh"]').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-test="skills-notice"]').text()).toContain('network')
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
  })

  it('a thrown approve resets busy, shows the error and reloads', async () => {
    m(api.getServerSkills).mockResolvedValue(ok(makeListing([makeSkill('a')])))
    m(api.approveSkills).mockRejectedValueOnce(new Error('network'))
    const w = mountPanel(); await flushPromises()
    await w.get('[data-test="skill-row-a"]').trigger('click'); await flushPromises()
    await w.get('[data-test="skill-approve"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="skill-approve"]').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-test="skills-notice"]').text()).toContain('network')
    expect(api.getServerSkills).toHaveBeenCalledTimes(2)
  })

  it('a thrown answer for the previous server changes nothing on the new one', async () => {
    let failA: (e: unknown) => void = () => {}
    let failRefresh: (e: unknown) => void = () => {}
    m(api.getServerSkills).mockImplementation((name: string) =>
      name === 'a' ? new Promise((_, rej) => { failA = rej }) : Promise.resolve(ok(makeListing([makeSkill('only_b')]))))
    const w = mountPanel('a')
    await w.setProps({ serverName: 'b' }); await flushPromises()
    m(api.refreshServerSkills).mockReturnValueOnce(new Promise((_, rej) => { failRefresh = rej }))
    await w.get('[data-test="skills-refresh"]').trigger('click'); await flushPromises()
    failA(new Error('late a')); await flushPromises()
    expect(w.find('[data-test="skills-error"]').exists()).toBe(false)
    expect(w.find('[data-test="skills-load-error"]').exists()).toBe(false)
    expect(w.find('[data-test="skill-row-only_b"]').exists()).toBe(true)
    // The refresh for b is still in flight: its button stays busy.
    expect(w.get('[data-test="skills-refresh"]').attributes('disabled')).toBeDefined()
    failRefresh(new Error('net')); await flushPromises()
    expect(w.get('[data-test="skills-refresh"]').attributes('disabled')).toBeUndefined()
  })
})
