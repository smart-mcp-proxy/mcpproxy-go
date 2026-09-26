import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// Spec 109 PR-a review round 9 finding (medium): activeStatCard's
// `if (filterApproval.value) return null` guard (added in round 4 to stop
// Total from misreporting active on an approval-only filter) ran BEFORE the
// filterStatus checks, so it also swallowed 'enabled'/'disabled'. With an
// approval filter set, clicking the Enabled or Disabled stat card still
// applied filterStatus (the table did narrow further) but the card never
// rendered as active, and a second click re-ran the exact same
// `filterStatus.value = card` no-op instead of toggling off — a permanently
// inert control with no visible feedback once an approval filter is active.

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools Enabled/Disabled stat cards stay responsive with an approval filter set', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    ;(api.getGlobalTools as any).mockResolvedValue({
      success: true,
      data: {
        stats: { total: 3, enabled: 2, disabled: 1, pending_approval: 2 },
        tools: [
          { name: 'new_tool', server_name: 's', approval_status: 'pending', disabled: false, config_denied: false, description: '' },
          { name: 'rugpull', server_name: 's', approval_status: 'pending', disabled: true, config_denied: false, description: '' },
          { name: 'fine', server_name: 's', approval_status: 'approved', disabled: false, config_denied: false, description: '' },
        ],
      },
    })
  })

  it('highlights Enabled after it is clicked while an approval filter is active, and toggles off on a second click', async () => {
    const wrapper = mountView()
    await flushPromises()

    const select = wrapper.find('[data-test="filter-approval"]')
    await select.setValue('pending')
    await flushPromises()

    const enabledCard = wrapper.find('[data-test="stat-enabled"]')
    expect(enabledCard.classes()).not.toContain('ring-2')

    // First click: applies filterStatus='enabled' AND must now render active.
    await enabledCard.trigger('click')
    await flushPromises()

    expect(enabledCard.classes()).toContain('ring-2')
    expect(wrapper.findAll('[data-test="tool-row"]').length).toBe(1) // pending AND enabled

    // Second click on the now-active card toggles it off (and, per the
    // existing shared toggle-off gesture, clears the approval filter too).
    await enabledCard.trigger('click')
    await flushPromises()

    expect(enabledCard.classes()).not.toContain('ring-2')
    expect((select.element as HTMLSelectElement).value).toBe('')
    expect(wrapper.findAll('[data-test="tool-row"]').length).toBe(3)
  })
})
