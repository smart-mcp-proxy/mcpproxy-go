import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AccessExplainer from '@/components/AccessExplainer.vue'
import api from '@/services/api'
import { CONNECT_CLIENT_EVENT } from '@/navigation/navModel'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { apiError } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getGlobalTools: vi.fn(),
    explainAccess: vi.fn(),
    getProfiles: vi.fn(),
  },
}))

// The T003 contract fixture (F24 shape): the one source of truth the Go tests
// also decode.
const FIXTURE = JSON.parse(readFileSync(resolve(__dirname, '../../../internal/profile/testdata/contract/explain_blocked.json'), 'utf8'))

const stub = { template: '<div />' }

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/servers/:serverName', name: 'server-detail', component: stub },
      { path: '/review', name: 'review', component: stub },
      { path: '/settings', name: 'settings', component: stub },
    ],
  })
}

async function mountExplainer(props: Record<string, unknown> = {}) {
  const router = makeRouter()
  await router.push('/')
  await router.isReady()
  const wrapper = mount(AccessExplainer, {
    props: { open: false, subject: { kind: 'client', name: 'cursor' }, ...props },
    global: { plugins: [router] },
  })
  await wrapper.setProps({ open: true })
  await flushPromises()
  return { wrapper, router }
}

async function run(wrapper: ReturnType<typeof mount>, tool = 'github:create_issue') {
  await wrapper.get('[data-test="explain-tool-input"]').setValue(tool)
  await wrapper.get('[data-test="explain-run"]').trigger('submit')
  await flushPromises()
}

