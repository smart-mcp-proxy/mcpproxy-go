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
    deleteSecret: vi.fn(),
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

    expect(api.importServersFromJSON).toHaveBeenCalledWith({ content: 'https://api.example.com/mcp', preview: true, allow_paste_fallback: true })
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

    const wrapper = await mountPaste()
    await wrapper.find('[data-test="paste-textarea"]').setValue('uvx mcp-server-github')
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()

    expect(wrapper.find('[data-test="secret-toggle-env-GITHUB_TOKEN"]').exists()).toBe(true)
    await wrapper.find('[data-test="secret-toggle-value-input"]').setValue('sk-live-abc123')

    // The apply call (preview:false) resolves separately from the earlier
    // preview call, matched below by its `preview` flag.
    vi.mocked(api.importServersFromJSON).mockImplementation(async (params) => {
      if (params.preview) {
        return {
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
            skipped: [], failed: [], warnings: [],
          },
        }
      }
      return {
        success: true,
        data: {
          format: 'command', format_name: 'Command Line',
          summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
          imported: [{ name: 'github', protocol: 'stdio', source_format: 'command', original_name: 'github' }],
          skipped: [], failed: [], warnings: [],
        },
      }
    })

    await wrapper.find('[data-test="paste-add-button"]').trigger('click')
    await flushPromises()

    expect(api.setSecret).toHaveBeenCalledWith('github-env-github-token', 'sk-live-abc123')
    // Add must re-parse the ORIGINAL raw content server-side (never send the
    // preview's own — potentially redacted — url/command/args back), and
    // carry the resolved secret across via env_override, not env_json built
    // from the preview.
    expect(api.importServersFromJSON).toHaveBeenCalledWith(
      expect.objectContaining({
        content: 'uvx mcp-server-github',
        preview: false,
        server_names: ['github'],
        env_override: { GITHUB_TOKEN: '${keyring:github-env-github-token}' },
      })
    )
  })

  // Review round 1: resolveSecretFields's returned writtenRefs were computed
  // but never passed to rollbackSecrets by this surface, so a secret write
  // that succeeded right before the add-server call itself failed (e.g. a
  // duplicate name) left an orphaned keyring entry behind.
  it('rolls back the just-written secret when the add-server call fails after a successful secret write', async () => {
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
    vi.mocked(api.deleteSecret).mockResolvedValue({ success: true, data: { message: '' } })

    const wrapper = await mountPaste()
    await wrapper.find('[data-test="paste-textarea"]').setValue('uvx mcp-server-github')
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()

    await wrapper.find('[data-test="secret-toggle-value-input"]').setValue('sk-live-abc123')

    // The apply (preview:false) call fails with a duplicate-name error.
    vi.mocked(api.importServersFromJSON).mockImplementation(async (params) => {
      if (params.preview) {
        return {
          success: true,
          data: {
            format: 'command', format_name: 'Command Line',
            summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
            imported: [{
              name: 'github', protocol: 'stdio', command: 'uvx', args: ['mcp-server-github'],
              source_format: 'command', original_name: 'github',
              summary: 'uvx mcp-server-github', tags: ['local process', 'needs secret'],
              env: [{ name: 'GITHUB_TOKEN', value_present: true, secret_like: true, empty_or_placeholder: false }],
            }],
            skipped: [], failed: [], warnings: [],
          },
        }
      }
      return { success: false, error: 'a server named "github" already exists' }
    })

    await wrapper.find('[data-test="paste-add-button"]').trigger('click')
    await flushPromises()

    expect(api.setSecret).toHaveBeenCalledWith('github-env-github-token', 'sk-live-abc123')
    expect(wrapper.find('[data-test="paste-add-error"]').text()).toContain('already exists')
    expect(api.deleteSecret).toHaveBeenCalledWith('github-env-github-token')
  })

  // Review round 4 (F-A): a credential embedded directly in a URL query
  // param is masked in the preview (`api_key=••••23 (16 chars)`) for
  // display. Add must never bake that masked string into the real server —
  // it must re-post the ORIGINAL raw pasted text so the backend re-parses
  // the true, unredacted URL server-side.
  it('re-parses the original raw text on Add instead of using the redacted preview URL', async () => {
    const rawUrl = 'https://api.example.com/mcp?api_key=ghp_verysecrettoken1234'
    const redactedUrl = 'https://api.example.com/mcp?api_key=••••34 (20 chars)'
    vi.mocked(api.importServersFromJSON).mockImplementation(async (params) => {
      if (params.preview) {
        return {
          success: true,
          data: {
            format: 'url',
            format_name: 'URL',
            summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
            imported: [{ name: 'api', protocol: 'http', url: redactedUrl, source_format: 'url', original_name: 'api', summary: redactedUrl, tags: ['remote'] }],
            skipped: [], failed: [], warnings: [],
          },
        }
      }
      return {
        success: true,
        data: {
          format: 'url', format_name: 'URL',
          summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
          imported: [{ name: 'api', protocol: 'http', source_format: 'url', original_name: 'api' }],
          skipped: [], failed: [], warnings: [],
        },
      }
    })

    const wrapper = await mountPaste()
    await wrapper.find('[data-test="paste-textarea"]').setValue(rawUrl)
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()

    // The preview correctly shows the masked value (nothing regressed there).
    expect(wrapper.find('[data-test="paste-summary"]').text()).toBe(redactedUrl)

    await wrapper.find('[data-test="paste-add-button"]').trigger('click')
    await flushPromises()

    const applyCall = vi.mocked(api.importServersFromJSON).mock.calls.find(([p]) => p.preview === false)
    expect(applyCall).toBeDefined()
    expect(applyCall![0].content).toBe(rawUrl)
    expect(applyCall![0].content).not.toContain('••••')
  })
})
