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
})
