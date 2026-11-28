import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import SessionTimeline from '@/components/activity/SessionTimeline.vue'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { useAuthStore } from '@/stores/auth'
import {
  failureNavigation,
  formatSessionDuration,
  normalizeTimelineAt,
  timelineCliCommand,
} from '@/utils/workSessions'
import { formatTimeMillis } from '@/utils/datetime'
import { WS_FAILED, WS_SCOPED, timelinePage } from './fixtures/workSessions124'

// Spec 124 PR-3 (T111; FR-061..FR-064): one page of a session's timeline, opened
// at the position in the address.

const door = vi.hoisted(() => ({
  page: null as Record<string, unknown> | null,
  fail: null as (Error & { status?: number }) | null,
  bodies: null as Record<string, unknown> | null,
}))

/** What the door answers unless a test replaces it. */
const answer = vi.hoisted(() => (_id: string, params: { include_bodies?: boolean } = {}) => {
  if (door.fail) return Promise.reject(door.fail)
  if (params.include_bodies) return Promise.resolve(door.bodies)
  return Promise.resolve(door.page)
})

vi.mock('@/services/api', () => ({
  default: { getWorkSessionTimeline: vi.fn(answer) },
}))

const stub = { template: '<div />' }

async function mountTimeline(path = `/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:2`) {
  const api = (await import('@/services/api')).default as any
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(SessionTimeline, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router, api }
}

const rowAt = (wrapper: any, index: number) => wrapper.get(`[data-test="timeline-row"][data-index="${index}"]`)