describe('AccessExplainer (Spec 108-i T094, FR-046)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['scope_filters'])
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [] })
    ;(api.getGlobalTools as any).mockResolvedValue({ success: true, data: { tools: [{ server_name: 'github', name: 'create_issue' }, { server_name: 'notion', name: 'update_page' }] } })
    ;(api.explainAccess as any).mockResolvedValue(FIXTURE)
  })

  it('lists server:tool from GET /tools in the picker', async () => {
    const { wrapper } = await mountExplainer()
    expect(wrapper.findAll('[data-test="explain-tool-options"] option').map(option => option.attributes('value'))).toEqual(['github:create_issue', 'notion:update_page'])
  })

  it('renders the steps in order with an icon AND text, plus the verdict banner', async () => {
    const { wrapper } = await mountExplainer()
    await run(wrapper)
    expect(api.explainAccess).toHaveBeenCalledWith({ tool: 'github:create_issue', client: 'cursor' })
    const steps = wrapper.findAll('[data-test="explain-steps"] li')
    expect(steps.map(step => step.attributes('data-test'))).toEqual(FIXTURE.steps.map((step: { step: string }) => `explain-step-${step.step}`))
    const tier = wrapper.get('[data-test="explain-step-tier_cap"]')
    expect(tier.text()).toContain('✗')
    expect(tier.text()).toContain('Fail')
    expect(tier.text()).toContain('Above tier cap')
    expect(wrapper.get('[data-test="explain-step-credential"]').text()).toContain('✓')
    expect(wrapper.get('[data-test="explain-step-credential"]').text()).toContain('Pass')
    expect(wrapper.get('[data-test="explain-step-token_permission"]').text()).toContain('Skipped')
    const verdict = wrapper.get('[data-test="explain-verdict"]')
    expect(verdict.text()).toContain('Hidden')
    expect(verdict.text()).toContain('github:create_issue')
    expect(verdict.text()).toContain('work-readonly')
    expect(wrapper.get('[data-test="explain-result"]').attributes('aria-live')).toBe('polite')
  })

  it('renders one button per top-level fix, in the server order', async () => {
    const { wrapper } = await mountExplainer()
    await run(wrapper)
    const buttons = wrapper.findAll('[data-test="explain-fixes"] button')
    expect(buttons.map(button => button.text())).toEqual(FIXTURE.fixes.map((fix: { label: string }) => fix.label))
    expect(buttons.map(button => button.attributes('data-test'))).toEqual(['explain-fix-allow_in_profile', 'explain-fix-move_client'])
  })

  const cases: Array<[string, string, (route: any) => void]> = [
    ['allow_in_profile', 'work-readonly', route => { expect(route.path).toBe('/profiles/work-readonly'); expect(route.query.focus).toBe('github:create_issue') }],
    ['classify_in_profile', 'work-readonly', route => { expect(route.path).toBe('/profiles/work-readonly'); expect(route.query.focus).toBe('github:create_issue') }],
    ['add_server_to_profile', 'work-readonly', route => { expect(route.path).toBe('/profiles/work-readonly'); expect(route.query.focus).toBe('github:create_issue') }],
    ['move_client', 'cursor', route => { expect(route.path).toBe('/clients'); expect(route.query).toEqual({ focus: 'cursor', move: '1' }) }],
    ['edit_token', 'ci', route => { expect(route.path).toBe('/tokens'); expect(route.query.token).toBe('ci') }],
    ['enable_server', 'github', route => { expect(route.path).toBe('/servers/github') }],
    ['approve_tool', 'github', route => { expect(route.path).toBe('/review'); expect(route.query.server).toBe('github') }],
    ['change_setting', 'require_mcp_auth', route => { expect(route.path).toBe('/settings'); expect(route.query).toEqual({ tab: 'security', focus: 'require_mcp_auth' }) }],
  ]
  it.each(cases)('the %s fix routes correctly', async (action, target, check) => {
    ;(api.explainAccess as any).mockResolvedValue({ ...FIXTURE, fixes: [{ step: 'tier_cap', action, target, label: `Fix ${action}` }] })
    const { wrapper, router } = await mountExplainer()
    await run(wrapper)
    await wrapper.get(`[data-test="explain-fix-${action}"]`).trigger('click')
    await flushPromises()
    check(router.currentRoute.value)
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('fix routes go through the scope link map; only move_client stays unscoped (#1446-10)', async () => {
    setAvailableFeatures(['scope_filters'])
    ;(api.explainAccess as any).mockResolvedValue({ ...FIXTURE, fixes: [
      { step: 'tier_cap', action: 'edit_token', target: 'ci', label: 'Fix edit_token' },
      { step: 'tier_cap', action: 'move_client', target: 'cursor', label: 'Fix move_client' },
    ] })
    const router = makeRouter()
    await router.push('/clients?profile=work-ro&client=cursor')
    await router.isReady()
    const wrapper = mount(AccessExplainer, { props: { open: false, subject: { kind: 'client', name: 'cursor' } }, global: { plugins: [router] } })
    await wrapper.setProps({ open: true })
    await flushPromises()
    await run(wrapper)
    await wrapper.get('[data-test="explain-fix-edit_token"]').trigger('click')
    await flushPromises()
    // The tokens page registers profile: the sticky filter carries, client does not.
    expect(router.currentRoute.value.path).toBe('/tokens')
    expect(router.currentRoute.value.query).toEqual({ profile: 'work-ro', token: 'ci' })
    await router.push('/clients?profile=work-ro&client=cursor')
    await wrapper.get('[data-test="explain-fix-move_client"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/clients')
    expect(router.currentRoute.value.query).toEqual({ focus: 'cursor', move: '1' })
  })

  it('reconnect_client dispatches the connect event for the client', async () => {
    ;(api.explainAccess as any).mockResolvedValue({ ...FIXTURE, fixes: [{ step: 'credential', action: 'reconnect_client', target: 'cursor', label: 'Reconnect Cursor' }] })
    const { wrapper } = await mountExplainer()
    await run(wrapper)
    const events: CustomEvent[] = []
    const listener = (event: Event) => events.push(event as CustomEvent)
    window.addEventListener(CONNECT_CLIENT_EVENT, listener)
    await wrapper.get('[data-test="explain-fix-reconnect_client"]').trigger('click')
    window.removeEventListener(CONNECT_CLIENT_EVENT, listener)
    expect(events[0].detail).toEqual({ client: 'cursor' })
  })

  it('shows a 400 (built-in tool) and a 404 inline', async () => {
    ;(api.explainAccess as any).mockRejectedValueOnce(apiError('access/explain covers upstream tools (server:tool) only', { status: 400 }))
    const { wrapper } = await mountExplainer()
    await run(wrapper, 'retrieve_tools')
    expect(wrapper.get('[data-test="explain-error"]').text()).toBe('access/explain covers upstream tools (server:tool) only')
    expect(wrapper.find('[data-test="explain-result"]').exists()).toBe(false)
    ;(api.explainAccess as any).mockRejectedValueOnce(apiError('client not found', { status: 404 }))
    await run(wrapper, 'github:create_issue')
    expect(wrapper.get('[data-test="explain-error"]').text()).toBe('client not found')
  })

  it('reads a 403 as "Requires an administrator", not a lost API key', async () => {
    ;(api.explainAccess as any).mockRejectedValueOnce(apiError('Admin credentials required', { status: 403 }))
    const { wrapper } = await mountExplainer()
    await run(wrapper)
    expect(wrapper.get('[data-test="explain-error"]').text()).toBe('Requires an administrator')
  })

  it('runs at once when a tool is preset, for a token or the anonymous subject', async () => {
    const { wrapper } = await mountExplainer({ subject: { kind: 'token', name: 'ci' }, tool: 'notion:update_page' })
    expect(api.explainAccess).toHaveBeenCalledWith({ tool: 'notion:update_page', token: 'ci' })
    expect(wrapper.find('[data-test="explain-result"]').exists()).toBe(true)
    ;(api.explainAccess as any).mockClear()
    await mountExplainer({ subject: { kind: 'anonymous' }, tool: 'github:create_issue' })
    expect(api.explainAccess).toHaveBeenCalledWith({ tool: 'github:create_issue', anonymous: true })
  })
})
