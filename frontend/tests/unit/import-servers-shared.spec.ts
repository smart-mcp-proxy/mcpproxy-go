import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ImportServers from '@/components/ImportServers.vue'

vi.mock('@/services/api', () => ({
  default: {
    getCanonicalConfigPaths: vi.fn().mockResolvedValue({ success: true, data: { paths: [] } }),
    importServersFromJSON: vi.fn().mockResolvedValue({ success: true, data: { format_name: 'JSON', imported: [{ name: 'demo', protocol: 'stdio' }] } }),
    importServersFromPath: vi.fn(),
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

  it('owns the wizard detected-source preview and writes only selected servers', async () => {
    const api = (await import('@/services/api')).default as any
    api.getCanonicalConfigPaths.mockResolvedValueOnce({ success: true, data: { paths: [{ name: 'Claude Code', format: 'claude-code', path: '/tmp/claude.json', exists: true }] } })
    api.importServersFromPath.mockResolvedValueOnce({ success: true, data: { imported: [{ name: 'github', summary: 'npx github-mcp' }, { name: 'filesystem', summary: 'npx fs-mcp' }] } })
    api.importServersFromPath.mockResolvedValueOnce({ success: true, data: { summary: { imported: 1 } } })
    const wrapper = mount(ImportServers, { props: { detected: true } })
    await flushPromises()
    expect(wrapper.find('[data-test="detected-import-sources"]').text()).toContain('github')
    const checks = wrapper.findAll('input[type="checkbox"]')
    await checks[2].setValue(false)
    await wrapper.find('[data-test="detected-import-confirm"]').trigger('click')
    await flushPromises()
    expect(api.importServersFromPath).toHaveBeenCalledWith(expect.objectContaining({ server_names: ['github'], skip_quarantine: false }))
    expect(wrapper.emitted('imported')?.[0]).toEqual([1])
  })

  it('renames duplicate selected names and can bypass quarantine explicitly', async () => {
    const api = (await import('@/services/api')).default as any
    api.getCanonicalConfigPaths.mockResolvedValueOnce({ success: true, data: { paths: [
      { name: 'Claude', format: 'claude-code', path: '/tmp/claude.json', exists: true },
      { name: 'Cursor', format: 'cursor', path: '/tmp/cursor.json', exists: true },
    ] } })
    api.importServersFromPath.mockResolvedValueOnce({ success: true, data: { imported: [{ name: 'github', summary: 'claude' }] } })
    api.importServersFromPath.mockResolvedValueOnce({ success: true, data: { imported: [{ name: 'github', summary: 'cursor' }] } })
    api.importServersFromPath.mockResolvedValue({ success: true, data: { summary: { imported: 1 }, imported: [] } })
    const wrapper = mount(ImportServers, { props: { detected: true } })
    await flushPromises()
    expect(wrapper.text()).toContain('github_claude_code')
    await wrapper.find('[data-test="detected-import-quarantine"]').setValue(false)
    await wrapper.find('[data-test="detected-import-confirm"]').trigger('click')
    await flushPromises()
    expect(api.importServersFromPath).toHaveBeenCalledWith(expect.objectContaining({ path: '/tmp/claude.json', rename: { github: 'github_claude_code' }, skip_quarantine: true }))
    expect(api.importServersFromPath).toHaveBeenCalledWith(expect.objectContaining({ path: '/tmp/cursor.json', rename: { github: 'github_cursor' }, skip_quarantine: true }))
  })
})
