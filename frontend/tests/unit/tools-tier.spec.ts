import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// Spec 109 FR-028 / T016 / X11: the Web Tools page's column and filter are
// labelled "Tier" (not "Risk" — risk stays the scan-score term), and use the
// server-computed `tier` field verbatim rather than deriving one locally. An
// unannotated tool shows "Unannotated", never silently as "Write" (the X11
// bug this replaces). (The `?risk=` URL alias in FR-028 belongs to the
// shared URL-filter composable a later PR in this spec owns — this page has
// no query-string filter wiring yet, on this or any other filter.)

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

describe('Tools tier column/filter (Spec 109 FR-028)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    ;(api.getGlobalTools as any).mockResolvedValue({
      success: true,
      data: {
        stats: { total: 4, enabled: 4, disabled: 0 },
        tools: [
          { name: 'delete_file', server_name: 's', approval_status: 'approved', enabled: true, description: '', tier: 'destructive' },
          { name: 'write_file', server_name: 's', approval_status: 'approved', enabled: true, description: '', tier: 'write' },
          { name: 'read_file', server_name: 's', approval_status: 'approved', enabled: true, description: '', tier: 'read' },
          { name: 'mystery_tool', server_name: 's', approval_status: 'approved', enabled: true, description: '', tier: 'unannotated' },
        ],
      },
    })
  })

  it('labels the filter and column "Tier"', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="filter-tier"]').exists()).toBe(true)
    expect(wrapper.text()).not.toMatch(/\bRisk\b/)
  })

  it('shows an unannotated tool as "Unannotated", never as "Write"', async () => {
    const wrapper = mountView()
    await flushPromises()

    const row = wrapper.findAll('[data-test="tool-row"]').find((r) => r.text().includes('mystery_tool'))
    expect(row).toBeTruthy()
    expect(row!.text()).toContain('Unannotated')
    expect(row!.text()).not.toContain('Write')
  })

  it('renders the tier values verbatim from the backend for the other tools', async () => {
    const wrapper = mountView()
    await flushPromises()

    const rows = wrapper.findAll('[data-test="tool-row"]')
    const textFor = (name: string) => rows.find((r) => r.text().includes(name))!.text()
    expect(textFor('delete_file')).toContain('Destructive')
    expect(textFor('write_file')).toContain('Write')
    expect(textFor('read_file')).toContain('Read')
  })

  it('filters to exactly the destructive tools when "Destructive" is selected', async () => {
    const wrapper = mountView()
    await flushPromises()

    const select = wrapper.find('[data-test="filter-tier"]')
    await select.setValue('destructive')
    await flushPromises()

    const rows = wrapper.findAll('[data-test="tool-row"]')
    expect(rows.length).toBe(1)
    expect(rows[0].text()).toContain('delete_file')
  })
})
