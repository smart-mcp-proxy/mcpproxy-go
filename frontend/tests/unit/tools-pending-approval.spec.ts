import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// T1 (v0.36.0 feedback): the "Pending Approval" stat counts tools that are
// pending OR changed, but the Approval=Pending filter matched only "pending",
// so an instance whose sole awaiting tool was "changed" (a rug-pull) showed a
// non-zero stat with an EMPTY table. This reproduces that exact data shape —
// one approved tool + one CHANGED tool, pending_approval = 1 — and locks the
// fix: clicking the stat applies an "awaiting" filter that surfaces the row.

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools — Pending Approval count matches the filtered table', () => {
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

  it('shows the changed tool (not an empty table) when the Pending Approval stat is clicked', async () => {
    const wrapper = mountView()
    await flushPromises()

    // Stat reflects the changed tool.
    expect(wrapper.find('[data-test="stat-pending"] .stat-value').text().trim()).toBe('1')

    // Before the fix this produced "No matching tools"; now it lists the changed row.
    await wrapper.find('[data-test="stat-pending"]').trigger('click')
    await flushPromises()

    const rows = wrapper.findAll('[data-test="tool-row"]')
    expect(rows.length).toBe(1)
    expect(rows[0].text()).toContain('hf_doc_search')
  })

  it('the Awaiting-approval filter matches both pending and changed', async () => {
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
    const wrapper = mountView()
    await flushPromises()
    await wrapper.find('[data-test="stat-pending"]').trigger('click')
    await flushPromises()
    const rows = wrapper.findAll('[data-test="tool-row"]')
    expect(rows.length).toBe(2) // pending + changed, not approved
    const text = rows.map(r => r.text()).join(' ')
    expect(text).toContain('new_tool')
    expect(text).toContain('rugpull')
    expect(text).not.toContain('fine')
  })
})
