import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// Spec 109 FR-027: one review-state vocabulary. The Web UI Tools filter
// offers exactly Approved / New, needs review / Changed, needs review — no
// `awaiting` value (it duplicated pending+changed under a third name) — and
// the "Needs review" stat links to a registered `/review` route (T026a),
// never the 404 catch-all.

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools approval filter vocabulary (Spec 109 FR-027 / T010)', () => {
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

  it('offers exactly Approved / New, needs review / Changed, needs review — no "awaiting"', async () => {
    const wrapper = mountView()
    await flushPromises()

    const options = wrapper.find('[data-test="filter-approval"]').findAll('option')
    const values = options.map((o) => o.attributes('value'))
    const labels = options.map((o) => o.text())

    expect(values).toEqual(['', 'approved', 'pending', 'changed'])
    expect(labels).toEqual(['All', 'Approved', 'New, needs review', 'Changed, needs review'])
    expect(values).not.toContain('awaiting')
    expect(labels.join(' ')).not.toMatch(/awaiting/i)
  })

  it('filters to exactly the pending tools when "New, needs review" is selected', async () => {
    const wrapper = mountView()
    await flushPromises()

    const select = wrapper.find('[data-test="filter-approval"]')
    await select.setValue('pending')
    await flushPromises()

    const rows = wrapper.findAll('[data-test="tool-row"]')
    expect(rows.length).toBe(1)
    expect(rows[0].text()).toContain('new_tool')
  })

  it('filters to exactly the changed tools when "Changed, needs review" is selected', async () => {
    const wrapper = mountView()
    await flushPromises()

    const select = wrapper.find('[data-test="filter-approval"]')
    await select.setValue('changed')
    await flushPromises()

    const rows = wrapper.findAll('[data-test="tool-row"]')
    expect(rows.length).toBe(1)
    expect(rows[0].text()).toContain('rugpull')
  })

  it('the "Needs review" stat links to /review, which resolves to a registered route', async () => {
    const wrapper = mountView()
    await flushPromises()

    const stat = wrapper.find('[data-test="stat-pending"]')
    expect(stat.text()).toContain('Needs review')
    expect(stat.attributes('to')).toBe('/review')

    const appRouter = (await import('@/router')).default
    const resolved = appRouter.resolve('/review')
    expect(resolved.name).not.toBe('not-found')
  })
})
