import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// Spec 109 PR-a review round 4 (low, first flagged round 1/2, never actually
// fixed): activeStatCard only inspected filterStatus, defaulting to 'total'
// whenever filterStatus was empty — even when filterApproval alone was
// filtering the table. Setting Approval to e.g. "New, needs review" rang the
// Total stat card as active even though the table below no longer shows all
// tools. Total must only read as active when NO filter (status or approval)
// is applied.

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools stat cards do not mislabel an approval-only filter as Total', () => {
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

  it('does not highlight Total once an approval filter narrows the table', async () => {
    const wrapper = mountView()
    await flushPromises()

    const totalCard = wrapper.find('[data-test="stat-total"]')
    expect(totalCard.classes()).toContain('ring-2')

    const select = wrapper.find('[data-test="filter-approval"]')
    await select.setValue('pending')
    await flushPromises()

    expect(wrapper.findAll('[data-test="tool-row"]').length).toBe(1)
    expect(totalCard.classes()).not.toContain('ring-2')
  })
})
