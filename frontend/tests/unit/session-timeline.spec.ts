import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import SessionTimeline from '@/components/activity/SessionTimeline.vue'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
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

vi.mock('@/services/api', () => ({
  default: {
    getWorkSessionTimeline: vi.fn((_id: string, params: { include_bodies?: boolean } = {}) => {
      if (door.fail) return Promise.reject(door.fail)
      if (params.include_bodies) return Promise.resolve(door.bodies)
      return Promise.resolve(door.page)
    }),
  },
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
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
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

    door.page = timelinePage({ failure_index: 3 })
    await wrapper.get('[data-test="timeline-next-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBe('failure:3')
    expect(router.currentRoute.value.query.ws).toBe(WS_FAILED.id)
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:3', before: 20, after: 20 })
    expect(wrapper.get('[data-test="timeline-failure-label"]').text()).toBe('failure 3 of 5')

    await wrapper.get('[data-test="timeline-prev-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBe('failure:2')
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
    expect(wrapper.get('[data-test="timeline-calls-link"]').attributes('href')).toBe(
      router.resolve({ name: 'activity', query: { view: 'calls', session: WS_FAILED.id } }).fullPath,
    )
  })

  it('keeps the first and the last failure in the facts, to the millisecond', async () => {
    const { wrapper } = await mountTimeline()
    const facts = wrapper.get('[data-test="timeline-facts"]').text()
    expect(facts).toMatch(/First failure\s*2026-11-2\d \d{2}:12:00\.000/)
    expect(facts).toMatch(/Last failure\s*2026-11-2\d \d{2}:40:00\.250/)
    expect(facts).toContain('The upstream server needs a login. · auth · MCPX_OAUTH_REFRESH_FAILED · github:list_issues')
  })

  it('says that the page filters do not narrow a timeline', async () => {
    setAvailableFeatures(['profile', 'client', 'token'])
    const scoped = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&client=cursor`)
    expect(scoped.wrapper.get('[data-test="timeline-scope-note"]').text()).toContain('do not change this timeline')
    const plain = await mountTimeline()
    expect(plain.wrapper.find('[data-test="timeline-scope-note"]').exists()).toBe(false)
    setAvailableFeatures([])
  })

  it('a session that never failed moves to its most recent row when the address names no position', async () => {
    const clean = { ...WS_FAILED, failures: 0, policy_blocks: 0, stored_failures: 0, last_failure: undefined, first_failure_at: undefined }
    door.page = timelinePage({ session: clean, position: 0, failure_index: 0, failure_total: 0, lifetime_failures: 0, earlier_failures_not_stored: false })
    const { router, api } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}`)
    expect(router.currentRoute.value.query.at).toBe('end')
    expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'end', before: 20, after: 20 })
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(2)
  })

  it('renders a scoped caller\'s session without the administrator-only fields', async () => {
    door.page = timelinePage({ session: WS_SCOPED, earlier_failures_not_stored: false })
    const { wrapper } = await mountTimeline(`/activity?view=sessions&ws=${WS_SCOPED.id}`)
    expect(wrapper.text()).not.toMatch(/undefined|NaN|null/)
    expect(wrapper.find('[data-test="timeline-truncated-start"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="timeline-facts"]').text()).not.toContain('connection')
    expect(wrapper.get('[data-test="timeline-facts"]').text()).not.toContain('Profile')
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
    const { wrapper, router } = await mountTimeline(`/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:99`)
    expect(wrapper.get('[data-test="timeline-error"]').text()).toContain('No such failure or row')
    door.fail = null
    await wrapper.get('[data-test="timeline-open-first-failure"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.at).toBeUndefined()
    expect(wrapper.findAll('[data-test="timeline-row"]').length).toBeGreaterThan(0)
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
  })
})
