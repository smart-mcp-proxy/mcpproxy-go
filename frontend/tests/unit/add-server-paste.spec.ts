import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import PasteServer from '@/components/PasteServer.vue'

vi.mock('@/services/api', () => ({
  default: {
    importServersFromJSON: vi.fn(),
    getConfigSecrets: vi.fn(),
    getSecretRefs: vi.fn(),
    setSecret: vi.fn(),
    callTool: vi.fn(),
  },
}))
import api from '@/services/api'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: { template: '<div/>' } },
    { path: '/servers/:serverName', component: { template: '<div/>' } },
  ],
})

async function mountPaste() {
  setActivePinia(createPinia())
  vi.mocked(api.getConfigSecrets).mockResolvedValue({
    success: true,
    data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
  })
  const wrapper = mount(PasteServer, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('PasteServer', () => {
  beforeEach(() => {
    vi.mocked(api.importServersFromJSON).mockReset()
    vi.mocked(api.getConfigSecrets).mockReset()
    vi.mocked(api.callTool).mockReset()
  })

  it('detects a pasted URL and shows a preview before adding anything', async () => {
    vi.mocked(api.importServersFromJSON).mockResolvedValue({
      success: true,
      data: {
        format: 'url',
        format_name: 'URL',
        summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
        imported: [{ name: 'api', protocol: 'http', url: 'https://api.example.com/mcp', source_format: 'url', original_name: 'api', summary: 'https://api.example.com/mcp', tags: ['remote'] }],
        skipped: [],
        failed: [],
        warnings: [],
      },
    })
    const wrapper = await mountPaste()
    await wrapper.find('[data-test="paste-textarea"]').setValue('https://api.example.com/mcp')
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()

    expect(api.importServersFromJSON).toHaveBeenCalledWith({ content: 'https://api.example.com/mcp', preview: true })
    expect(api.callTool).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="paste-format"]').text()).toBe('Remote URL')
    expect(wrapper.find('[data-test="paste-tag-remote"]').exists()).toBe(true)
  })

  it('detects a pasted command line and preview never executes anything', async () => {
    vi.mocked(api.importServersFromJSON).mockResolvedValue({
      success: true,
      data: {
        format: 'command',
        format_name: 'Command Line',
        summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
        imported: [{
          name: 'server-filesystem', protocol: 'stdio', command: 'npx', args: ['-y', '@modelcontextprotocol/server-filesystem', '/tmp'],
          source_format: 'command', original_name: 'server-filesystem',
          summary: 'npx -y @modelcontextprotocol/server-filesystem /tmp', tags: ['local process'],
        }],
        skipped: [],
        failed: [],
        warnings: [],
      },
    })
    const wrapper = await mountPaste()
    await wrapper.find('[data-test="paste-textarea"]').setValue('npx -y @modelcontextprotocol/server-filesystem /tmp')
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()

    expect(wrapper.find('[data-test="paste-tag-local process"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="paste-summary"]').text()).toBe('npx -y @modelcontextprotocol/server-filesystem /tmp')
    // Preview only — the add button hasn't been clicked, so nothing was added.
    expect(api.callTool).not.toHaveBeenCalled()
  })

  it('shows a secret toggle for a detected env var and commits it through Add', async () => {
    vi.mocked(api.importServersFromJSON).mockResolvedValue({
      success: true,
      data: {
        format: 'command',
        format_name: 'Command Line',
        summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
        imported: [{
          name: 'github', protocol: 'stdio', command: 'uvx', args: ['mcp-server-github'],
          source_format: 'command', original_name: 'github',
          summary: 'uvx mcp-server-github', tags: ['local process', 'needs secret'],
          env: [{ name: 'GITHUB_TOKEN', value_present: true, secret_like: true, empty_or_placeholder: false }],
        }],
        skipped: [],
        failed: [],
        warnings: [],
      },
    })
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '', reference: '' } })
    vi.mocked(api.callTool).mockResolvedValue({ success: true, data: {} })

    const wrapper = await mountPaste()
    await wrapper.find('[data-test="paste-textarea"]').setValue('uvx mcp-server-github')
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()

    expect(wrapper.find('[data-test="secret-toggle-env-GITHUB_TOKEN"]').exists()).toBe(true)
    await wrapper.find('[data-test="secret-toggle-value-input"]').setValue('sk-live-abc123')
    await wrapper.find('[data-test="paste-add-button"]').trigger('click')
    await flushPromises()

    expect(api.setSecret).toHaveBeenCalledWith('github-env-github-token', 'sk-live-abc123')
    expect(api.callTool).toHaveBeenCalledWith(
      'upstream_servers',
      expect.objectContaining({ env_json: JSON.stringify({ GITHUB_TOKEN: '${keyring:github-env-github-token}' }) })
    )
  })
})
