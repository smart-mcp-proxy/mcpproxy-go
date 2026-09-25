import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Spec 109 FR-056 (T008/T020): the settings page had accumulated three names
// (audit W5) — this pins it down to "Settings" everywhere (sidebar label,
// route title/document.title, H1), drops the emoji tab icons for the app's
// line-icon set, and gates the "Server Edition" tab on the resolved runtime
// edition (`systemStore.status.edition`) rather than on config content.

const emptyConfig = vi.hoisted(() => ({ server_edition: { enabled: false } }))

vi.mock('@/services/api', () => ({
  default: {
    getConfig: vi.fn().mockResolvedValue({ success: true, data: { config: emptyConfig } }),
    getStatus: vi.fn().mockResolvedValue({ success: true, data: {} }),
    validateConfig: vi.fn(),
    applyConfig: vi.fn(),
  },
}))

import Settings from '@/views/Settings.vue'
import { useSystemStore } from '@/stores/system'
import appRouter from '@/router'

function makeRouter(path = '/settings') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', name: 'settings', component: Settings, meta: { title: 'Settings' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div/>' } },
    ],
  })
  router.push(path)
  return router
}

async function mountSettings() {
  const router = makeRouter()
  await router.isReady()
  const wrapper = mount(Settings, {
    global: {
      plugins: [router],
      stubs: { VueMonacoEditor: true, ConnectModal: true },
    },
  })
  await flushPromises()
  return wrapper
}

describe('Settings naming (Spec 109 FR-056)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('names the /settings route "Settings", not "Configuration"', () => {
    const resolved = appRouter.resolve('/settings')
    expect(resolved.meta.title).toBe('Settings')
  })

  it('names the sidebar entry "Settings"', () => {
    const src = readFileSync(resolve(__dirname, '../../src/components/SidebarNav.vue'), 'utf-8')
    expect(src).toMatch(/>Settings<\/span>/)
    expect(src).not.toMatch(/>Configuration<\/span>/)
  })

  it('renders "Settings" as the H1', async () => {
    const wrapper = await mountSettings()
    expect(wrapper.find('h1').text()).toBe('Settings')
    wrapper.unmount()
  })

  it('has no emoji in the tab labels', async () => {
    const wrapper = await mountSettings()
    const tabs = wrapper.find('[data-test="settings-tabs"]')
    // A crude but effective emoji sweep: astral-plane pictographs (U+1F000+,
    // covers 🔒🧰👥 etc.), the Misc Symbols / Dingbats blocks (covers ⚙),
    // and the variation-selector-16 that turns ⚙ into ⚙️.
    expect(tabs.text()).not.toMatch(/[\u{1F000}-\u{1FFFF}☀-➿️]/u)
  })

  it('hides the Server Edition tab when the config has a server_edition key but the runtime edition is personal', async () => {
    const wrapper = await mountSettings()
    expect(wrapper.find('[data-test="settings-tab-teams"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows the Server Edition tab once the runtime edition is server, regardless of config content', async () => {
    const systemStore = useSystemStore()
    // @ts-expect-error partial StatusUpdate is enough for this computed
    systemStore.status = { edition: 'server' }
    const wrapper = await mountSettings()
    expect(wrapper.find('[data-test="settings-tab-teams"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
