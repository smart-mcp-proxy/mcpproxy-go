import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// Spec 109 PR-a review round 1 (medium): selectStatCard's toggle-to-total
// branch used to clear both filterStatus AND filterApproval. The PR dropped
// the filterApproval reset, so setting an Approval filter and then clicking
// the Total stat card (the established "reset" gesture — see clearFilters,
// which clears both) silently leaves the approval filter applied with no
// visible way to tell from the stat row.

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools Total stat card resets the approval filter too (review round 1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    ;(api.getGlobalTools as any).mockResolvedValue({
      success: true,
      data: {
        stats: { total: 3, enabled: 3, disabled: 0, pending_approval: 2 },
        tools: [
          { name: 'new_tool', server_name: 's', approval_status: 'pending', enabled: true, description: '' },
          { name: 'rugpull', server_name: 's', approval_status: 'changed', enabled: true, description: '' },
          { name: 'fine', server_name: 's', approval_status: 'approved', enabled: true, description: '' },
        ],
      },
    })
  })

  it('clears an active approval filter when the Total card is clicked', async () => {
    const wrapper = mountView()
    await flushPromises()

    const select = wrapper.find('[data-test="filter-approval"]')
    await select.setValue('pending')
    await flushPromises()
    expect(wrapper.findAll('[data-test="tool-row"]').length).toBe(1)

    await wrapper.find('[data-test="stat-total"]').trigger('click')
    await flushPromises()

    expect((select.element as HTMLSelectElement).value).toBe('')
    expect(wrapper.findAll('[data-test="tool-row"]').length).toBe(3)
  })
})
