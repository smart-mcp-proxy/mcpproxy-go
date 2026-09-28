import { describe, it, expect, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ManualServerForm from '@/components/ManualServerForm.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    getConfigSecrets: vi.fn().mockResolvedValue({
      success: true,
      data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
    }),
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
    callTool: vi.fn().mockResolvedValue({ success: true, data: {} }),
  },
}))

describe('ManualServerForm post-add navigation', () => {
  it('can keep the onboarding wizard mounted after adding a server', async () => {
    setActivePinia(createPinia())
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div />' } }, { path: '/servers/:serverName', component: { template: '<div />' } }],
    })
    await router.push('/')
    await router.isReady()

    const wrapper = mount(ManualServerForm, {
      props: { navigateAfterAdd: false },
      global: { plugins: [router] },
    })
    await flushPromises()
    await wrapper.find('[data-test="manual-name-input"]').setValue('fs-server')
    await wrapper.find('[data-test="manual-command-input"]').setValue('npx')
    await wrapper.find('[data-test="manual-server-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.emitted('added')).toEqual([['fs-server']])
    expect(router.currentRoute.value.path).toBe('/')
    expect(api.callTool).toHaveBeenCalledWith('upstream_servers', expect.objectContaining({ name: 'fs-server' }))
    wrapper.unmount()
  })
})
