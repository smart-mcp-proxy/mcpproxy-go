import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const review = (definitionsCaptured = true) => ({
  success: true,
  data: {
    server: { name: 'fixture', transport: 'stdio', command: 'node ./fixture.js', trust_mode: 'manual', source_registry_id: 'official', source_registry_provenance: 'catalog', quarantined: true, definitions_captured: definitionsCaptured },
    tools: [
      { name: 'read_file', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true },
      { name: 'write_file', description: 'write', tier: 'write', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false },
      { name: 'remove_file', description: 'remove', tier: 'destructive', approval_status: 'changed', disabled: false, scan_verdict: 'warnings', default_allowed: false, diff: { description: '- old\n+ new' } },
      { name: 'implicit', description: 'implicit', tier: 'unannotated', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false },
      { name: 'legacy', description: 'legacy', tier: 'unknown', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false },
    ],
  },
})

async function mountScreen(definitionsCaptured = true) {
  ;(api.getServerReview as any).mockResolvedValue(review(definitionsCaptured))
  const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

describe('ReviewScreen (T086)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    ;(api.discoverServerTools as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
		;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
		;(api.scanAll as any).mockResolvedValue({ success: true, data: { status: 'running', total: 1, completed: 0, running: 1 } })
  })

  it('renders the review payload, canonical tier counts, and a changed diff', async () => {
    const wrapper = await mountScreen()
    expect(wrapper.get('[data-test="review-tier-counts"]').text()).toContain('destructive1')
    expect(wrapper.get('[data-test="review-tier-counts"]').text()).toContain('unannotated1')
    expect(wrapper.get('[data-test="review-tool-diff"]').text()).toContain('+ new')
    expect(wrapper.get('[data-test="review-server-identity"]').text()).toContain('node ./fixture.js')
    expect(wrapper.get('[data-test="review-server-identity"]').text()).toContain('manual')
    expect(wrapper.get('[data-test="review-server-identity"]').text()).toContain('official')
  })

  it('sends unchecked tools as the block selection', async () => {
    const wrapper = await mountScreen()
    // Only read_file starts checked (default_allowed); checking write_file takes it off the block list.
    await wrapper.get('[data-test="review-allow-write_file"]').setValue(true)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['remove_file', 'implicit', 'legacy'])
  })

  it('offers definition capture and asks for a blind-approval confirmation', async () => {
    const wrapper = await mountScreen(false)
    const dialog = wrapper.get('dialog').element as HTMLDialogElement & { showModal: () => void }
    dialog.showModal = vi.fn()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    expect(dialog.showModal).toHaveBeenCalled()
    await wrapper.get('[data-test="review-no-definitions"] button').trigger('click')
    await flushPromises()
    expect(api.discoverServerTools).toHaveBeenCalledWith('fixture')
    expect(api.getServerReview).toHaveBeenCalledTimes(2)
    window.dispatchEvent(new CustomEvent('mcpproxy:scan-settled', { detail: { server_name: 'other' } }))
    await flushPromises()
    expect(api.getServerReview).toHaveBeenCalledTimes(2)
    window.dispatchEvent(new CustomEvent('mcpproxy:scan-settled', { detail: { server_name: 'fixture' } }))
    await flushPromises()
    expect(api.discoverServerTools).toHaveBeenCalledWith('fixture')
    expect(api.getServerReview).toHaveBeenCalledTimes(3)
  })

  it('does not let a changed-only filter alter hidden tool selections and keeps scan history on the detail', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture', change: 'changed' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(wrapper.findAll('article[data-test^="review-tool-"]')).toHaveLength(1)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['write_file', 'remove_file', 'implicit', 'legacy'])
    expect(api.listScanHistory).toHaveBeenCalled()
  })

  it('keeps the route change restriction when only the server name changes, and All states still clears it', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture', change: 'changed' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    const select = () => wrapper.get('[data-test="review-filter-state"]').element as HTMLSelectElement
    expect(select().value).toBe('changed')
    const before = wrapper.findAll('article[data-test^="review-tool-"]').length
    await wrapper.setProps({ serverName: 'other' })
    await flushPromises()
    expect(select().value).toBe('changed')
    expect(wrapper.findAll('article[data-test^="review-tool-"]').length).toBe(before)
    await wrapper.get('[data-test="review-clear-filters"]').trigger('click')
    expect(select().value).toBe('all')
  })

  it('offers an explicit forced retry only after a dangerous approval rejection', async () => {
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous baseline finding' }).mockResolvedValueOnce({ success: true })
    const wrapper = await mountScreen()
    const forceDialog = wrapper.findAll('dialog')[1].element as HTMLDialogElement & { showModal: () => void }
    forceDialog.showModal = vi.fn()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(forceDialog.showModal).toHaveBeenCalled()
    await wrapper.findAll('dialog')[1].get('button.btn-error').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenNthCalledWith(2, 'fixture', true, ['write_file', 'remove_file', 'implicit', 'legacy'])
  })

	it('keeps fleet scan start and progress controls reachable from the review flow', async () => {
		const wrapper = await mountScreen()
		await wrapper.get('[data-test="scan-all-button"]').trigger('click')
		await flushPromises()
		expect(api.scanAll).toHaveBeenCalled()
		expect(wrapper.get('[data-test="scan-queue-progress"]').text()).toContain('1')
	})

	it('restores fleet scan polling and disables duplicate starts after reopening review', async () => {
		;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'running', total: 2, completed: 1, running: 1 } })
		const wrapper = await mountScreen()
		expect(wrapper.get('[data-test="scan-queue-progress"]').text()).toContain('1/2')
		expect((wrapper.get('[data-test="scan-all-button"]').element as HTMLButtonElement).disabled).toBe(true)
	})
})
