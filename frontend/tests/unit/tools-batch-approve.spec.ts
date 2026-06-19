import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

// MCP-2918: batch Approve / Reject on the global Tools page. The page spans
// multiple servers, so the actions group the multi-select by server_name and
// fan out one approveTools / blockTools call per server. These tests lock the
// group-by-server fan-out, the skip-already-approved rule, the enable/disable
// guard on the buttons, and partial-failure surfacing.

vi.mock('@/services/api', () => ({
  default: {
    getGlobalTools: vi.fn(),
    setToolEnabled: vi.fn(),
    approveTools: vi.fn(),
    blockTools: vi.fn(),
  },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

// One approved tool + pending/changed tools spread across two servers.
const TOOLS = [
  { name: 'new_a', server_name: 'alpha', approval_status: 'pending', enabled: true, description: '' },
  { name: 'changed_a', server_name: 'alpha', approval_status: 'changed', enabled: true, description: '' },
  { name: 'new_b', server_name: 'beta', approval_status: 'pending', enabled: true, description: '' },
  { name: 'ok_b', server_name: 'beta', approval_status: 'approved', enabled: true, description: '' },
]

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

async function selectAll(wrapper: any) {
  // Select every row currently rendered on the page.
  await wrapper.find('[data-test="tools-select-all"]').setValue(true)
  await flushPromises()
}

describe('Tools — batch approve / reject (multi-server fan-out)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    ;(api.getGlobalTools as any).mockResolvedValue({
      success: true,
      data: {
        stats: { total: 4, enabled: 4, disabled: 0, pending_approval: 3 },
        tools: TOOLS,
      },
    })
    ;(api.approveTools as any).mockResolvedValue({ success: true, data: { approved: 2 } })
    ;(api.blockTools as any).mockResolvedValue({ success: true, data: { blocked: 2 } })
  })

  it('disables Approve/Reject when no pending or changed tool is selected', async () => {
    const wrapper = mountView()
    await flushPromises()

    // Nothing selected → batch bar (and its buttons) not even rendered.
    expect(wrapper.find('[data-test="batch-approve"]').exists()).toBe(false)

    // Select only the already-approved tool → buttons rendered but disabled.
    const approvedTool = TOOLS.find(t => t.approval_status === 'approved')!
    const wrapperVm = wrapper.vm as any
    wrapperVm.selectedKeys.add(`${approvedTool.server_name}\x00${approvedTool.name}`)
    await flushPromises()

    const approveBtn = wrapper.find('[data-test="batch-approve"]')
    const rejectBtn = wrapper.find('[data-test="batch-reject"]')
    expect(approveBtn.exists()).toBe(true)
    expect(rejectBtn.exists()).toBe(true)
    expect((approveBtn.element as HTMLButtonElement).disabled).toBe(true)
    expect((rejectBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('enables Approve/Reject when at least one pending or changed tool is selected', async () => {
    const wrapper = mountView()
    await flushPromises()
    await selectAll(wrapper)

    const approveBtn = wrapper.find('[data-test="batch-approve"]')
    const rejectBtn = wrapper.find('[data-test="batch-reject"]')
    expect((approveBtn.element as HTMLButtonElement).disabled).toBe(false)
    expect((rejectBtn.element as HTMLButtonElement).disabled).toBe(false)
  })

  it('groups the selection by server and calls approveTools once per server with only pending/changed names', async () => {
    const wrapper = mountView()
    await flushPromises()
    await selectAll(wrapper)

    await wrapper.find('[data-test="batch-approve"]').trigger('click')
    await flushPromises()

    // One call per server that has approvable tools; the approved-only server
    // ('beta' has new_b pending + ok_b approved) still gets a call, but only
    // for its pending/changed names.
    expect((api.approveTools as any)).toHaveBeenCalledTimes(2)
    const calls = (api.approveTools as any).mock.calls
    const byServer = Object.fromEntries(calls.map((c: any[]) => [c[0], c[1]]))
    expect(Object.keys(byServer).sort()).toEqual(['alpha', 'beta'])
    expect([...byServer.alpha].sort()).toEqual(['changed_a', 'new_a'])
    expect(byServer.beta).toEqual(['new_b']) // ok_b (approved) skipped
  })

  it('calls blockTools per server on Reject', async () => {
    const wrapper = mountView()
    await flushPromises()
    await selectAll(wrapper)

    await wrapper.find('[data-test="batch-reject"]').trigger('click')
    await flushPromises()

    expect((api.blockTools as any)).toHaveBeenCalledTimes(2)
    const calls = (api.blockTools as any).mock.calls
    const byServer = Object.fromEntries(calls.map((c: any[]) => [c[0], c[1]]))
    expect([...byServer.alpha].sort()).toEqual(['changed_a', 'new_a'])
    expect(byServer.beta).toEqual(['new_b'])
  })

  it('shows a single success toast summarising tools and servers', async () => {
    const wrapper = mountView()
    await flushPromises()
    const store = useSystemStore()
    await selectAll(wrapper)

    await wrapper.find('[data-test="batch-approve"]').trigger('click')
    await flushPromises()

    const success = store.toasts.filter(t => t.type === 'success')
    expect(success.length).toBe(1)
    expect(success[0].message).toMatch(/3 tools/)
    expect(success[0].message).toMatch(/2 servers/)
  })

  it('surfaces partial failures with an error toast', async () => {
    ;(api.approveTools as any).mockImplementation((server: string) => {
      if (server === 'beta') return Promise.resolve({ success: false, error: 'boom' })
      return Promise.resolve({ success: true, data: { approved: 2 } })
    })

    const wrapper = mountView()
    await flushPromises()
    const store = useSystemStore()
    await selectAll(wrapper)

    await wrapper.find('[data-test="batch-approve"]').trigger('click')
    await flushPromises()

    const errors = store.toasts.filter(t => t.type === 'error')
    expect(errors.length).toBe(1)
    expect(errors[0].message).toMatch(/beta/)
  })
})
