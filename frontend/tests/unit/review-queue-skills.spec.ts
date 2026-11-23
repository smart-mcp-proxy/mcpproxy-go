import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createPinia } from 'pinia'
import Review from '@/views/Review.vue'
import api from '@/services/api'
import type { ReviewQueueRow } from '@/types'
import { isActiveBlocker, reviewQueueRowLink, reviewQueueRowSubtitle, sortReviewRows, summarizeReviewQueue } from '@/utils/reviewQueue'
import { AttentionKindSkillReview } from '@/types'

// Spec 125 T031/T045: held skills in the review queue. The core sends skill
// counts and skill_review rows to administrators only; the Web UI shows them
// and links a skill-only row to the server's Skills tab.

vi.mock('@/services/api', () => ({ default: { getReviewQueue: vi.fn(), listScanHistory: vi.fn() } }))

const skillOnly: ReviewQueueRow = { server: 'io.github.o/hf', kind: 'skill_review', quarantined: false, enabled: true, skills_pending: 3, skills_changed: 1, since: '2026-11-01T00:00:00Z' }
const toolsAndSkills: ReviewQueueRow = { server: 'alpha', kind: 'tool_review', quarantined: false, enabled: true, pending: 1, changed: 0, skills_pending: 2 }
const toolsOnly: ReviewQueueRow = { server: 'bravo', kind: 'tool_review', quarantined: false, enabled: true, pending: 1, changed: 1 }

describe('review queue rows with held skills (helpers)', () => {
  it('exports the skill_review attention kind', () => {
    expect(AttentionKindSkillReview).toBe('skill_review')
  })

  it('words a row: tools, skills, or both', () => {
    expect(reviewQueueRowSubtitle(toolsOnly)).toBe('1 new · 1 changed')
    expect(reviewQueueRowSubtitle(skillOnly)).toBe('4 skills awaiting review · 1 changed')
    expect(reviewQueueRowSubtitle({ ...skillOnly, skills_pending: 1, skills_changed: 0 })).toBe('1 skill awaiting review')
    expect(reviewQueueRowSubtitle(toolsAndSkills)).toBe('1 new · 0 changed · 2 skills awaiting review')
    expect(reviewQueueRowSubtitle({ server: 'q', kind: 'server_review', quarantined: true })).toBe('Server awaiting review')
  })

  it('links a skill-only row to the filtered Skills tab and a tool row to its review', () => {
    expect(reviewQueueRowLink(skillOnly)).toEqual({ path: '/servers/io.github.o%2Fhf', query: { tab: 'skills', state: 'needs-review' } })
    expect(reviewQueueRowLink(toolsAndSkills, 'pending')).toEqual({ path: '/review/alpha', query: { change: 'pending' } })
    expect(reviewQueueRowLink(toolsOnly)).toEqual({ path: '/review/bravo', query: {} })
  })

  it('counts a skill-only row as an active blocker and in the summary', () => {
    expect(isActiveBlocker(skillOnly)).toBe(true)
    expect(summarizeReviewQueue([skillOnly, toolsOnly])).toEqual({ total: 2, active: 2, deferred: 0 })
  })

  it('weighs held skills in the impact sort', () => {
    const many = { ...skillOnly, server: 'zeta', skills_pending: 10, skills_changed: 0 }
    expect(sortReviewRows([toolsOnly, many], 'impact').map(r => r.server)).toEqual(['zeta', 'bravo'])
  })
})

describe('Review queue page with held skills', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  })

  async function open(path = '/review') {
    ;(api.getReviewQueue as any).mockResolvedValue({ success: true, data: { count: 3, servers: [toolsAndSkills, toolsOnly, skillOnly] } })
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/review', component: Review }, { path: '/review/:server', component: Review },
      { path: '/servers/:serverName', component: { template: '<div />' } },
    ] })
    await router.push(path); await router.isReady()
    const wrapper = mount(Review, { global: { plugins: [createPinia(), router] } }); await flushPromises()
    return wrapper
  }

  it('shows the skill counts and filters the Skills tab from both queue links', async () => {
    const wrapper = await open()
    expect(wrapper.get('[data-test="review-count-badge"]').text()).toBe('3')
    const row = wrapper.get('[data-test="servers-review-row-io.github.o/hf"]')
    expect(row.text()).toContain('4 skills awaiting review · 1 changed')
    expect(wrapper.get('[data-test="servers-review-link-io.github.o/hf"]').attributes('href')).toBe('/servers/io.github.o%2Fhf?tab=skills&state=needs-review')
    expect(wrapper.get('[data-test="servers-review-row-alpha"]').text()).toContain('2 skills awaiting review')
    expect(wrapper.get('[data-test="servers-review-link-alpha"]').attributes('href')).toBe('/review/alpha')
    expect(wrapper.get('[data-test="servers-review-skills-link-alpha"]').attributes('href')).toBe('/servers/alpha?tab=skills&state=needs-review')
    expect(wrapper.find('[data-test="servers-review-skills-link-bravo"]').exists()).toBe(false)
  })

  it('keeps the tool change filter about tools: a skill-only row is not a pending tool', async () => {
    const wrapper = await open('/review?change=pending')
    expect(wrapper.find('[data-test="servers-review-row-alpha"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-review-row-io.github.o/hf"]').exists()).toBe(false)
  })

  it('finds a skill-only row by search', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-search"]').setValue('hf')
    expect(wrapper.find('[data-test="servers-review-row-io.github.o/hf"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-review-row-bravo"]').exists()).toBe(false)
  })
})
