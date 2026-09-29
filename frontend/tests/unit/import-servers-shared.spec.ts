import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ImportServers from '@/components/ImportServers.vue'

vi.mock('@/services/api', () => ({
  default: {
    getCanonicalConfigPaths: vi.fn().mockResolvedValue({ success: true, data: { paths: [] } }),
    importServersFromJSON: vi.fn().mockResolvedValue({ success: true, data: { format_name: 'JSON', imported: [{ name: 'demo', protocol: 'stdio' }] } }),
  },
}))

describe('ImportServers', () => {
  it('owns preview and selection before an import is committed', async () => {
    const wrapper = mount(ImportServers)
    await flushPromises()
    await wrapper.find('[data-test="import-content-textarea"]').setValue('{"mcpServers":{"demo":{}}}')
    await wrapper.find('[data-test="import-preview-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="import-preview"]').text()).toContain('demo')
    expect(wrapper.find('[data-test="import-confirm-button"]').text()).toContain('Import 1 server')
  })
})
