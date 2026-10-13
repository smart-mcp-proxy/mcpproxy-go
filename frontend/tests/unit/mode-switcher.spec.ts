import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// The header's `Mode:` badge was read-only: a `cursor-help` pointer, a native
// title tooltip most users never waited out, and no way to act on it. It is now
// a switcher over THREE settings on TWO axes — routing_mode (restart-bound) and
// the two serialization axes (hot-reloadable) — and the whole point of the
// panel is that an operator can tell those apart before clicking.

const getRouting = vi.hoisted(() => vi.fn())
const patchConfig = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: { getRouting, patchConfig },
}))

import ModeSwitcher from '@/components/ModeSwitcher.vue'
import { useSystemStore } from '@/stores/system'

type RoutingOverrides = Partial<{
  routing_mode: string
  tool_response_mode: string
  direct_tool_response_mode: string
  pending_routing_mode: string
  restart_required: boolean
  code_execution_enabled: boolean
}>

// Mirrors the /api/v1/routing payload, including the resolution the handler
// does: the serialization axes are never empty on the wire.
const backend = vi.hoisted(() => ({ state: {} as Record<string, unknown> }))

function routingPayload(o: RoutingOverrides = {}) {
  return {
    routing_mode: 'retrieve_tools',
    description: 'BM25 search',
    endpoints: {
      default: '/mcp',
      direct: '/mcp/all',
      code_execution: '/mcp/code',
      retrieve_tools: '/mcp/call',
    },
    available_modes: ['retrieve_tools', 'direct', 'code_execution'],
    tool_response_mode: 'full',
    direct_tool_response_mode: 'full',
    pending_routing_mode: '',
    restart_required: false,
    code_execution_enabled: true,
    ...o,
  }
}

