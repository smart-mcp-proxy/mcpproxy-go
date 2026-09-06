import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// UX audit F08 — "Review asks for definitions that the Tools tab hides."
//
// A server quarantined at admission is `connected: false` and its tools are
// deliberately kept out of both the state snapshot and the search index
// (internal/runtime/tool_quarantine.go), so `GET /servers/{id}/tools` returns an
// empty list. The Tools tab rendered that as the generic "No tools available /
// Server must be connected to view tools." — the same words a broken server
// gets — while the Security tab was asking the operator to review those very
// definitions. "Withheld for review" and "absent" read identically.
//
// These tests pin the copy: withheld says withheld, the sentence never names a
// count (`quarantine.pending_count` counts tools awaiting review, not tools
// being withheld — quarantining an already-trusted server keeps its approval
// records, so pending_count understates the withheld set), a genuinely
// disconnected server keeps the old wording, and a connected server with no
// tools keeps its own.

type ServerOverrides = Record<string, unknown>

const state = {
  server: {} as ServerOverrides,
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [state.server] })),
      // Both tool sources come back empty — the exact F08 shape.
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getScanReport: vi.fn(() => ok({})),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
    },
  }
})

async function mountDetail(server: ServerOverrides) {
  state.server = server
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/probe')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'probe' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

const quarantined = (extra: ServerOverrides = {}): ServerOverrides => ({
  name: 'probe',
  protocol: 'http',
  enabled: true,
  connected: false,
  quarantined: true,
  tool_count: 0,
  health: {
    level: 'healthy',
    admin_state: 'quarantined',
    summary: 'Quarantined for review',
    action: 'approve',
  },
  ...extra,
})

describe('ServerDetail — Tools tab empty state on a quarantined server (F08)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  // Mount sanity: without this, every assertion below could pass or fail for
  // the wrong reason (a component that never rendered the Tools tab at all).
  it('actually renders the Tools tab empty state', async () => {
    const wrapper = await mountDetail(quarantined())
    expect(wrapper.find('[data-test="security-tab"]').exists()).toBe(true)
    // The Tools tab is the default tab and the tool list is empty, so the empty
    // state — whatever it says — is the branch under test.
    expect(wrapper.find('[data-test="server-tools-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="tool-quarantine-list"]').exists()).toBe(false)
  })

  it('says the tools are withheld for review, not that the server must be connected', async () => {
    const wrapper = await mountDetail(
      quarantined({ quarantine: { pending_count: 2, changed_count: 0, blocked_count: 0 } })
    )
    const empty = wrapper.find('[data-test="server-tools-empty"]')
    expect(empty.exists()).toBe(true)
    // The heading carries half the message and is asserted positively — without
    // this, reverting `toolsEmptyHeading` alone leaves the whole suite green.
    expect(empty.find('h3').text()).toBe('Tools withheld for review')
    const text = empty.text()
    expect(text).toContain('withheld')
    expect(text).not.toContain('Server must be connected to view tools.')
    expect(text).not.toContain('This server has no tools available.')
  })

  // `quarantine.pending_count` counts tools awaiting review, NOT tools being
  // withheld. Quarantining an already-trusted server keeps its approval records
  // (QuarantineServer purges only the index), so a server with 20 approved tools
  // and 2 pending ones reports pending_count 2 while all 22 are withheld. No
  // number in this sentence is a correct number, so there is no number in it.
  it('never names a count, even when the quarantine stats carry one', async () => {
    const wrapper = await mountDetail(
      quarantined({ quarantine: { pending_count: 2, changed_count: 1, blocked_count: 3 } })
    )
    const text = wrapper.find('[data-test="server-tools-empty"]').text()
    expect(text).toContain('withheld')
    expect(text).not.toMatch(/\d/)
  })

  it('says the same thing when the quarantine stats are absent', async () => {
    const wrapper = await mountDetail(quarantined())
    const empty = wrapper.find('[data-test="server-tools-empty"]')
    expect(empty.find('h3').text()).toBe('Tools withheld for review')
    const text = empty.text()
    expect(text).toContain("This server's tools are withheld")
    expect(text).not.toMatch(/\d/)
  })

  it('offers a way through to the Security tab where the findings live', async () => {
    const wrapper = await mountDetail(
      quarantined({ quarantine: { pending_count: 2, changed_count: 0, blocked_count: 0 } })
    )
    const cta = wrapper.find('[data-test="server-tools-empty-security"]')
    expect(cta.exists()).toBe(true)
    await cta.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="security-tab"]').classes()).toContain('tab-active')
  })

  // Integrated-review finding. The withheld copy told the user to "approve the
  // server to list them" — true for a quarantined server that connects fine,
  // and misleading for one that cannot start at all. A live probe on a
  // quarantined stdio server whose command does not exist returned
  // status="error", connected=false, last_error="Command ... not found on the
  // spawn PATH" — and the page still offered approval as the whole remedy,
  // while the fault alert and diagnostic panel are deliberately suppressed for
  // every quarantined server (issue #1076). Approving that server hands the
  // user a second failure. Say so instead.
  it('does not promise that approval alone will list tools when the server is not connected', async () => {
    const wrapper = await mountDetail(
      quarantined({ connected: false, status: 'error', last_error: 'failed to connect: command not found' })
    )
    const text = wrapper.find('[data-test="server-tools-empty"]').text()
    expect(text).toContain('withheld')
    expect(text).toMatch(/not connected|cannot connect|connection/i)
  })

  // The control: when the quarantined server IS connected, approval really is
  // the whole remedy and the copy must not acquire a connection caveat.
  it('keeps approval as the whole remedy when the quarantined server is connected', async () => {
    const wrapper = await mountDetail(quarantined({ connected: true }))
    const text = wrapper.find('[data-test="server-tools-empty"]').text()
    expect(text).toContain('approve the server')
    expect(text).not.toMatch(/not connected|cannot connect/i)
  })

  // Round-2 cross-model review. The caveat above was first keyed on
  // `!connected`, which is the DESIGNED state of a quarantined server, not a
  // fault: the supervisor disconnects any quarantined server without an active
  // inspection exemption and refuses to dial it
  // (internal/runtime/supervisor/supervisor.go). So the "check the connection
  // error" advice fired on EVERY quarantined server — including the default,
  // perfectly healthy quarantined-on-add case this whole screen exists to
  // serve — and sent the user hunting for an error that does not exist, while
  // approval genuinely was their entire remedy.
  //
  // This is the exact shape `quarantined()` builds: connected false, no
  // last_error. It is therefore also the shape of the two tests above it, which
  // is why nothing caught this.
  it('does not invent a connection error for a healthy quarantined server that is simply not dialled', async () => {
    const wrapper = await mountDetail(quarantined({ connected: false }))
    const text = wrapper.find('[data-test="server-tools-empty"]').text()
    expect(text).toContain('withheld')
    // Approval IS the whole remedy here.
    expect(text).toContain('approve the server')
    // And no fault may be asserted, because none was reported.
    expect(text).not.toMatch(/connection error|not connected|cannot connect/i)
  })

  // The other half of the same boundary: an observed fault is what earns the
  // caveat, and the caveat stays hedged ("may not be enough") because a stale
  // error from an earlier dial is not proof that approval will fail.
  it('names the fault only when the server actually reported one', async () => {
    const wrapper = await mountDetail(
      quarantined({ connected: false, last_error: 'failed to connect: command not found' })
    )
    const text = wrapper.find('[data-test="server-tools-empty"]').text()
    expect(text).toMatch(/connection error/i)
    expect(text).toMatch(/may not be enough/i)
    // Never the categorical claim it replaced.
    expect(text).not.toMatch(/will not list tools/i)
  })

  it('keeps the original wording for a disconnected server that is NOT quarantined', async () => {
    const wrapper = await mountDetail({
      name: 'probe',
      protocol: 'http',
      enabled: true,
      connected: false,
      quarantined: false,
      tool_count: 0,
    })
    const empty = wrapper.find('[data-test="server-tools-empty"]')
    expect(empty.find('h3').text()).toBe('No tools available')
    const text = empty.text()
    expect(text).toContain('Server must be connected to view tools.')
    expect(text).not.toContain('withheld')
  })

  it('keeps the original wording for a connected server with no tools', async () => {
    const wrapper = await mountDetail({
      name: 'probe',
      protocol: 'http',
      enabled: true,
      connected: true,
      quarantined: false,
      tool_count: 0,
    })
    const empty = wrapper.find('[data-test="server-tools-empty"]')
    expect(empty.find('h3').text()).toBe('No tools available')
    const text = empty.text()
    expect(text).toContain('This server has no tools available.')
    expect(text).not.toContain('withheld')
  })
})
