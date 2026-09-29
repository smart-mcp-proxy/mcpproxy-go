import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ScannerSettings from '@/components/ScannerSettings.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: { listScanners: vi.fn(), installScanner: vi.fn(), removeScanner: vi.fn(), configureScanner: vi.fn() } }))
describe('ScannerSettings (T088)', () => {
  beforeEach(() => { vi.clearAllMocks(); ;(api.listScanners as any).mockResolvedValue({ success: true, data: [{ id: 'deep', name: 'Deep', description: 'optional', status: 'available' }] }); ;(api.installScanner as any).mockResolvedValue({ success: true }) })
  it('keeps scanner controls under Settings security and toggles a scanner', async () => {
    const wrapper = mount(ScannerSettings); await flushPromises()
    expect(wrapper.get('[data-test="settings-scanners"]').text()).toContain('Scanners')
    await wrapper.get('button.btn-primary').trigger('click')
    expect(api.installScanner).toHaveBeenCalledWith('deep')
  })

  it('retains scanner retry, optional/custom environment and Docker image override controls', async () => {
    ;(api.listScanners as any).mockResolvedValue({ success: true, data: [{ id: 'deep', name: 'Deep', description: 'optional', status: 'error', docker_image: 'vendor/deep', optional_env: [{ key: 'MODE', label: 'Mode' }] }] })
    const wrapper = mount(ScannerSettings); await flushPromises()
    expect(wrapper.text()).toContain('Retry')
    expect(wrapper.text()).toContain('Docker Image')
    expect(wrapper.text()).toContain('Add Custom Variable')
    await wrapper.get('[data-test="scanner-retry-deep"]').trigger('click')
    expect(api.installScanner).toHaveBeenCalledWith('deep')
  })
})
