import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// T1 (v0.36.0 feedback) established that the "Pending Approval" stat counts
// tools that are pending OR changed. Spec 109 FR-027 then turned that stat
// into a plain link to the review queue (`/review`) instead of an in-page
// filter toggle — "needs review" means something to go act on elsewhere, not
// one more way to slice this table — so this now pins the count and the link
// target instead of a click-to-filter interaction.

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools — Needs review stat (Spec 109 FR-027)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    ;(api.getGlobalTools as any).mockResolvedValue({
      success: true,
      data: {
        stats: { total: 2, enabled: 2, disabled: 0, pending_approval: 1 },
        tools: [
          { name: 'hf_doc_search', server_name: 'hugginface', approval_status: 'changed', enabled: true, description: 'changed tool' },
          { name: 'list_repos', server_name: 'github', approval_status: 'approved', enabled: true, description: 'ok' },
        ],
      },
    })
  })

  it('counts pending + changed tools (both are "needs review")', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="stat-pending"] .stat-value').text().trim()).toBe('1')
  })

  it('labels the card "Needs review" and links to /review, not an in-page filter', async () => {
    const wrapper = mountView()
    await flushPromises()

    const card = wrapper.find('[data-test="stat-pending"]')
    expect(card.text()).toContain('Needs review')
    expect(card.attributes('to')).toBe('/review')

    // Clicking it must not mutate the Approval filter or the visible rows —
    // it is a navigation, not a toggle.
    await card.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-test="tool-row"]').length).toBe(2)
  })
})