describe('Session timeline (Spec 124 FR-062)', () => {
  beforeEach(async () => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    // A test may replace the door's answer; every test starts from the default.
    ;((await import('@/services/api')).default as any).getWorkSessionTimeline.mockImplementation(answer)
    door.page = timelinePage()
    door.fail = null
    door.bodies = null
  })

  it('opens at the position in the address, with 20 rows of context', async () => {
    const { api } = await mountTimeline()
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(1)
    expect(api.getWorkSessionTimeline).toHaveBeenCalledWith(WS_FAILED.id, { at: 'failure:2', before: 20, after: 20 })
  })

  it('opens at the first failure when the address names no position', async () => {
    const { api } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}`)
    expect(api.getWorkSessionTimeline).toHaveBeenCalledWith(WS_FAILED.id, { at: 'first_failure', before: 20, after: 20 })
  })

  it('highlights failed rows and marks the positioned one', async () => {
    const { wrapper } = await mountTimeline()
    expect(wrapper.findAll('[data-test="timeline-row"]')).toHaveLength(7)
    expect(rowAt(wrapper, 14).attributes('data-failed')).toBe('true')
    expect(rowAt(wrapper, 14).attributes('data-position')).toBe('true')
    expect(rowAt(wrapper, 14).attributes('aria-current')).toBe('true')
    expect(rowAt(wrapper, 12).attributes('data-failed')).toBe('true')
    expect(rowAt(wrapper, 12).attributes('data-position')).toBeUndefined()
    // A failure the page is not positioned at keeps its own surface.
    expect(rowAt(wrapper, 12).classes()).toContain('timeline-row-failed')
    expect(rowAt(wrapper, 12).classes()).not.toContain('timeline-row-position')
    expect(rowAt(wrapper, 14).classes()).toContain('timeline-row-position')
    expect(rowAt(wrapper, 11).classes()).not.toContain('timeline-row-failed')
    expect(rowAt(wrapper, 11).attributes('data-failed')).toBeUndefined()
    expect(rowAt(wrapper, 14).text()).toContain('github:list_issues')
    expect(rowAt(wrapper, 14).text()).toContain('The upstream server needs a login.')
    expect(rowAt(wrapper, 15).text()).toContain('Blocked')
  })

  it('styles upstream events and session rows apart from calls', async () => {
    const { wrapper } = await mountTimeline()
    const event = rowAt(wrapper, 13)
    expect(event.attributes('data-kind')).toBe('upstream_event')
    expect(event.classes()).toContain('timeline-row-event')
    expect(event.text()).toContain('github')
    expect(event.text()).toContain('The token refresh failed.')
    expect(event.text()).toContain('MCPX_OAUTH_REFRESH_FAILED')
    expect(event.get('[data-test="timeline-event-repeats"]').text()).toMatch(/^×3, last at \d{2}:\d{2}:\d{2}\.125$/)

    const start = rowAt(wrapper, 10)
    expect(start.attributes('data-kind')).toBe('session_start')
    expect(start.classes()).toContain('timeline-row-boundary')
    expect(start.text()).toContain('Session started')
    expect(rowAt(wrapper, 16).text()).toContain('Session ended')
    // Boundary rows are not stored rows: nothing to expand.
    expect(wrapper.find('[data-test="timeline-expand-10"]').exists()).toBe(false)

    expect(rowAt(wrapper, 11).classes()).not.toContain('timeline-row-event')
    expect(rowAt(wrapper, 11).classes()).not.toContain('timeline-row-boundary')
  })

  it('reads a recovery\'s text from its metadata and gives it its own tone', async () => {
    const page = timelinePage()
    ;(page.rows[3] as any).activity = {
      id: 'e13', type: 'upstream_event', status: 'success', timestamp: '2026-11-26T22:39:59.650Z', server_name: 'github',
      metadata: { kind: 'connection_restored', count: 1, message: 'The connection to the upstream server was restored.' },
    }
    door.page = page
    const { wrapper } = await mountTimeline()
    const event = rowAt(wrapper, 13)
    expect(event.text()).toContain('Connection restored')
    expect(event.text()).toContain('The connection to the upstream server was restored.')
    expect(event.text()).toContain('Recovered')
    expect(event.classes()).toContain('timeline-row-recovery')
    expect(event.find('[data-test="timeline-event-repeats"]').exists()).toBe(false)
  })

  it('names a built-in tool by its own name, with the server it was asked about beside it', async () => {
    const page = timelinePage()
    ;(page.rows[1] as any).kind = 'internal_call'
    ;(page.rows[1] as any).activity = { id: 'a11', type: 'internal_tool_call', status: 'success', timestamp: '2026-11-26T22:11:58.100Z', server_name: 'github', tool_name: 'call_tool_read', duration_ms: 30 }
    door.page = page
    const { wrapper } = await mountTimeline()
    expect(rowAt(wrapper, 11).get('code').text()).toBe('call_tool_read')
    expect(rowAt(wrapper, 11).text()).toContain('built-in tool · github')
  })

  it('marks an upstream event from before the session started', async () => {
    const page = timelinePage()
    ;(page.rows[3] as any).precedes_session = true
    door.page = page
    const { wrapper } = await mountTimeline()
    expect(rowAt(wrapper, 13).text()).toContain('before the session started')
  })

  it('prints times to the millisecond', async () => {
    const { wrapper } = await mountTimeline()
    expect(rowAt(wrapper, 14).get('[data-test="timeline-time"]').text()).toMatch(/^\d{2}:\d{2}:\d{2}\.250$/)
    expect(rowAt(wrapper, 13).get('[data-test="timeline-time"]').text()).toMatch(/^\d{2}:\d{2}:\d{2}\.650$/)
    expect(formatTimeMillis('2026-11-26T22:40:00.007Z')).toMatch(/^\d{2}:\d{2}:00\.007$/)
  })

  it('steps to the previous and the next failure and says "failure N of M"', async () => {
    const { wrapper, router, api } = await mountTimeline()
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 2 of 5')

    // The door answers each failure with its own position: 12, 14, 15.
    const positions: Record<string, { failure_index: number; position: number }> = {
      'failure:1': { failure_index: 1, position: 12 },
      'failure:2': { failure_index: 2, position: 14 },
      'failure:3': { failure_index: 3, position: 15 },
    }
    api.getWorkSessionTimeline.mockImplementation((_id: string, params: { at: string }) =>
      Promise.resolve(timelinePage(positions[params.at])))
    const positioned = () => wrapper.findAll('[data-test="timeline-row"][data-position="true"]').map(r => r.attributes('data-index'))
    expect(positioned()).toEqual(['14'])

    await wrapper.get('[data-test="timeline-next-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBe('failure:3')
    expect(router.currentRoute.value.query.ws).toBe(WS_FAILED.id)
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:3', before: 20, after: 20 })
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 3 of 5')
    expect(positioned()).toEqual(['15'])

    await wrapper.get('[data-test="timeline-prev-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBe('failure:2')
    expect(positioned()).toEqual(['14'])
    await wrapper.get('[data-test="timeline-prev-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBe('failure:1')
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 1 of 5')
    expect(positioned()).toEqual(['12'])
    expect(wrapper.get('[data-test="timeline-prev-failure"]').attributes('disabled')).toBeDefined()
  })

  it('disables the step that does not exist', async () => {
    door.page = timelinePage({ failure_index: 1, position: 12 })
    const first = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}`)
    expect(first.wrapper.get('[data-test="timeline-prev-failure"]').attributes('disabled')).toBeDefined()
    expect(first.wrapper.get('[data-test="timeline-next-failure"]').attributes('disabled')).toBeUndefined()

    door.page = timelinePage({ failure_index: 5, failure_total: 5 })
    const last = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:5`)
    expect(last.wrapper.get('[data-test="timeline-next-failure"]').attributes('disabled')).toBeDefined()
  })

  it('loads earlier and later rows around the same position', async () => {
    door.page = timelinePage({ has_after: true })
    const { wrapper, api } = await mountTimeline()
    await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:2', before: 70, after: 20 })
    await wrapper.get('[data-test="timeline-load-later"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:2', before: 70, after: 70 })
  })

  it('a page opened at the end is widened around the row it shows, not around an end that moves', async () => {
    // The session is still running: "end" a moment later is another row.
    door.page = timelinePage({ position: 15, failure_index: 3, has_before: true })
    const { wrapper, router, api } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&at=end`)
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'end', before: 20, after: 20 })
    await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBe('row:a15')
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'row:a15', before: 70, after: 20 })
  })

  it('shows the rows a wider page brings, and keeps them after a step to another failure', async () => {
    const later = { index: 16, kind: 'call', at: '2026-11-26T22:40:30.000Z', activity: { id: 'a16', type: 'tool_call', status: 'success', timestamp: '2026-11-26T22:40:30.000Z', server_name: 'github', tool_name: 'later_call' } }
    const wide = (overrides: Record<string, unknown> = {}) => {
      const page = timelinePage(overrides)
      page.rows.splice(6, 1, later as any, { index: 17, kind: 'session_end', at: '2026-11-26T22:41:05.000Z' })
      return page
    }
    door.page = timelinePage({ has_after: true })
    const { wrapper, api } = await mountTimeline()
    api.getWorkSessionTimeline.mockImplementation((_id: string, params: { at: string; after: number }) =>
      Promise.resolve(params.after > 20 ? wide(params.at === 'failure:3' ? { failure_index: 3, position: 15 } : {}) : timelinePage({ has_after: true })))
    expect(wrapper.find('[data-test="timeline-row"][data-index="16"][data-kind="call"]').exists()).toBe(false)
    await wrapper.get('[data-test="timeline-load-later"]').trigger('click')
    await flushPromises()
    expect(rowAt(wrapper, 16).text()).toContain('later_call')
    expect(wrapper.findAll('[data-test="timeline-row"]').length).toBe(8)
    await wrapper.get('[data-test="timeline-next-failure"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:3', before: 20, after: 70 })
    expect(rowAt(wrapper, 16).text()).toContain('later_call')
  })

  it('reads the same page again on Refresh', async () => {
    const { wrapper, api } = await mountTimeline()
    await wrapper.get('[data-test="timeline-refresh"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(2)
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:2', before: 20, after: 20 })
  })

  it('keeps the amount of context when stepping to another failure (US1 scenario 4)', async () => {
    const { wrapper, api } = await mountTimeline()
    await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="timeline-next-failure"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:3', before: 70, after: 20 })
  })

  it('at the door\'s limit "load earlier" continues from the earliest row shown', async () => {
    const { wrapper, router, api } = await mountTimeline()
    for (let i = 0; i < 5; i++) {
      await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
      await flushPromises()
    }
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:2', before: 250, after: 20 })
    expect(wrapper.get('[data-test="timeline-load-earlier"]').text()).toContain('Continue from the earliest row shown')
    await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
    await flushPromises()
    // The first stored row of the page (the session start row has no id).
    expect(router.currentRoute.value.query.at).toBe('row:a11')
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'row:a11', before: 50, after: 20 })
  })

  it('shows the error code of a failed call, and its duration in the details', async () => {
    const page = timelinePage()
    ;(page.rows[4] as any).activity.metadata = { error_code: 'MCPX_OAUTH_LOGIN_REQUIRED' }
    door.page = page
    door.bodies = { ...page, rows: [page.rows[4]] }
    const { wrapper } = await mountTimeline()
    expect(rowAt(wrapper, 14).get('[data-test="timeline-code"]').text()).toBe('MCPX_OAUTH_LOGIN_REQUIRED')
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-test="timeline-detail-14"]').text()
    expect(detail).toContain('Error code: MCPX_OAUTH_LOGIN_REQUIRED')
    expect(detail).toContain('Error class: auth')
    expect(detail).toContain('Duration: 42ms')
    expect(detail).toContain('Request id: req-a14')
  })

  it('an error on a row that is not the counted failure takes one line; the details keep it whole', async () => {
    // Every REST call is stored twice: the upstream call (the failure) and the
    // built-in wrapper around it, which repeats the same error.
    const text = 'The upstream server needs a login.\nSign in with: mcpproxy auth login --server=github'
    const page = timelinePage()
    const wrapperRow = {
      index: 15,
      kind: 'internal_call',
      at: '2026-11-26T22:40:00.251Z',
      activity: { ...(page.rows[4] as any).activity, id: 'a14w', type: 'internal_call', tool_name: 'call_tool_read', error_message: text, metadata: { error_code: 'MCPX_OAUTH_LOGIN_REQUIRED' } },
    }
    ;(page.rows[4] as any).activity.error_message = text
    page.rows.splice(5, 2, wrapperRow as any, { index: 16, kind: 'session_end', at: '2026-11-26T22:41:05.000Z' })
    door.page = page
    door.bodies = { ...page, rows: [wrapperRow] }
    const { wrapper } = await mountTimeline()
    // The failure itself prints its error whole.
    const failure = rowAt(wrapper, 14).get('[data-test="timeline-message"]')
    expect(failure.classes()).not.toContain('truncate')
    expect(failure.text()).toContain('mcpproxy auth login')
    // The wrapper keeps its Error tag and one line of the same text.
    const repeat = rowAt(wrapper, 15)
    expect(repeat.attributes('data-failed')).not.toBe('true')
    expect(repeat.text()).toContain('Error')
    const line = repeat.get('[data-test="timeline-message"]')
    expect(line.classes()).toContain('truncate')
    expect(line.attributes('title')).toBe(text)
    expect(line.find('[data-test="timeline-code"]').exists()).toBe(false)
    await wrapper.get('[data-test="timeline-expand-15"]').trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-test="timeline-detail-15"]').text()
    expect(detail).toContain('mcpproxy auth login --server=github')
    expect(detail).toContain('Error code: MCPX_OAUTH_LOGIN_REQUIRED')
  })

  it('shows why MCPProxy refused a call, in the row and in its details', async () => {
    const page = timelinePage()
    ;(page.rows[5] as any).bodies_removed = false
    ;(page.rows[5] as any).activity.metadata = { decision: 'blocked', reason: 'Permission denied: the profile does not allow delete_repo' }
    door.page = page
    door.bodies = { ...page, rows: [page.rows[5]] }
    const { wrapper } = await mountTimeline()
    expect(rowAt(wrapper, 15).text()).toContain('Permission denied: the profile does not allow delete_repo')
    await wrapper.get('[data-test="timeline-expand-15"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-message-15"]').text()).toBe('Permission denied: the profile does not allow delete_repo')
  })

  it('Refresh drops bodies read earlier, so a row opened again shows what the door answers now', async () => {
    const page = timelinePage()
    const first = structuredClone(page.rows[4]) as any
    first.activity.response = 'token=abc123'
    door.bodies = { ...page, rows: [first] }
    const { wrapper } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-14"]').text()).toContain('token=abc123')

    const masked = structuredClone(page.rows[4]) as any
    masked.activity.response = 'token=***'
    door.bodies = { ...page, rows: [masked] }
    await wrapper.get('[data-test="timeline-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="timeline-detail-14"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="timeline-cli"]').text()).not.toContain('--include-bodies')
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-test="timeline-detail-14"]').text()
    expect(detail).toContain('token=***')
    expect(detail).not.toContain('abc123')
  })

  it('a body answer asked for before Refresh does not come back after it', async () => {
    const page = timelinePage()
    const unmasked = structuredClone(page.rows[4]) as any
    unmasked.activity.response = 'token=abc123'
    const masked = structuredClone(page.rows[4]) as any
    masked.activity.response = 'token=***'
    const { wrapper, api } = await mountTimeline()
    // The first body request stays open across the refresh and answers last.
    let answerFirst!: (value: unknown) => void
    api.getWorkSessionTimeline.mockImplementationOnce(() => new Promise(resolve => { answerFirst = resolve }))
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="timeline-refresh"]').trigger('click')
    await flushPromises()
    door.bodies = { ...page, rows: [masked] }
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-14"]').text()).toContain('token=***')
    answerFirst({ ...page, rows: [unmasked] })
    await flushPromises()
    const detail = wrapper.get('[data-test="timeline-detail-14"]').text()
    expect(detail).toContain('token=***')
    expect(detail).not.toContain('abc123')
  })

  it('the CLI command names the page on screen while another one is on its way', async () => {
    const { wrapper, api } = await mountTimeline()
    let answerNext!: (value: unknown) => void
    api.getWorkSessionTimeline.mockImplementationOnce(() => new Promise(resolve => { answerNext = resolve }))
    await wrapper.get('[data-test="timeline-next-failure"]').trigger('click')
    await flushPromises()
    // Still failure 2 on screen, so still failure 2 in the command.
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 2 of 5')
    expect(wrapper.get('[data-test="timeline-cli"]').text()).toBe(`mcpproxy activity session ${WS_FAILED.id} --at failure:2`)
    answerNext(timelinePage({ failure_index: 3, position: 15 }))
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-cli"]').text()).toBe(`mcpproxy activity session ${WS_FAILED.id} --at failure:3`)
  })

  it('does not ask for bodies in the command when the opened row has left the page', async () => {
    const page = timelinePage()
    const withBodies = structuredClone(page.rows[4]) as any
    withBodies.activity.arguments = { repo: 'octo/demo' }
    door.bodies = { ...page, rows: [withBodies] }
    const { wrapper } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-cli"]').text()).toContain('--include-bodies')
    // The next page does not hold row a14.
    door.page = timelinePage({ rows: page.rows.slice(5), position: 15, failure_index: 3 })
    await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="timeline-detail-14"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="timeline-cli"]').text()).not.toContain('--include-bodies')
  })

  it('says so when retention removed the bodies after the page was read', async () => {
    const page = timelinePage()
    door.bodies = { ...page, rows: [{ ...(page.rows[4] as any), bodies_removed: true }] }
    const { wrapper } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    const bodies = wrapper.get('[data-test="timeline-bodies-14"]').text()
    expect(bodies).toContain('removed by retention')
    expect(bodies).not.toContain('No arguments recorded')
    // No bodies are on screen, so the command does not ask for them.
    expect(wrapper.get('[data-test="timeline-cli"]').text()).not.toContain('--include-bodies')
  })

  it('stops asking for bodies in the command once retention removed those of the opened row', async () => {
    const page = timelinePage({ has_after: true })
    const withBody = structuredClone(page.rows[4]) as any
    withBody.activity.arguments = { repo: 'acme/app' }
    door.page = page
    door.bodies = { ...page, rows: [withBody] }
    const { wrapper } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-cli"]').text()).toContain('--include-bodies')
    // Retention strips the bodies; the page is read again, wider.
    const stripped = timelinePage({ has_after: true })
    ;(stripped.rows[4] as any).bodies_removed = true
    door.page = stripped
    await wrapper.get('[data-test="timeline-load-later"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-14"]').text()).toContain('removed by retention')
    expect(wrapper.get('[data-test="timeline-cli"]').text()).not.toContain('--include-bodies')
  })

  it('a page read again, wider, reads the bodies of the open rows again', async () => {
    const page = timelinePage({ has_after: true })
    const first = structuredClone(page.rows[4]) as any
    first.activity.response = 'token=abc123'
    door.page = page
    door.bodies = { ...page, rows: [first] }
    const { wrapper, api } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-14"]').text()).toContain('token=abc123')
    // The scan finished meanwhile: the door now answers the masked response.
    const masked = structuredClone(page.rows[4]) as any
    masked.activity.response = 'token=***'
    door.bodies = { ...page, rows: [masked] }
    await wrapper.get('[data-test="timeline-load-later"]').trigger('click')
    await flushPromises()
    await flushPromises()
    const detail = wrapper.get('[data-test="timeline-detail-14"]').text()
    expect(detail).toContain('token=***')
    expect(detail).not.toContain('abc123')
    expect(api.getWorkSessionTimeline.mock.calls.filter((c: any[]) => c[1]?.include_bodies).length).toBe(2)
  })

  it('does not ask for bodies in the command when the open row has neither arguments nor a response', async () => {
    const page = timelinePage()
    const bare = structuredClone(page.rows[1]) as any
    delete bare.activity.arguments
    delete bare.activity.response
    door.bodies = { ...page, rows: [bare] }
    const { wrapper } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-11"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-11"]').text()).toContain('No arguments recorded')
    expect(wrapper.get('[data-test="timeline-cli"]').text()).not.toContain('--include-bodies')
  })

  it('the token and the client facts link to their subject only, and not at all to a roster a signed-in user cannot load', async () => {
    const { wrapper } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&profile=work-readonly`)
    const links = () => wrapper.get('[data-test="timeline-facts"]').findAll('a').map(a => a.attributes('href')!)
    const query = (href: string) => Object.fromEntries(new URL(href, 'http://x').searchParams)
    expect(query(links().find(href => href.startsWith('/tokens'))!)).toEqual({ token: 'nightly-bot' })
    expect(query(links().find(href => href.startsWith('/clients'))!)).toEqual({ client: 'cursor' })
    wrapper.unmount()

    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = { id: 'u1', email: 'u@example.com', role: 'user' } as any
    const tenant = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}`)
    const hrefs = tenant.wrapper.get('[data-test="timeline-facts"]').findAll('a').map(a => a.attributes('href')!)
    expect(hrefs.some(href => href.startsWith('/clients'))).toBe(false)
    expect(hrefs).toContain('/my/tokens')
    tenant.wrapper.unmount()
  })

  it('a failed body request offers a retry and does not add --include-bodies', async () => {
    const { wrapper, api } = await mountTimeline()
    api.getWorkSessionTimeline.mockImplementationOnce(() => Promise.reject(new Error('core said no')))
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-bodies-14"]').text()).toContain('core said no')
    expect(wrapper.get('[data-test="timeline-cli"]').text()).not.toContain('--include-bodies')
    const page = timelinePage()
    const withBodies = structuredClone(page.rows[4]) as any
    withBodies.activity.arguments = { repo: 'octo/demo' }
    door.bodies = { ...page, rows: [withBodies] }
    await wrapper.get('[data-test="timeline-bodies-14"] button').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-bodies-14"]').text()).toContain('octo/demo')
    expect(wrapper.get('[data-test="timeline-cli"]').text()).toContain('--include-bodies')
  })

  it('does not say "Copied" when the browser refuses the clipboard', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn(() => Promise.reject(new Error('denied'))) }, configurable: true,
    })
    const { wrapper } = await mountTimeline()
    await wrapper.get('[data-test="timeline-copy-cli"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-copy-cli"]').text()).toContain('Not copied')
    expect(wrapper.get('[data-test="timeline-copy-cli"]').text()).not.toContain('Copied')
  })

  it('offers no "load" control where the door says there is nothing more', async () => {
    door.page = timelinePage({ has_before: false, has_after: false })
    const { wrapper } = await mountTimeline()
    expect(wrapper.find('[data-test="timeline-load-earlier"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="timeline-load-later"]').exists()).toBe(false)
  })

  it('fetches bodies only when a row is expanded', async () => {
    const page = timelinePage()
    const withBodies = structuredClone(page.rows[4]) as any
    withBodies.activity.arguments = { repo: 'octo/demo' }
    withBodies.activity.response = '{"error":"unauthorized"}'
    door.bodies = { ...page, rows: [withBodies], position: 14 }
    const { wrapper, api } = await mountTimeline()
    expect(wrapper.text()).not.toContain('octo/demo')

    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, {
      at: 'row:a14', before: 0, after: 0, include_bodies: true,
    })
    const detail = wrapper.get('[data-test="timeline-detail-14"]')
    expect(detail.text()).toContain('octo/demo')
    expect(detail.text()).toContain('unauthorized')
    expect(wrapper.get('[data-test="timeline-expand-14"]').attributes('aria-expanded')).toBe('true')

    // Collapsing and opening again does not ask twice.
    const calls = api.getWorkSessionTimeline.mock.calls.length
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline.mock.calls.length).toBe(calls)
  })

  it('says when retention removed the bodies, and asks for nothing', async () => {
    const { wrapper, api } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-15"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="timeline-detail-15"]').text()).toContain('removed by retention')
  })

  it('offers the CLI command of the current view (FR-064)', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { wrapper } = await mountTimeline()
    expect(wrapper.get('[data-test="timeline-cli"]').text()).toBe(`mcpproxy activity session ${WS_FAILED.id} --at failure:2`)
    await wrapper.get('[data-test="timeline-load-earlier"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="timeline-copy-cli"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(`mcpproxy activity session ${WS_FAILED.id} --at failure:2 --before 70`)
    expect(wrapper.get('[data-test="timeline-copy-cli"]').text()).toContain('Copied')
  })

  it('notes a trimmed start and the lifetime failure count (FR-061)', async () => {
    const { wrapper } = await mountTimeline()
    expect(wrapper.get('[data-test="timeline-truncated-start"]').text()).toContain('removed by retention')
    const notice = wrapper.get('[data-test="timeline-earlier-failures"]')
    expect(notice.text()).toContain('7 failures')
    expect(notice.text()).toContain('5 are still in the log')
  })

  it('shows neither note when nothing was removed', async () => {
    door.page = timelinePage({
      session: { ...WS_FAILED, truncated_start_at: undefined },
      earlier_failures_not_stored: false,
    })
    const { wrapper } = await mountTimeline()
    expect(wrapper.find('[data-test="timeline-truncated-start"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="timeline-earlier-failures"]').exists()).toBe(false)
  })

  it('shows the session facts and keeps the link to its tool calls (FR-065)', async () => {
    const { wrapper, router } = await mountTimeline()
    const facts = wrapper.get('[data-test="timeline-facts"]').text()
    expect(facts).toContain('nightly-bot')
    expect(facts).toContain('payments')
    expect(facts).toContain('41m 05s')
    expect(facts).toContain('120')
    expect(facts).toContain('3 connections')
    // The session's attempts: its calls and the calls MCPProxy refused, which
    // are stored as decisions and are not in the Tool calls view by default.
    expect(wrapper.get('[data-test="timeline-calls-link"]').attributes('href')).toBe(
      router.resolve({ name: 'activity', query: { view: 'calls', session: WS_FAILED.id, type: 'tool_call,internal_tool_call,policy_decision' } }).fullPath,
    )
  })

  it('the link to the tool calls does not carry a time range the timeline did not apply', async () => {
    const { wrapper, router } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&from=-1h&to=2026-11-27T00:00:00Z&client=cursor`)
    expect(wrapper.get('[data-test="timeline-calls-link"]').attributes('href')).toBe(
      router.resolve({ name: 'activity', query: { view: 'calls', session: WS_FAILED.id, type: 'tool_call,internal_tool_call,policy_decision' } }).fullPath,
    )
  })

  it('a step to another failure drops bodies read earlier, like Refresh does', async () => {
    const page = timelinePage()
    const first = structuredClone(page.rows[4]) as any
    first.activity.response = 'token=abc123'
    door.bodies = { ...page, rows: [first] }
    const { wrapper, router, api } = await mountTimeline()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-detail-14"]').text()).toContain('token=abc123')

    // The core masked the response meanwhile; the page moves away and back.
    const masked = structuredClone(page.rows[4]) as any
    masked.activity.response = 'token=***'
    door.bodies = { ...page, rows: [masked] }
    await router.replace({ query: { ...router.currentRoute.value.query, at: 'failure:1' } })
    await flushPromises()
    await router.replace({ query: { ...router.currentRoute.value.query, at: 'failure:2' } })
    await flushPromises()
    await wrapper.get('[data-test="timeline-expand-14"]').trigger('click')
    await flushPromises()
    const detail = wrapper.get('[data-test="timeline-detail-14"]').text()
    expect(detail).toContain('token=***')
    expect(detail).not.toContain('abc123')
    expect(api.getWorkSessionTimeline.mock.calls.filter((c: any[]) => c[1]?.include_bodies).length).toBe(2)
  })

  it('a failure step stays usable while the next page is on its way, so the keyboard focus stays on it', async () => {
    const { wrapper, api } = await mountTimeline()
    let release!: (value: unknown) => void
    api.getWorkSessionTimeline.mockImplementation(() => new Promise(resolve => { release = resolve }))
    const next = wrapper.get('[data-test="timeline-next-failure"]')
    ;(next.element as HTMLElement).focus()
    await next.trigger('click')
    await flushPromises()
    // Still loading: the button is not disabled, and focus is still on it.
    expect(next.attributes('disabled')).toBeUndefined()
    expect(document.activeElement).toBe(next.element)
    release(timelinePage({ failure_index: 3, position: 15 }))
    await flushPromises()
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 3 of 5')
    expect(document.activeElement).toBe(next.element)
    wrapper.unmount()
  })

  it('keeps the first and the last failure in the facts, to the millisecond', async () => {
    const { wrapper } = await mountTimeline()
    const facts = wrapper.get('[data-test="timeline-facts"]').text()
    expect(facts).toMatch(/First failure\s*2026-11-2\d \d{2}:\d{2}:00\.000/)
    expect(facts).toMatch(/Last failure\s*2026-11-2\d \d{2}:\d{2}:00\.250/)
    expect(facts).toContain('The upstream server needs a login. · auth · MCPX_OAUTH_REFRESH_FAILED · github:list_issues')
  })

  it('says that the page filters do not narrow a timeline', async () => {
    setAvailableFeatures(['profile', 'client', 'token'])
    const scoped = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&client=cursor`)
    expect(scoped.wrapper.get('[data-test="timeline-scope-note"]').text()).toContain('are not applied to it')
    // So the link to the session's tool calls does not carry them either.
    const calls = new URL(scoped.wrapper.get('[data-test="timeline-calls-link"]').attributes('href')!, 'http://x').searchParams
    expect(calls.has('client')).toBe(false)
    expect(calls.get('session')).toBe(WS_FAILED.id)
    const plain = await mountTimeline()
    expect(plain.wrapper.find('[data-test="timeline-scope-note"]').exists()).toBe(false)
    setAvailableFeatures([])
  })

  /** A page of plain calls, indices `from`..`from + count - 1`, positioned at `position`. */
  const cleanPage = (session: Record<string, unknown>, from: number, count: number, position: number, extra: Record<string, unknown> = {}) => ({
    session,
    rows: Array.from({ length: count }, (_, i) => ({
      index: from + i,
      kind: 'call',
      at: `2026-11-26T22:00:${String(from + i).padStart(2, '0')}.000Z`,
      activity: { id: `c${from + i}`, type: 'tool_call', status: 'success', timestamp: `2026-11-26T22:00:${String(from + i).padStart(2, '0')}.000Z`, server_name: 'github', tool_name: 'echo', duration_ms: 3 },
    })),
    position,
    total_rows: 50,
    failure_index: 0,
    failure_total: 0,
    lifetime_failures: 0,
    has_before: from > 0,
    has_after: from + count < 50,
    ...extra,
  })

  it('a session that never failed moves to its most recent row when the address names no position', async () => {
    const clean = { ...WS_FAILED, failures: 0, policy_blocks: 0, stored_failures: 0, last_failure: undefined, first_failure_at: undefined }
    const api = (await import('@/services/api')).default as any
    // What the door answers: the earliest rows for the default, the last rows for `end`.
    api.getWorkSessionTimeline.mockImplementation((_id: string, params: { at: string }) =>
      Promise.resolve(params.at === 'end' ? cleanPage(clean, 29, 21, 49) : cleanPage(clean, 0, 21, 0)))
    const { wrapper, router } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}`)
    expect(router.currentRoute.value.query.at).toBe('end')
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'end', before: 20, after: 20 })
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(2)
    // The newest row is the one that is marked; the earliest page was never shown.
    const marked = wrapper.findAll('[data-test="timeline-row"][data-position="true"]')
    expect(marked.map(r => r.attributes('data-index'))).toEqual(['49'])
    expect(wrapper.find('[data-test="timeline-row"][data-index="0"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('No failures in this session')
  })

  it('a session whose failures were all removed by retention stays at its earliest row', async () => {
    // Lifetime failures, none stored: the door's earliest-row answer is the right one.
    const trimmed = { ...WS_FAILED, failures: 4, stored_failures: 0 }
    const api = (await import('@/services/api')).default as any
    api.getWorkSessionTimeline.mockImplementation(() =>
      Promise.resolve(cleanPage(trimmed, 0, 21, 0, { lifetime_failures: 4, earlier_failures_not_stored: true })))
    const { wrapper, router } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}`)
    expect(router.currentRoute.value.query.at).toBeUndefined()
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(1)
    expect(wrapper.findAll('[data-test="timeline-row"][data-position="true"]').map(r => r.attributes('data-index'))).toEqual(['0'])
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('No failures left in the log')
    expect(wrapper.get('[data-test="timeline-earlier-failures"]').text()).toContain('4 failures')
  })

  it('renders a scoped caller\'s session without the administrator-only fields', async () => {
    door.page = timelinePage({ session: WS_SCOPED, earlier_failures_not_stored: false })
    const { wrapper } = await mountTimeline(`/activity?view=sessions&ws=${WS_SCOPED.id}`)
    expect(wrapper.text()).not.toMatch(/undefined|NaN|null/)
    expect(wrapper.find('[data-test="timeline-truncated-start"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="timeline-facts"]').text()).not.toContain('connection')
    expect(wrapper.get('[data-test="timeline-facts"]').text()).not.toContain('Profile')
  })

  it('announces "session not found": it arrives after the focus moved to the heading', async () => {
    door.fail = Object.assign(new Error('not found'), { status: 404 })
    const { wrapper } = await mountTimeline()
    expect(wrapper.get('[data-test="timeline-not-found"]').attributes('role')).toBe('alert')
  })

  it('a 404 shows "session not found" with a way back to the list', async () => {
    door.fail = Object.assign(new Error('Work session not found'), { status: 404 })
    const { wrapper } = await mountTimeline('/activity?view=sessions&ws=ws-gone&failed=true')
    const state = wrapper.get('[data-test="timeline-not-found"]')
    expect(state.text()).toContain('Session not found')
    const back = new URL(state.get('[data-test="timeline-back-to-list"]').attributes('href')!, 'http://x').searchParams
    expect(back.get('view')).toBe('sessions')
    expect(back.has('ws')).toBe(false)
    expect(back.has('at')).toBe(false)
    expect(back.get('failed')).toBe('true')
    expect(wrapper.find('[data-test="timeline-row"]').exists()).toBe(false)
  })

  it('a position that is not in the session offers the first failure', async () => {
    door.fail = Object.assign(new Error('Invalid at. No such failure or row in this session'), { status: 400 })
    const { wrapper, router, api } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:99`)
    expect(wrapper.get('[data-test="timeline-error"]').text()).toContain('No such failure or row')
    // The error is announced: it replaced the control the user was on.
    expect(wrapper.get('[data-test="timeline-error"]').attributes('role')).toBe('alert')
    door.fail = null
    door.page = timelinePage({ failure_index: 1, position: 12 })
    await wrapper.get('[data-test="timeline-open-first-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBeUndefined()
    // The door is asked for the first failure, and that row is the one marked.
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'first_failure', before: 20, after: 20 })
    expect(wrapper.findAll('[data-test="timeline-row"][data-position="true"]').map(r => r.attributes('data-index'))).toEqual(['12'])
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 1 of 5')
  })

  it('never sends a malformed position to the door', async () => {
    const { api } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&at=sideways`)
    expect(api.getWorkSessionTimeline).toHaveBeenCalledWith(WS_FAILED.id, { at: 'first_failure', before: 20, after: 20 })
  })
})

