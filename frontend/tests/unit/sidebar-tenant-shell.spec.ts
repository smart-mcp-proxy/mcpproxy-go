import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

const { logoutMock } = vi.hoisted(() => ({ logoutMock: vi.fn() }))

vi.mock('@/services/auth-api', () => ({
  authApi: {
    logout: logoutMock,
    getProvider: vi.fn(),
    getSessionStatus: vi.fn(),
    getMe: vi.fn(),
    getLoginUrl: vi.fn(),
  },
}))

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => false),
    getOnboardingState: vi.fn(async () => ({ success: true, data: null })),
    getGlobalTools: vi.fn(async () => ({ success: true, data: { stats: { total: 0 } } })),
    getConfigSecrets: vi.fn(async () => ({ success: true, data: { total_secrets: 0 } })),
  },
}))

import SidebarNav from '@/components/SidebarNav.vue'
import { useAuthStore } from '@/stores/auth'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/my/servers', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } },
    ],
  })
}

describe('SidebarNav settled tenant shell', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    logoutMock.mockReset().mockResolvedValue(undefined)
  })

  it('keeps tenant navigation and sign-out usable without loading core data', async () => {
    const router = makeRouter()
    await router.push('/')
    await router.isReady()

    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = {
      id: 'tenant', email: 'tenant@example.test', display_name: 'Tenant',
      role: 'user', provider: 'oidc', created_at: '', last_login_at: '',
    }

    const wrapper = mount(SidebarNav, { global: { plugins: [router] } })
    await flushPromises()

    await wrapper.get('a[href="/my/servers"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/my/servers')

    await wrapper.get('button[title="Sign out"]').trigger('click')
    await flushPromises()
    expect(logoutMock).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.path).toBe('/login')
  })
})
