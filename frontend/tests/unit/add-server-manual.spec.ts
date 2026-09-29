import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import ManualServerForm from '@/components/ManualServerForm.vue'

vi.mock('@/services/api', () => ({
  default: {
    getConfigSecrets: vi.fn(),
    getSecretRefs: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
    callTool: vi.fn(),
    getServers: vi.fn(),
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

async function mountManual() {
  setActivePinia(createPinia())
  vi.mocked(api.getConfigSecrets).mockResolvedValue({
    success: true,
    data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
  })
  const wrapper = mount(ManualServerForm, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('ManualServerForm', () => {
  beforeEach(() => {
    vi.mocked(api.getConfigSecrets).mockReset()
    vi.mocked(api.getSecretRefs).mockReset()
    vi.mocked(api.setSecret).mockReset()
    vi.mocked(api.deleteSecret).mockReset()
    vi.mocked(api.callTool).mockReset()
    vi.mocked(api.getServers).mockReset()
  })

  // Review round 1: resolveSecretFields's returned writtenRefs were computed
  // but never passed to rollbackSecrets by this surface, so a secret write
  // that succeeded right before the add-server call itself failed (e.g. a
  // duplicate name) left an orphaned keyring entry behind.
  it('rolls back the just-written secret when the add-server call fails after a successful secret write', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '', reference: '' } })
    vi.mocked(api.deleteSecret).mockResolvedValue({ success: true, data: { message: '' } })
    vi.mocked(api.callTool).mockResolvedValue({ success: false, error: 'a server named "github" already exists' })

    const wrapper = await mountManual()

    await wrapper.find('[data-test="manual-name-input"]').setValue('github')
    await wrapper.find('[data-test="manual-type-stdio"]').setValue(true)
    await wrapper.find('[data-test="manual-command-input"]').setValue('uvx')
    await wrapper.find('[data-test="manual-env-add"]').trigger('click')
    await wrapper.find('[data-test="manual-env-name-0"]').setValue('GITHUB_TOKEN')
    await wrapper.find('[data-test="secret-toggle-mode-secret"]').trigger('click')
    await wrapper.find('[data-test="secret-toggle-value-input"]').setValue('sk-live-abc123')

    await wrapper.find('[data-test="manual-server-form"]').trigger('submit')
    await flushPromises()

    expect(api.setSecret).toHaveBeenCalledWith('github-env-github-token', 'sk-live-abc123')
    expect(wrapper.find('[data-test="manual-error"]').text()).toContain('already exists')
    expect(api.deleteSecret).toHaveBeenCalledWith('github-env-github-token')
  })

  // Migrated from the retired AddServerModal spec (Spec 109 T108a): the
  // protocol-conditional payload must not leak the other transport's fields.
  it('submits stdio fields only for a stdio server, and names the server it added', async () => {
    vi.mocked(api.callTool).mockResolvedValue({ success: true, data: {} })
    vi.mocked(api.getServers).mockResolvedValue({ success: true, data: { servers: [] } })
    const wrapper = await mountManual()

    await wrapper.find('[data-test="manual-name-input"]').setValue('fs-server')
    await wrapper.find('[data-test="manual-command-input"]').setValue('npx')
    await wrapper.find('[data-test="manual-args-input"]').setValue('-y  @scope/server /tmp')
    await wrapper.find('[data-test="manual-server-form"]').trigger('submit')
    await flushPromises()

    expect(api.callTool).toHaveBeenCalledWith('upstream_servers', {
      operation: 'add',
      name: 'fs-server',
      protocol: 'stdio',
      enabled: true,
      command: 'npx',
      args_json: JSON.stringify(['-y', '@scope/server', '/tmp']),
    })
    expect(wrapper.emitted('added')).toEqual([['fs-server']])
  })

  it('submits url only for an http server, and names the server it added', async () => {
    vi.mocked(api.callTool).mockResolvedValue({ success: true, data: {} })
    vi.mocked(api.getServers).mockResolvedValue({ success: true, data: { servers: [] } })
    const wrapper = await mountManual()

    await wrapper.find('[data-test="manual-name-input"]').setValue('remote')
    await wrapper.find('[data-test="manual-type-http"]').setValue(true)
    await wrapper.find('[data-test="manual-url-input"]').setValue('https://api.example.com/mcp')
    await wrapper.find('[data-test="manual-server-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.find('[data-test="manual-command-input"]').exists()).toBe(false)
    expect(api.callTool).toHaveBeenCalledWith('upstream_servers', {
      operation: 'add',
      name: 'remote',
      protocol: 'http',
      enabled: true,
      url: 'https://api.example.com/mcp',
    })
    expect(wrapper.emitted('added')).toEqual([['remote']])
  })
})