describe('work-session helpers', () => {
  it('normalizes the position', () => {
    expect(normalizeTimelineAt(undefined)).toBe('first_failure')
    expect(normalizeTimelineAt('')).toBe('first_failure')
    expect(normalizeTimelineAt('end')).toBe('end')
    expect(normalizeTimelineAt('failure:3')).toBe('failure:3')
    expect(normalizeTimelineAt('failure:0')).toBe('first_failure')
    expect(normalizeTimelineAt('failure:x')).toBe('first_failure')
    // The door and the CLI read the ordinal as a number: 02 is the second failure.
    expect(normalizeTimelineAt('failure:02')).toBe('failure:2')
    expect(normalizeTimelineAt('failure:00')).toBe('first_failure')
    expect(normalizeTimelineAt('failure:0000000002')).toBe('failure:2')
    expect(normalizeTimelineAt('failure:000000000000')).toBe('first_failure')
    expect(normalizeTimelineAt('row:01JABC')).toBe('row:01JABC')
    expect(normalizeTimelineAt('row:')).toBe('first_failure')
    expect(normalizeTimelineAt('sideways')).toBe('first_failure')
  })

  it('builds the CLI command PR-2 accepts', () => {
    expect(timelineCliCommand({ id: 'ws-1', at: 'first_failure', before: 20, after: 20 })).toBe('mcpproxy activity session ws-1')
    expect(timelineCliCommand({ id: 'ws-1', at: 'end', before: 20, after: 20 })).toBe('mcpproxy activity session ws-1 --at end')
    expect(timelineCliCommand({ id: 'ws-1', at: 'row:01JABC', before: 0, after: 250, includeBodies: true })).toBe(
      'mcpproxy activity session ws-1 --at row:01JABC --before 0 --after 250 --include-bodies',
    )
    // The door answers at most 500 rows, the positioned one included, and the
    // CLI refuses a request for more: with both sides at the limit the page on
    // screen is 250 before and 249 after.
    expect(timelineCliCommand({ id: 'ws-1', at: 'row:01JABC', before: 250, after: 250 })).toBe(
      'mcpproxy activity session ws-1 --at row:01JABC --before 250 --after 249',
    )
    expect(timelineCliCommand({ id: 'ws-1', at: 'end', before: 250, after: 249 })).toBe(
      'mcpproxy activity session ws-1 --at end --before 250 --after 249',
    )
    // An id is quoted when a shell would read it as more than one word.
    expect(timelineCliCommand({ id: "ws it's", at: 'first_failure', before: 20, after: 20 })).toBe(
      "mcpproxy activity session 'ws it'\\''s'",
    )
  })

  it('formats a duration', () => {
    expect(formatSessionDuration(0)).toBe('0ms')
    expect(formatSessionDuration(850)).toBe('850ms')
    expect(formatSessionDuration(12_000)).toBe('12s')
    expect(formatSessionDuration(2_465_000)).toBe('41m 05s')
    expect(formatSessionDuration(7_380_000)).toBe('2h 03m')
    expect(formatSessionDuration(93_600_000)).toBe('1d 02h')
    expect(formatSessionDuration(-5)).toBe('0ms')
  })

  it('derives the failure steps from a page', () => {
    expect(failureNavigation(timelinePage() as any)).toEqual({ label: 'failure 2 of 5', prev: 'failure:1', next: 'failure:3' })
    // Positioned on a row that is not a failure: "previous" is the failure before it.
    expect(failureNavigation(timelinePage({ position: 16, failure_index: 3 }) as any)).toEqual({
      label: 'after failure 3 of 5', prev: 'failure:3', next: 'failure:4',
    })
    expect(failureNavigation(timelinePage({ position: 10, failure_index: 0 }) as any)).toEqual({
      label: 'before failure 1 of 5', prev: undefined, next: 'failure:1',
    })
    expect(failureNavigation(timelinePage({ position: 10, failure_index: 0, failure_total: 0, lifetime_failures: 0 }) as any)).toEqual({
      label: 'No failures in this session', prev: undefined, next: undefined,
    })
    expect(failureNavigation(timelinePage({ position: 10, failure_index: 0, failure_total: 0 }) as any)).toEqual({
      label: 'No failures left in the log', prev: undefined, next: undefined,
    })
    // The positioned row is missing from the page (removed between the door's
    // index pass and its read): a position asked for as a failure is that
    // failure, so "previous" leads to the one before it and not to itself.
    const missing = timelinePage({ rows: timelinePage().rows.filter(r => r.index !== 14) }) as any
    expect(failureNavigation(missing, 'failure:2')).toEqual({ label: 'failure 2 of 5', prev: 'failure:1', next: 'failure:3' })
    expect(failureNavigation(missing, 'row:a15')).toEqual({ label: 'after failure 2 of 5', prev: 'failure:2', next: 'failure:3' })
    const firstMissing = timelinePage({ rows: [], position: 12, failure_index: 1 }) as any
    expect(failureNavigation(firstMissing, 'first_failure')).toEqual({ label: 'failure 1 of 5', prev: undefined, next: 'failure:2' })
  })
})