async function mountOpen(o: RoutingOverrides = {}) {
  backend.state = routingPayload(o)
  const wrapper = mount(ModeSwitcher, {
    global: { plugins: [createPinia()], stubs: { RouterLink: true } },
  })
  const store = useSystemStore()
  await store.fetchRouting()
  await flushPromises()
  await wrapper.find('[data-test="mode-switcher-button"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('ModeSwitcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    backend.state = routingPayload()
    getRouting.mockReset().mockImplementation(() =>
      Promise.resolve({ success: true, data: backend.state })
    )
    patchConfig.mockReset().mockResolvedValue({
      success: true,
      data: { success: true, applied_immediately: true, requires_restart: false, changed_fields: [] },
    })
  })

  it('names the mode /mcp is actually serving and opens on click', async () => {
    const wrapper = await mountOpen()
    expect(wrapper.find('[data-test="mode-switcher-active"]').text()).toBe('Retrieve')
    expect(wrapper.find('[data-test="mode-switcher-menu"]').exists()).toBe(true)
    // Opening refetches: Settings, the tray or the config file may have moved
    // any of the three values since the last poll.
    expect(getRouting.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('labels each routing mode with its trade-off, its endpoint and its restart cost', async () => {
    const wrapper = await mountOpen()

    expect(wrapper.find('[data-test="mode-active-badge-retrieve_tools"]').text()).toBe('serving now')
    expect(wrapper.find('[data-test="mode-restart-badge-direct"]').text()).toBe('needs restart')
    expect(wrapper.find('[data-test="mode-restart-badge-code_execution"]').exists()).toBe(true)

    // Visible: one short line plus the endpoint chip. The full explanation and
    // its trade-off are the hover hint — the panel must not scroll, so the long
    // form cannot be inline. This asserts it is still REACHABLE, not dropped.
    const direct = wrapper.find('[data-test="mode-option-direct"]')
    expect(direct.text()).toContain('/mcp/all')
    expect(direct.text().length).toBeLessThan(110)
    expect(direct.attributes('title')).toContain('190 tokens per tool')

    // The way to get Direct WITHOUT a restart has to be on screen, or the only
    // visible path to it is a restart the operator may not want.
    expect(wrapper.find('[data-test="mode-switcher-restart-note"]').text()).toContain('/mcp')
  })

  it('writes only the field it changed, via PATCH', async () => {
    const wrapper = await mountOpen()
    await wrapper.find('[data-test="mode-option-direct"]').trigger('click')
    await flushPromises()

    expect(patchConfig).toHaveBeenCalledTimes(1)
    expect(patchConfig).toHaveBeenCalledWith({ routing_mode: 'direct' })
  })

  it('keeps naming the served mode after a restart-required switch, and says what is pending', async () => {
    const wrapper = await mountOpen()
    patchConfig.mockImplementation((partial: Record<string, string>) => {
      // ApplyConfig's restart contract: disk moves, memory does not.
      backend.state = routingPayload({
        pending_routing_mode: partial.routing_mode,
        restart_required: true,
      })
      return Promise.resolve({
        success: true,
        data: {
          success: true,
          applied_immediately: false,
          requires_restart: true,
          restart_reason: 'Routing mode changed',
          changed_fields: ['routing_mode'],
        },
      })
    })

    await wrapper.find('[data-test="mode-option-direct"]').trigger('click')
    await flushPromises()

    // The badge must not claim Direct while /mcp is still serving Retrieve.
    expect(wrapper.find('[data-test="mode-switcher-active"]').text()).toBe('Retrieve')
    expect(wrapper.find('[data-test="mode-switcher-pending-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="mode-pending-badge-direct"]').text()).toBe('after restart')

    const notice = wrapper.find('[data-test="mode-switcher-pending-notice"]')
    expect(notice.exists()).toBe(true)
    expect(notice.text()).toContain('/mcp/all') // works right now, no restart

    // And a warning toast, not a green "applied".
    const store = useSystemStore()
    expect(store.toasts.at(-1)?.type).toBe('warning')
  })

  it('lets the operator cancel a pending switch', async () => {
    const wrapper = await mountOpen({
      pending_routing_mode: 'direct',
      restart_required: true,
    })
    await wrapper.find('[data-test="mode-switcher-cancel-pending"]').trigger('click')
    await flushPromises()

    expect(patchConfig).toHaveBeenCalledWith({ routing_mode: 'retrieve_tools' })
  })

  it('switches each serialization axis on its own field, applied immediately', async () => {
    const wrapper = await mountOpen()

    await wrapper.find('[data-test="serialization-option-tool-response-compact"]').trigger('click')
    await flushPromises()
    expect(patchConfig).toHaveBeenLastCalledWith({ tool_response_mode: 'compact' })

    await wrapper
      .find('[data-test="serialization-option-direct-tool-response-deferred"]')
      .trigger('click')
    await flushPromises()
    expect(patchConfig).toHaveBeenLastCalledWith({ direct_tool_response_mode: 'deferred' })

    // Hot-reloadable: no restart wording for these two.
    const store = useSystemStore()
    expect(store.toasts.at(-1)?.type).toBe('success')
  })

  it('quantifies the deferred trade-off instead of naming it', async () => {
    const wrapper = await mountOpen()
    const deferred = wrapper.find(
      '[data-test="serialization-option-direct-tool-response-deferred"]'
    )
    // The visible line quantifies it roughly; the hover hint carries the
    // measured numbers from Spec 102 (SC-001 as restated) and the cost.
    expect(deferred.text()).toContain('%')
    expect(deferred.attributes('title')).toMatch(/29\.7%|34\.8%/)
    expect(deferred.attributes('title')).toContain('describe_tool')
  })

  it('names the endpoints each axis governs, which depends on the routing mode', async () => {
    const retrieve = await mountOpen()
    expect(retrieve.find('[data-test="serialization-surface-tool-response"]').text()).toContain(
      '/mcp · /mcp/call'
    )
    // Direct listings still govern /mcp/all while /mcp serves Retrieve — the
    // axis is never inert, so it is never greyed out.
    expect(
      retrieve.find('[data-test="serialization-surface-direct-tool-response"]').text()
    ).toBe('/mcp/all')

    const direct = await mountOpen({ routing_mode: 'direct' })
    expect(
      direct.find('[data-test="serialization-surface-direct-tool-response"]').text()
    ).toContain('/mcp · /mcp/all')
  })

  it('surfaces a non-default serialization on the collapsed badge', async () => {
    // The panel's word for it, not the raw config value: an operator who opens
    // the panel after reading "compact" would find no such word in it.
    const compact = await mountOpen({ tool_response_mode: 'compact' })
    expect(compact.find('[data-test="mode-switcher-serialization-badge"]').text()).toBe('Signatures')

    const deferred = await mountOpen({
      routing_mode: 'direct',
      direct_tool_response_mode: 'deferred',
    })
    expect(deferred.find('[data-test="mode-switcher-serialization-badge"]').text()).toBe(
      'Signatures'
    )

    // Code execution PINS /mcp to full schemas, so a badge there would advertise
    // a serialization that surface does not use.
    const codeExec = await mountOpen({
      routing_mode: 'code_execution',
      tool_response_mode: 'compact',
    })
    expect(codeExec.find('[data-test="mode-switcher-serialization-badge"]').exists()).toBe(false)

    // …and stays quiet when both axes are at their default.
    const plain = await mountOpen()
    expect(plain.find('[data-test="mode-switcher-serialization-badge"]').exists()).toBe(false)
  })

  it('names both settings on its tabs, so neither hides below the fold', async () => {
    const wrapper = await mountOpen({ tool_response_mode: 'compact' })
    // Retrieve is serving, so the Schema-detail tab names the retrieve axis.
    expect(wrapper.find('[data-test="mode-tab-surface"]').text()).toContain('Retrieve')
    expect(wrapper.find('[data-test="mode-tab-detail"]').text()).toContain('Signatures')

    const direct = await mountOpen({ routing_mode: 'direct', direct_tool_response_mode: 'full' })
    expect(direct.find('[data-test="mode-tab-detail"]').text()).toContain('Full schemas')
  })

  it('never lets two chips disagree: both tabs report what is SERVING', async () => {
    const wrapper = await mountOpen({ pending_routing_mode: 'direct', restart_required: true })
    expect(wrapper.find('[data-test="mode-switcher-active"]').text()).toBe('Retrieve')
    // Not "Direct": the pending choice is spelled out in the notice and on the
    // option's "after restart" badge, never implied by a chip.
    expect(wrapper.find('[data-test="mode-tab-surface"]').text()).toContain('Retrieve')
    expect(wrapper.find('[data-test="mode-tab-surface"]').text()).not.toContain('Direct')
    expect(wrapper.find('[data-test="mode-switcher-pending-notice"]').exists()).toBe(true)
  })

  // Code execution pins the retrieve surface to full schemas because
  // describe_tool is not exposed there (Spec 085 FR-011). Claiming the setting
  // governs /mcp in that mode is a lie the operator would act on.
  it('does not claim the retrieve axis governs /mcp under code execution', async () => {
    const codeExec = await mountOpen({ routing_mode: 'code_execution' })
    expect(codeExec.find('[data-test="serialization-surface-tool-response"]').text()).toBe(
      '/mcp/call'
    )
    const note = codeExec.find('[data-test="serialization-note-tool-response"]')
    expect(note.text()).toContain('full schemas')
    expect(note.attributes('title')).toContain('describe_tool')

    // …and says nothing of the sort in the mode where it does govern /mcp.
    const retrieve = await mountOpen()
    expect(retrieve.find('[data-test="serialization-note-tool-response"]').exists()).toBe(false)
  })

  // An unrecognized pending value resolves to Retrieve like any unknown mode,
  // which would render "still serving Retrieve, saved on disk: Retrieve" over a
  // Cancel button that cancels nothing.
  it('says nothing rather than contradicting itself on an unrecognized pending mode', async () => {
    const wrapper = await mountOpen({
      pending_routing_mode: 'something_new',
      restart_required: true,
    })
    expect(wrapper.find('[data-test="mode-switcher-pending-notice"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="mode-switcher-pending-badge"]').exists()).toBe(false)
  })

  // The panel must fit without scrolling: a dropdown with an inner scrollbar
  // hides the half of the decision that is below the fold, which is exactly
  // what the two tabs exist to prevent. Height is measured in the browser
  // sweep; here we pin the two things that make it grow — an overflow class on
  // the container, and long inline copy where a hover hint belongs.
  // The panel must FIT without scrolling at a normal viewport — the two tabs
  // exist so neither half of the decision hides below a fold. It keeps a
  // viewport-relative cap and overflow anyway: dropping them entirely made the
  // bottom of the panel permanently unreachable at 200% zoom or on a short
  // window, because the panel is absolutely positioned inside a sticky header
  // that the page cannot scroll. The cap is sized so it never engages at normal
  // heights; what keeps it from engaging is the copy budget asserted below.
  it('fits without scrolling, and keeps its long copy in hover hints', async () => {
    const wrapper = await mountOpen()
    const menu = wrapper.find('[data-test="mode-switcher-menu"]')
    const classes = menu.classes().join(' ')
    expect(classes).toMatch(/overflow-y-auto/)
    expect(classes).toMatch(/max-h-\[calc\(100vh/)

    for (const mode of ['retrieve_tools', 'direct', 'code_execution']) {
      const row = wrapper.find(`[data-test="mode-option-${mode}"]`)
      expect(row.text().length).toBeLessThan(110)
      // …and the long form is still one hover away.
      expect((row.attributes('title') || '').length).toBeGreaterThan(60)
    }
    for (const id of ['tool-response-compact', 'direct-tool-response-deferred']) {
      const row = wrapper.find(`[data-test="serialization-option-${id}"]`)
      expect(row.text().length).toBeLessThan(80)
      expect((row.attributes('title') || '').length).toBeGreaterThan(60)
    }
  })

  it('links out to the docs from both tabs', async () => {
    const wrapper = await mountOpen()
    expect(wrapper.find('[data-test="mode-switcher-surface-doc"]').attributes('href')).toContain(
      'docs.mcpproxy.app'
    )
    expect(wrapper.find('[data-test="mode-switcher-detail-doc"]').attributes('href')).toContain(
      'docs.mcpproxy.app'
    )
  })

  // The code-execution surface has NO tool-calling path but the code_execution
  // tool, which refuses while the feature is off. A prerequisite that only
  // appears on hover is one the operator meets after restarting into a surface
  // that finds tools and can call none of them.
  it('warns inline when Code Exec cannot work yet', async () => {
    const off = await mountOpen({ code_execution_enabled: false })
    const prereq = off.find('[data-test="mode-option-code_execution-prereq"]')
    expect(prereq.exists()).toBe(true)
    expect(prereq.text()).toContain('Settings')
    // …and only for the mode it gates.
    expect(off.find('[data-test="mode-option-direct-prereq"]').exists()).toBe(false)

    const on = await mountOpen({ code_execution_enabled: true })
    expect(on.find('[data-test="mode-option-code_execution-prereq"]').exists()).toBe(false)
  })

  it('does not promise a pending surface that cannot serve yet', async () => {
    const wrapper = await mountOpen({
      pending_routing_mode: 'code_execution',
      restart_required: true,
      code_execution_enabled: false,
    })
    const notice = wrapper.find('[data-test="mode-switcher-pending-notice"]').text()
    expect(notice).toContain('Settings')
    expect(notice).not.toContain('serves it now')
  })

  // Selection was conveyed by a background tint and an unlabelled tick — neither
  // of which reaches assistive tech.
  it('exposes its selection to assistive tech, on both axes', async () => {
    const wrapper = await mountOpen({ tool_response_mode: 'compact' })

    const active = wrapper.find('[data-test="mode-option-retrieve_tools"]')
    expect(active.attributes('role')).toBe('radio')
    expect(active.attributes('aria-checked')).toBe('true')
    expect(wrapper.find('[data-test="mode-option-direct"]').attributes('aria-checked')).toBe('false')

    const chosen = wrapper.find('[data-test="serialization-option-tool-response-compact"]')
    expect(chosen.attributes('aria-checked')).toBe('true')
    expect(
      wrapper.find('[data-test="serialization-option-tool-response-full"]').attributes('aria-checked')
    ).toBe('false')
  })

  it('sends each serialization axis to its own docs page', async () => {
    const wrapper = await mountOpen()
    const retrieveDoc = wrapper.find('[data-test="serialization-doc-tool-response"]').attributes('href')
    const directDoc = wrapper
      .find('[data-test="serialization-doc-direct-tool-response"]')
      .attributes('href')
    // Two different specs, two different pages — one link for both sent half the
    // readers to the wrong feature.
    expect(retrieveDoc).not.toBe(directDoc)
    expect(directDoc).toContain('schema-deferred')
  })

  it('reports a failed write instead of showing a mode it did not set', async () => {
    const wrapper = await mountOpen()
    patchConfig.mockResolvedValue({ success: false, error: 'invalid routing mode' })

    await wrapper.find('[data-test="mode-option-direct"]').trigger('click')
    await flushPromises()

    const store = useSystemStore()
    expect(store.toasts.at(-1)?.type).toBe('error')
    expect(wrapper.find('[data-test="mode-switcher-active"]').text()).toBe('Retrieve')
  })
})
