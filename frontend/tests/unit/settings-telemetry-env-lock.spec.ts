import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-044a (codex first-run user test F-03): with MCPPROXY_TELEMETRY=false
// (or DO_NOT_TRACK / CI) the core sends nothing, yet GET /api/v1/config serves
// the stored `telemetry.enabled: true`, so Settings rendered the toggle ON.
// The effective state on /api/v1/status now locks the toggle OFF with a reason.

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(),
  getStatus: vi.fn(),
}))

vi.mock('@/services/api', () => ({
  default: {
    getConfig: mocks.getConfig,
    getStatus: mocks.getStatus,
    validateConfig: vi.fn(),
    applyConfig: vi.fn(),
    patchConfig: vi.fn(),
  },
}))

import Settings from '@/views/Settings.vue'

const envLockLine = (
  JSON.parse(
    readFileSync(resolve(__dirname, '../../../internal/telemetry/testdata/effective_state_cases.json'), 'utf8')
  ) as { cases: { name: string; setting_lock: string | null }[] }
).cases.find((c) => c.name === 'env_mcpproxy_false_overrides_config_true')!.setting_lock

async function mountGeneral() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', name: 'settings', component: Settings, meta: { title: 'Settings' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div/>' } },
    ],
  })
  router.push('/settings?tab=general')
  await router.isReady()
  const wrapper = mount(Settings, { global: { plugins: [router], stubs: { VueMonacoEditor: true } } })
  await flushPromises()
  return wrapper
}

describe('Settings telemetry toggle follows the effective state', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.getConfig.mockReset()
    mocks.getStatus.mockReset()
    mocks.getConfig.mockResolvedValue({ success: true, data: { config: { telemetry: { enabled: true } } } })
  })

  it('env opt-out: toggle is disabled and unchecked, shows the reason, and is not dirty', async () => {
    mocks.getStatus.mockResolvedValue({
      success: true,
      data: { telemetry: { enabled: false, source: 'env', disabled_by: 'MCPPROXY_TELEMETRY=false' } },
    })
    const wrapper = await mountGeneral()

    const toggle = wrapper.find('[data-test="setting-toggle-telemetry.enabled"]')
    expect(toggle.exists()).toBe(true)
    expect((toggle.element as HTMLInputElement).disabled).toBe(true)
    expect((toggle.element as HTMLInputElement).checked).toBe(false)

    expect(wrapper.find('[data-test="setting-locked-telemetry.enabled"]').text()).toBe(envLockLine)

    expect(wrapper.find('[data-test="setting-row-telemetry.enabled"]').classes()).not.toContain('bg-warning/5')
    const apply = wrapper.find('[data-test="settings-apply-general"]')
    expect((apply.element as HTMLButtonElement).disabled).toBe(true)
    wrapper.unmount()
  })

  it('control: a config-level setting stays editable with no lock line', async () => {
    mocks.getStatus.mockResolvedValue({
      success: true,
      data: { telemetry: { enabled: true, source: 'config' } },
    })
    const wrapper = await mountGeneral()

    const toggle = wrapper.find('[data-test="setting-toggle-telemetry.enabled"]')
    expect((toggle.element as HTMLInputElement).disabled).toBe(false)
    expect((toggle.element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.find('[data-test="setting-locked-telemetry.enabled"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('control: an older core without status.telemetry leaves the toggle editable', async () => {
    mocks.getStatus.mockResolvedValue({ success: true, data: {} })
    const wrapper = await mountGeneral()
    const toggle = wrapper.find('[data-test="setting-toggle-telemetry.enabled"]')
    expect((toggle.element as HTMLInputElement).disabled).toBe(false)
    wrapper.unmount()
  })
})
