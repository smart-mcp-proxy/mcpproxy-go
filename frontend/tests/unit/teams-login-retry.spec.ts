import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { describe, it, expect, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  setAuthRequired: vi.fn(),
  recovered: false,
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    provider: { display_name: 'Example Corp' },
    get bootstrapError() { return mocks.recovered ? null : 'Session check failed' },
    isTeamsEdition: true,
    isAuthenticated: true,
    canShowShell: true,
    checkAuth: mocks.checkAuth,
    login: vi.fn(),
  }),
}))

vi.mock('@/stores/system', () => ({
  useSystemStore: () => ({ setAuthRequired: mocks.setAuthRequired }),
}))

describe('Teams login retry', () => {
  it('returns a recovered session to the real Home route', async () => {
    mocks.recovered = false
    mocks.checkAuth.mockImplementation(async () => { mocks.recovered = true })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'home', component: { template: '<div />' } },
        { path: '/login', name: 'login', component: { template: '<div />' } },
      ],
    })
    await router.push('/login')
    await router.isReady()

    const { default: Login } = await import('@/views/teams/Login.vue')
    const wrapper = mount(Login, { global: { plugins: [router] } })
    await wrapper.get('button.btn-ghost').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('home')
    expect(mocks.setAuthRequired).toHaveBeenCalledWith(false)
  })
})
