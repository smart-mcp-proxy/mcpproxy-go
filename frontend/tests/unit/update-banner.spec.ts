import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// Spec 079 US1 (FR-005): dismissible, non-modal Web UI update banner with
// per-version dismissal — dismissing vX persists (localStorage) and keeps vX
// suppressed across reloads, while a newer vY makes the banner reappear.

import UpdateBanner from '@/components/UpdateBanner.vue'
import { useSystemStore } from '@/stores/system'

const STORAGE_KEY = 'update-banner-dismissed-version'

function mountBanner(update?: {
  available: boolean
  latest_version?: string
  release_url?: string
}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useSystemStore()
  store.info = {
    version: 'v1.2.0',
    web_ui_url: '',
    listen_addr: '127.0.0.1:8080',
    endpoints: { http: '127.0.0.1:8080', socket: '' },
    ...(update ? { update } : {}),
  } as never
  return { wrapper: mount(UpdateBanner, { global: { plugins: [pinia] } }), store }
}

describe('UpdateBanner (Spec 079 FR-005)', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('renders latest + current version and the release-notes link when an update is available', () => {
    const { wrapper } = mountBanner({
      available: true,
      latest_version: 'v1.3.0',
      release_url: 'https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0',
    })
    const banner = wrapper.find('[data-test="update-banner"]')
    expect(banner.exists()).toBe(true)
    expect(banner.text()).toContain('v1.3.0')
    expect(banner.text()).toContain('v1.2.0')
    const link = wrapper.find('[data-test="update-banner-release-link"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe(
      'https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v1.3.0'
    )
  })

  it('does not render when no update is available', () => {
    const { wrapper } = mountBanner({ available: false })
    expect(wrapper.find('[data-test="update-banner"]').exists()).toBe(false)
  })

  it('does not render when the update object is absent (update_check disabled)', () => {
    const { wrapper } = mountBanner(undefined)
    expect(wrapper.find('[data-test="update-banner"]').exists()).toBe(false)
  })

  it('dismiss hides the banner and persists the dismissed version', async () => {
    const { wrapper } = mountBanner({ available: true, latest_version: 'v1.3.0' })
    await wrapper.find('[data-test="update-banner-dismiss"]').trigger('click')
    expect(wrapper.find('[data-test="update-banner"]').exists()).toBe(false)
    expect(localStorage.getItem(STORAGE_KEY)).toBe('v1.3.0')
  })

  it('stays dismissed for the same version across remounts', () => {
    localStorage.setItem(STORAGE_KEY, 'v1.3.0')
    const { wrapper } = mountBanner({ available: true, latest_version: 'v1.3.0' })
    expect(wrapper.find('[data-test="update-banner"]').exists()).toBe(false)
  })

  it('reappears when a newer version than the dismissed one becomes latest', () => {
    localStorage.setItem(STORAGE_KEY, 'v1.3.0')
    const { wrapper } = mountBanner({ available: true, latest_version: 'v1.4.0' })
    const banner = wrapper.find('[data-test="update-banner"]')
    expect(banner.exists()).toBe(true)
    expect(banner.text()).toContain('v1.4.0')
  })

  it('still renders and dismisses (session-only) when localStorage is blocked', async () => {
    // Blocked storage (embedded/private contexts) throws on access; the
    // banner must degrade to session-only dismissal, not break setup.
    // Scoped to the banner's key so unrelated store setup (theme, api key)
    // keeps working in this test.
    const realGet = Storage.prototype.getItem
    const realSet = Storage.prototype.setItem
    const getSpy = vi
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(function (this: Storage, key: string) {
        if (key === STORAGE_KEY) throw new Error('storage blocked')
        return realGet.call(this, key)
      })
    const setSpy = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(function (this: Storage, key: string, value: string) {
        if (key === STORAGE_KEY) throw new Error('storage blocked')
        realSet.call(this, key, value)
      })
    try {
      const { wrapper } = mountBanner({ available: true, latest_version: 'v1.3.0' })
      const banner = wrapper.find('[data-test="update-banner"]')
      expect(banner.exists()).toBe(true)
      expect(banner.text()).toContain('v1.3.0')

      await wrapper.find('[data-test="update-banner-dismiss"]').trigger('click')
      expect(wrapper.find('[data-test="update-banner"]').exists()).toBe(false)
    } finally {
      getSpy.mockRestore()
      setSpy.mockRestore()
    }
  })
})
