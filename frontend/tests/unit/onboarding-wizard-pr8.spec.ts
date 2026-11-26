import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import api from '@/services/api'

// Spec 134 PR 8 (FR-053): the verify step has distinct text for unknown, no
// handshake, handshake only and recorded success, never mentions a prompt that
// is not on screen; its tabs and the hints disclosure are keyboard-operable.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(), getOnboardingState: vi.fn(), getActivities: vi.fn(), getConfig: vi.fn(),
    getDockerStatus: vi.fn(), getCanonicalConfigPaths: vi.fn(), getStatus: vi.fn(),
  },
}))

function state(over: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      has_connected_client: true, has_configured_server: true, connected_client_count: 1,
      connected_client_ids: ['cursor'], configured_server_count: 1, state: { engaged: false },
      should_show_wizard: true, first_mcp_client_ever: true, mcp_clients_seen_ever: ['claude-code'],
      incomplete_tab_count: 0, has_usable_server: true, usable_servers: ['filesystem'], ...over,
    },
  }
}

async function open(onb: Record<string, unknown>, status: unknown) {
  ;(api.getOnboardingState as any).mockResolvedValue(state(onb))
  ;(api.getStatus as any).mockResolvedValue(status)
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div/>' } }] })
  router.push('/')
  await router.isReady()
  const wrapper = mount(OnboardingWizard, {
    props: { show: false },
    global: { plugins: [router], stubs: { RouterLink: { template: '<a><slot /></a>' } } },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  await wrapper.get('[data-test="tab-verify"]').trigger('click')
  await flushPromises()
  return wrapper
}

const ok = (routing_mode: string, flag?: boolean) => ({
  success: true,
  data: { routing_mode, ...(flag === undefined ? {} : { activation: { first_real_tool_call_ever: flag } }) },
})

describe('Wizard verify step copy (FR-053)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [] })
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
  })

  it('four states, four different texts', async () => {
    const texts = new Set<string>()
    const noHandshake = await open({ first_mcp_client_ever: false }, ok('retrieve_tools', false))
    texts.add(noHandshake.get('[data-test="panel-verify"]').text().split('Try one of')[0])
    const unknown = await open({}, ok('direct', false))
    const unknownRow = unknown.get('[data-test="verify-first-upstream-call"]')
    expect(unknownRow.attributes('data-state')).toBe('unknown')
    texts.add(unknownRow.text())
    const pending = await open({}, ok('retrieve_tools', false))
    const pendingRow = pending.get('[data-test="verify-first-upstream-call"]')
    expect(pendingRow.attributes('data-state')).toBe('pending')
    texts.add(pendingRow.text())
    const done = await open({}, ok('retrieve_tools', true))
    const doneRow = done.get('[data-test="verify-first-upstream-call"]')
    expect(doneRow.attributes('data-state')).toBe('satisfied')
    texts.add(doneRow.text())
    expect(texts.size).toBe(4)
  })

  it('the unknown text never claims a tool has or has not run', async () => {
    const w = await open({}, ok('code_execution', false))
    const t = w.get('[data-test="verify-first-upstream-call"]').text()
    expect(t).toMatch(/cannot|not tracked|not known|unknown/i)
    expect(t).not.toContain('No upstream tool call recorded')
  })

  it('does not point at a prompt that is not on screen', async () => {
    const withPrompts = await open({}, ok('retrieve_tools', false))
    expect(withPrompts.find('[data-test="verify-sample-prompts"]').exists()).toBe(true)
    expect(withPrompts.get('[data-test="verify-first-upstream-call"]').text()).toContain('prompt below')

    const without = await open({ has_usable_server: false, usable_servers: [] }, ok('retrieve_tools', false))
    expect(without.find('[data-test="verify-sample-prompts"]').exists()).toBe(false)
    expect(without.get('[data-test="verify-first-upstream-call"]').text()).not.toMatch(/prompt/i)
  })
})

describe('Wizard tabs and hints are keyboard-operable (FR-053)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [] })
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
  })

  it('wizard tabs are buttons with aria-selected', async () => {
    const w = await open({}, ok('retrieve_tools', true))
    for (const id of ['clients', 'servers', 'verify']) {
      const tab = w.get(`[data-test="tab-${id}"]`)
      expect(tab.element.tagName).toBe('BUTTON')
      expect(tab.attributes('role')).toBe('tab')
      expect(tab.attributes('aria-selected')).toBeDefined()
    }
    expect(w.get('[data-test="tab-verify"]').attributes('aria-selected')).toBe('true')
  })

  it('ArrowLeft / ArrowRight move between wizard tabs', async () => {
    const w = await open({}, ok('retrieve_tools', true))
    await w.get('[data-test="tab-verify"]').trigger('keydown', { key: 'ArrowLeft' })
    await flushPromises()
    expect(w.get('[data-test="tab-servers"]').attributes('aria-selected')).toBe('true')
    await w.get('[data-test="tab-servers"]').trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(w.get('[data-test="tab-verify"]').attributes('aria-selected')).toBe('true')
  })

  it('panels are tabpanels named by their tab', async () => {
    const w = await open({}, ok('retrieve_tools', true))
    const panel = w.get('[data-test="panel-verify"]')
    expect(panel.attributes('role')).toBe('tabpanel')
    expect(panel.attributes('aria-labelledby')).toBe(w.get('[data-test="tab-verify"]').attributes('id'))
  })

  it('arrow keys move from the focused tab, not the selected one', async () => {
    const w = await open({}, ok('retrieve_tools', true))
    // verify is selected; ArrowRight pressed on the Clients tab goes to Servers
    await w.get('[data-test="tab-clients"]').trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(w.get('[data-test="tab-servers"]').attributes('aria-selected')).toBe('true')
  })

  it('shows a checking state, not "not tracked", until the status call settles', async () => {
    const releases: Array<(v: unknown) => void> = []
    ;(api.getOnboardingState as any).mockResolvedValue(state({}))
    ;(api.getStatus as any).mockImplementation(() => new Promise((r) => { releases.push(r) }))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div/>' } }] })
    router.push('/')
    await router.isReady()
    const w = mount(OnboardingWizard, { props: { show: false }, global: { plugins: [router], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await w.setProps({ show: true })
    await flushPromises()
    await w.get('[data-test="tab-verify"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-test="verify-first-upstream-call"]').attributes('data-state')).toBe('checking')
    releases.forEach((r) => r(ok('retrieve_tools', false)))
    await flushPromises()
    expect(w.get('[data-test="verify-first-upstream-call"]').attributes('data-state')).toBe('pending')
  })

  it('the hints disclosure is a button with aria-expanded, in both states', async () => {
    const hints: any = [{ icon: 'lightbulb', title: 'Settings', sections: [{ text: 'x' }] }]
    const w = mount(CollapsibleHintsPanel, { props: { hints } })
    const btn = w.get('button[aria-expanded="false"]')
    await btn.trigger('click')
    const open2 = w.get('button[aria-expanded="true"]')
    expect(open2.element.tagName).toBe('BUTTON')
    await open2.trigger('click')
    expect(w.find('button[aria-expanded="false"]').exists()).toBe(true)
  })

  it('focus follows the hints toggle into its other state', async () => {
    const hints: any = [{ icon: 'lightbulb', title: 'Settings', sections: [{ text: 'x' }] }]
    const w = mount(CollapsibleHintsPanel, { props: { hints }, attachTo: document.body })
    await w.get('[data-test="hints-toggle"]').trigger('click')
    await flushPromises()
    expect(document.activeElement?.getAttribute('aria-expanded')).toBe('true')
    w.unmount()
  })
})
