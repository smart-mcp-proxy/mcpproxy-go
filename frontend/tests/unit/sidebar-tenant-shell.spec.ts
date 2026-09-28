import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

const { logoutMock, getOnboardingStateMock, getGlobalToolsMock, getConfigSecretsMock } = vi.hoisted(() => ({
  logoutMock: vi.fn(),
  getOnboardingStateMock: vi.fn(),
  getGlobalToolsMock: vi.fn(),
  getConfigSecretsMock: vi.fn(),
}))

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
    getOnboardingState: getOnboardingStateMock,
    getGlobalTools: getGlobalToolsMock,
    getConfigSecrets: getConfigSecretsMock,
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
    getOnboardingStateMock.mockReset().mockResolvedValue({ success: true, data: null })
    getGlobalToolsMock.mockReset().mockResolvedValue({ success: true, data: { stats: { total: 0 } } })
    getConfigSecretsMock.mockReset().mockResolvedValue({ success: true, data: { total_secrets: 0 } })
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

    expect(getOnboardingStateMock).not.toHaveBeenCalled()
    expect(getGlobalToolsMock).not.toHaveBeenCalled()
    expect(getConfigSecretsMock).not.toHaveBeenCalled()

    await wrapper.get('a[href="/my/servers"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/my/servers')

    await wrapper.get('button[title="Sign out"]').trigger('click')
    await flushPromises()
    expect(logoutMock).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.path).toBe('/login')
  })
})
