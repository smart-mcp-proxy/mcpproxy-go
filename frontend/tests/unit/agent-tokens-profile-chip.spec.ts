import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AgentTokens from '@/views/AgentTokens.vue'
import api from '@/services/api'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_RO } from './fixtures/profiles108i'

// Spec 108 D39 (T149, demo finding #7): daisyUI's `badge-sm` has a fixed height,
// so a long profile title wrapped inside the Profile chip and the border cut
// through the second line (it read as strikethrough, i.e. "revoked"). The chip
// is now a single line with an ellipsis and the full title on hover.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    listAgentTokens: vi.fn(),
    getServers: vi.fn(),
    getProfiles: vi.fn(),
  },
}))

const stub = { template: '<div />' }
const future = '2030-01-01T00:00:00Z'
const LONG_TITLE = 'Work Read-only for very long client names 2026'
const LONG_PROFILE = { ...WORK_RO, name: 'work-readonly', title: LONG_TITLE }
const TOKENS = [
  { name: 'qa-ro', token_prefix: 'mcp_agt_aa', allowed_servers: ['*'], permissions: ['read'], expires_at: future, created_at: future, last_used_at: null, revoked: false, profile_pin: 'work-readonly', kind: 'agent', legacy_scope: false },
]

describe('Agent token Profile chip (Spec 108 T149)', () => {
  const original = (HTMLDialogElement.prototype as any).showModal

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(HTMLDialogElement.prototype as any).showModal = vi.fn()
    setAvailableFeatures(['scope_filters'])
    ;(api.listAgentTokens as any).mockResolvedValue({ success: true, data: { tokens: TOKENS } })
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [] } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [LONG_PROFILE] })
  })
  afterEach(() => {
    ;(HTMLDialogElement.prototype as any).showModal = original
  })

  it('is bounded, clips overflow, truncates the text and carries the full title', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/clients', name: 'clients', component: stub }, { path: '/profiles', name: 'profiles', component: stub }] })
    await router.push('/clients?tab=tokens')
    await router.isReady()
    const wrapper = mount(AgentTokens, { global: { plugins: [router] }, attachTo: document.body })
    await new Promise(resolve => setTimeout(resolve, 130))
    await flushPromises()

    const chip = wrapper.find('[data-test="token-profile-qa-ro"]')
    expect(chip.exists()).toBe(true)
    expect(chip.text()).toBe(LONG_TITLE)
    const cls = chip.classes().join(' ')
    expect(cls).toMatch(/max-w-\[/)
    expect(chip.classes()).toContain('overflow-hidden')
    expect(chip.classes()).toContain('min-w-0')
    expect(chip.attributes('title')).toBe(LONG_TITLE)
    const inner = chip.find('span.truncate')
    expect(inner.exists()).toBe(true)
    expect(inner.text()).toBe(LONG_TITLE)
    wrapper.unmount()
  })
})
