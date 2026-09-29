import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), startScan: vi.fn(), listScanHistory: vi.fn(),
} }))

const review = (definitionsCaptured = true) => ({
  success: true,
  data: {
    server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: definitionsCaptured },
    tools: [
      { name: 'read_file', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean' },
      { name: 'write_file', description: 'write', tier: 'write', approval_status: 'pending', disabled: false, scan_verdict: 'clean' },
      { name: 'remove_file', description: 'remove', tier: 'destructive', approval_status: 'changed', disabled: false, scan_verdict: 'warnings', diff: { description: '- old\n+ new' } },
      { name: 'implicit', description: 'implicit', tier: 'unannotated', approval_status: 'pending', disabled: false, scan_verdict: 'clean' },
      { name: 'legacy', description: 'legacy', tier: 'unknown', approval_status: 'pending', disabled: false, scan_verdict: 'clean' },
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
    ;(api.startScan as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  })

  it('renders the review payload, canonical tier counts, and a changed diff', async () => {
    const wrapper = await mountScreen()
    expect(wrapper.get('[data-test="review-tier-counts"]').text()).toContain('destructive1')
    expect(wrapper.get('[data-test="review-tier-counts"]').text()).toContain('unannotated1')
    expect(wrapper.get('[data-test="review-tool-diff"]').text()).toContain('+ new')
  })

  it('sends unchecked tools as the block selection', async () => {
    const wrapper = await mountScreen()
    await wrapper.get('[data-test="review-allow-remove_file"]').setValue(false)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['remove_file'])
  })

  it('offers definition capture and asks for a blind-approval confirmation', async () => {
    const wrapper = await mountScreen(false)
    const dialog = wrapper.get('dialog').element as HTMLDialogElement & { showModal: () => void }
    dialog.showModal = vi.fn()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    expect(dialog.showModal).toHaveBeenCalled()
    await wrapper.get('[data-test="review-no-definitions"] button').trigger('click')
    expect(api.startScan).toHaveBeenCalledWith('fixture')
    expect(wrapper.get('[data-test="review-no-definitions"]').text()).toContain('Fetching')
    window.dispatchEvent(new CustomEvent('mcpproxy:scan-settled', { detail: { server_name: 'other' } }))
    await flushPromises()
    expect(api.getServerReview).toHaveBeenCalledTimes(1)
    window.dispatchEvent(new CustomEvent('mcpproxy:scan-settled', { detail: { server_name: 'fixture' } }))
    await flushPromises()
    expect(api.getServerReview).toHaveBeenCalledTimes(2)
  })

  it('blocks tools hidden by a changed-only filter and keeps scan history on the detail', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture', change: 'changed' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(wrapper.findAll('article[data-test^="review-tool-"]')).toHaveLength(1)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['read_file', 'write_file', 'implicit', 'legacy'])
    expect(api.listScanHistory).toHaveBeenCalled()
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
    expect(api.securityApprove).toHaveBeenNthCalledWith(2, 'fixture', true, [])
  })
})
