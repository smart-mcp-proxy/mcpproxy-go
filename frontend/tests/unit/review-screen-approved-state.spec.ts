import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(),
  startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

type Tool = { name: string; approval_status: string; disabled?: boolean }

function payload(quarantined: boolean, tools: Tool[]) {
  return {
    success: true,
    data: {
      server: { name: 'fixture', transport: 'stdio', quarantined, definitions_captured: true, scan: { verdict: 'clean', risk_score: 0, coverage: 'current', tools_scanned: tools.length } },
      tools: tools.map(t => ({ description: t.name, tier: 'read', disabled: false, scan_verdict: 'clean', ...t })),
    },
  }
}

async function mountScreen(quarantined: boolean, tools: Tool[]) {
  ;(api.getServerReview as any).mockResolvedValue(payload(quarantined, tools))
  const wrapper = mount(ReviewScreen, {
    props: { serverName: 'fixture' },
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :data-to="to"><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

const approvedTools: Tool[] = [
  { name: 'a', approval_status: 'approved' },
  { name: 'b', approval_status: 'approved' },
  { name: 'c', approval_status: 'approved', disabled: true },
]

describe('ReviewScreen approved state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
    ;(api.quarantineServer as any).mockResolvedValue({ success: true })
  })

  it('shows approved and blocked tools as state, with no Approve or Reject controls', async () => {
    const wrapper = await mountScreen(false, approvedTools)
    const buttonLabels = wrapper.findAll('button').map(b => b.text())
    expect(buttonLabels).not.toContain('Approve')
    expect(buttonLabels).not.toContain('Reject')
    expect(wrapper.get('[data-test="review-tool-state-a"]').text()).toBe('Approved')
    expect(wrapper.get('[data-test="review-tool-state-c"]').text()).toBe('Blocked')
    expect(wrapper.get('[data-test="review-heading"]').text()).toBe('fixture is approved')
    expect(wrapper.get('[data-test="review-subtitle"]').text()).toContain('All 3 tools approved (1 blocked)')
    expect(wrapper.text()).not.toContain('Review tool definitions before changing what agents can call')
    expect(wrapper.get('[data-test="review-manage-tools"]').attributes('data-to')).toBe('/servers/fixture?tab=tools')
  })

  it('omits the blocked count when nothing is blocked', async () => {
    const wrapper = await mountScreen(false, approvedTools.slice(0, 2))
    expect(wrapper.get('[data-test="review-subtitle"]').text()).toContain('All 2 tools approved.')
  })

  it('confirms before quarantining to review again, then reloads', async () => {
    const wrapper = await mountScreen(false, approvedTools)
    const dialog = wrapper.get('[data-test="review-requarantine-dialog"]').element as HTMLDialogElement & { showModal: () => void; close: () => void }
    dialog.showModal = vi.fn()
    dialog.close = vi.fn()
    await wrapper.get('[data-test="review-requarantine"]').trigger('click')
    expect(dialog.showModal).toHaveBeenCalled()
    expect(api.quarantineServer).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="review-requarantine-dialog"]').text()).toContain('Agents lose access to every tool on fixture until you approve it again.')

    const loads = (api.getServerReview as any).mock.calls.length
    await wrapper.get('[data-test="review-requarantine-confirm"]').trigger('click')
    await flushPromises()
    expect(api.quarantineServer).toHaveBeenCalledWith('fixture')
    expect((api.getServerReview as any).mock.calls.length).toBe(loads + 1)
    expect(wrapper.emitted('refreshed')).toBeTruthy()
  })

  it('cancel does not quarantine', async () => {
    const wrapper = await mountScreen(false, approvedTools)
    const dialog = wrapper.get('[data-test="review-requarantine-dialog"]').element as HTMLDialogElement & { showModal: () => void; close: () => void }
    dialog.showModal = vi.fn()
    dialog.close = vi.fn()
    await wrapper.get('[data-test="review-requarantine"]').trigger('click')
    await wrapper.get('[data-test="review-requarantine-cancel"]').trigger('click')
    expect(api.quarantineServer).not.toHaveBeenCalled()
    expect(dialog.close).toHaveBeenCalled()
  })

  it('keeps Approve and Reject only on pending or changed tools of a trusted server', async () => {
    const wrapper = await mountScreen(false, [
      { name: 'a', approval_status: 'approved' },
      { name: 'b', approval_status: 'changed' },
    ])
    expect(wrapper.get('[data-test="review-heading"]').text()).toBe('Review fixture')
    expect(wrapper.get('[data-test="review-subtitle"]').text()).toContain('1 tool needs review')
    expect(wrapper.get('[data-test="review-tool-a"]').findAll('button').map(b => b.text())).toEqual([])
    expect(wrapper.get('[data-test="review-tool-a"]').get('[data-test="review-tool-state-a"]').text()).toBe('Approved')
    expect(wrapper.get('[data-test="review-tool-b"]').findAll('button').map(b => b.text())).toEqual(['Approve', 'Reject'])
    expect(wrapper.find('[data-test="review-requarantine"]').exists()).toBe(false)
  })

  it('leaves the quarantined checkbox flow unchanged', async () => {
    const wrapper = await mountScreen(true, [
      { name: 'a', approval_status: 'pending' },
      { name: 'b', approval_status: 'approved' },
    ])
    expect(wrapper.get('[data-test="review-heading"]').text()).toBe('Review fixture')
    expect(wrapper.get('[data-test="review-subtitle"]').text()).toBe('Review tool definitions before changing what agents can call.')
    expect(wrapper.find('[data-test="review-allow-a"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-allow-b"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-tool-state-a"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="review-approve-server"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-requarantine"]').exists()).toBe(false)
  })

  it('reads as approved, with a quarantine control, for a trusted server with no captured tools', async () => {
    const wrapper = await mountScreen(false, [])
    expect(wrapper.get('[data-test="review-heading"]').text()).toBe('fixture is approved')
    expect(wrapper.get('[data-test="review-subtitle"]').text()).toContain('No tool definitions')
    expect(wrapper.find('[data-test="review-requarantine"]').exists()).toBe(true)
  })

  it('keeps the review heading for a quarantined server with no tools', async () => {
    const wrapper = await mountScreen(true, [])
    expect(wrapper.get('[data-test="review-heading"]').text()).toBe('Review fixture')
    expect(wrapper.find('[data-test="review-requarantine"]').exists()).toBe(false)
  })
})
