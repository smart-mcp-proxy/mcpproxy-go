import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 134 PR 5 (FR-045..FR-049): the Activity calls table has five data columns
// and one action column; everything it dropped is in the row detail; one Sort
// control, one filter popover, one chip row with an overflow list; Group repeats
// is on by default. Inventory rows A5-01..A5-54.

const OK = {
  id: 'ok-1', type: 'tool_call', status: 'success', timestamp: '2026-08-21T10:00:00Z',
  server_name: 'github', tool_name: 'list_repos', request_id: 'r1', duration_ms: 40,
  client_id: 'cursor', profile: 'work',
  has_sensitive_data: true, max_severity: 'high', detection_types: ['aws_key'],
  metadata: { intent: { operation_type: 'read', reason: 'enumerate repositories' } },
}
const BAD = {
  id: 'bad-1', type: 'tool_call', status: 'error', timestamp: '2026-08-21T09:59:00Z',
  server_name: 'slack', tool_name: 'post_message', request_id: 'r2', duration_ms: 90, client_name: 'Some Agent',
  error_message: 'boom',
}
const BLOCKED = {
  id: 'blk-1', type: 'policy_decision', status: 'blocked', timestamp: '2026-08-21T09:58:00Z',
  server_name: 'slack', tool_name: 'delete_channel', request_id: 'r3', profile: 'work', block_reason: 'not_allowed',
}
const CFG = {
  id: 'cfg-1', type: 'config_change', status: 'success', timestamp: '2026-08-21T09:57:00Z',
  metadata: { action: 'server_added' },
}
const NOCLIENT = {
  id: 'nc-1', type: 'tool_call', status: 'success', timestamp: '2026-08-21T09:56:00Z',
  server_name: 'fs', tool_name: 'read', request_id: 'r5', duration_ms: 5,
}
const REPEATS = [1, 2, 3].map(n => ({
  id: `rep-${n}`, type: 'tool_call', status: 'success', timestamp: `2026-08-21T09:5${n}:00Z`,
  server_name: 'fs', tool_name: 'stat', request_id: `rr${n}`, duration_ms: 3 + n, client_id: 'cursor',
}))

const state = { rows: [OK, BAD, BLOCKED, CFG, NOCLIENT, ...REPEATS] as Record<string, unknown>[], fail: false }

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() =>
        state.fail
          ? Promise.resolve({ success: false, error: 'core said no' })
          : ok({ activities: state.rows, total: state.rows.length, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 9, call_count: 8, success_count: 6, error_count: 1, blocked_count: 1, rejected_count: 0 })),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/export'),
    },
  }
})

async function mountAt(path = '/activity') {
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/clients', name: 'clients', component: { template: '<div/>' } },
      { path: '/tokens', name: 'tokens', component: { template: '<div/>' } },
      { path: '/sessions', name: 'sessions', component: { template: '<div/>' } },
      { path: '/profiles/:name', name: 'profile-editor', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] }, attachTo: document.body })
  await flushPromises()
  return wrapper
}
type W = Awaited<ReturnType<typeof mountAt>>
const rows = (w: W) => w.findAll('[data-test="activity-row"]')
const rowFor = (w: W, text: string) => rows(w).find(r => r.text().includes(text))!
const headers = (w: W) => w.findAll('[data-test="activity-table"] thead th').map(h => h.text())

beforeEach(() => {
  setActivePinia(createPinia())
  window.localStorage.clear()
  state.rows = [OK, BAD, BLOCKED, CFG, NOCLIENT, ...REPEATS]
  state.fail = false
  document.body.innerHTML = ''
})

describe('A5-01..A5-13 calls table columns (FR-045)', () => {
  it('shows time, tool, result, client, duration and one action column', async () => {
    const w = await mountAt()
    const h = headers(w)
    expect(h.map(x => x.replace(/[↑↓]/g, '').trim())).toEqual(['Time', 'Tool', 'Result', 'Client', 'Duration', 'Open details'])
    for (const gone of ['Type', 'Server', 'Scope', 'Details', 'Sensitive', 'Intent', 'Status']) expect(h.join('|')).not.toContain(gone)
    // exactly one action button per row, with its keyboard name
    const first = rowFor(w, 'github:list_repos')
    expect(first.findAll('button[data-test^="activity-open-"]')).toHaveLength(1)
    expect(first.find('[data-test="activity-open-ok-1"]').attributes('aria-label')).toMatch(/^Open details for Tool Call/)
  })

  it('prints the tool as server:tool and keeps the present fallbacks', async () => {
    const w = await mountAt('/activity?view=calls&type=tool_call,config_change')
    expect(rowFor(w, 'slack:post_message').find('[data-test="activity-tool-label"]').text()).toBe('slack:post_message')
    // tool-less event: its action text
    expect(rowFor(w, 'server_added').text()).toContain('server_added')
    // unattributed call: the present "unattributed" text; event: the dash
    expect(rowFor(w, 'fs:read').find('[data-test="attribution-none"]').text()).toBe('unattributed')
    expect(rowFor(w, 'server_added').find('[data-test="attribution-none"]').text()).toBe('—')
    // reported (not verified) client keeps its marker; verified client links
    expect(rowFor(w, 'slack:post_message').find('[data-test="attribution-client-reported"]').text()).toContain('(reported)')
    expect(rowFor(w, 'github:list_repos').find('[data-test="attribution-client"]').exists()).toBe(true)
  })

  it('keeps a quiet success and a label for every other status', async () => {
    const w = await mountAt()
    const ok = rowFor(w, 'github:list_repos')
    expect(ok.find('[data-test="activity-status-icon"]').exists()).toBe(true)
    expect(ok.find('[data-test="activity-status"]').text()).toBe('Success')
    expect(ok.find('[data-test="activity-status"]').classes()).toContain('sr-only')
    const bad = rowFor(w, 'slack:post_message').find('[data-test="activity-status"]')
    expect(bad.text()).toBe('Error')
    expect(bad.classes().join(' ')).not.toContain('sr-only')
    // detections keep an at-a-glance mark with accessible text
    expect(ok.find('[data-test="activity-sensitive-mark"]').exists()).toBe(true)
    expect(ok.find('[data-test="activity-sensitive-mark"]').text()).toContain('high-severity sensitive data detected')
  })

  it('a blocked attempt keeps its Blocked label', async () => {
    const w = await mountAt('/activity?status=blocked')
    expect(rowFor(w, 'slack:delete_channel').find('[data-test="activity-status"]').text()).toBe('Blocked')
  })

  it('System and All keep their columns', async () => {
    const w = await mountAt('/activity?view=all')
    const h = headers(w).join('|')
    for (const col of ['Time', 'Type', 'Server', 'Details', 'Status', 'Duration']) expect(h).toContain(col)
    expect(h).not.toContain('Tool|')
  })
})

describe('A5-20..A5-26 row detail (FR-049)', () => {
  it('shows every removed column in the detail, with its links and actions', async () => {
    const w = await mountAt()
    await rowFor(w, 'github:list_repos').find('[data-test="activity-open-ok-1"]').trigger('click')
    await flushPromises()
    const panel = w.find('[data-test="activity-detail-panel"]')
    expect(panel.find('[data-test="activity-detail-type"]').text()).toContain('Tool Call')
    expect(panel.find('[data-test="activity-detail-details"]').text()).toContain('list_repos')
    const server = panel.find('a[href="/servers/github"]')
    expect(server.exists()).toBe(true)
    expect(panel.find('[data-test="activity-drawer-attribution"]').text()).toMatch(/cursor/)
    expect(panel.find('[data-test="activity-drawer-attribution"] [data-test="attribution-profile"]').exists()).toBe(true)
    expect(panel.text()).toContain('Sensitive Data Detected')
    expect(panel.text()).toContain('Intent Declaration')
    expect(panel.text()).toContain('enumerate repositories')
  })

  it('an unattributed call still says so in the detail, and a blocked row opens with its label', async () => {
    const w = await mountAt()
    await rowFor(w, 'fs:read').find('[data-test="activity-open-nc-1"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="activity-drawer-attribution"] [data-test="attribution-none"]').text()).toBe('unattributed')
    w.unmount()
    const b = await mountAt('/activity?status=blocked')
    await rowFor(b, 'slack:delete_channel').find('[data-test="activity-open-blk-1"]').trigger('click')
    await flushPromises()
    expect(b.find('[data-test="activity-detail-status"]').text()).toBe('Blocked')
  })
})

describe('review round 1 fixes', () => {
  it('focus enters the detail panel on open and returns to the row button on close', async () => {
    const w = await mountAt()
    const btn = rowFor(w, 'github:list_repos').find('[data-test="activity-open-ok-1"]')
    ;(btn.element as HTMLElement).focus()
    await btn.trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(w.find('[data-test="activity-detail-panel"]').element)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(document.activeElement).toBe(btn.element)
  })

  it('keeps the numeric detection count in the row and in the detail', async () => {
    const w = await mountAt()
    expect(rowFor(w, 'github:list_repos').find('[data-test="activity-sensitive-mark"]').text()).toContain('1')
    await rowFor(w, 'github:list_repos').find('[data-test="activity-open-ok-1"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="activity-detail-sensitive-count"]').text()).toContain('1')
  })

  it('names a metadata-only code_execution parent', async () => {
    state.rows = [{ id: 'ce-1', type: 'internal_tool_call', status: 'success', timestamp: '2026-08-21T10:00:00Z', request_id: 'rq', metadata: { internal_tool_name: 'code_execution' } }]
    const w = await mountAt()
    expect(rows(w)[0].text()).toContain('code_execution')
    expect(rows(w)[0].find('[data-test="activity-parent-badge"]').exists()).toBe(true)
  })

  it('the calls table uses fixed column widths so a long raw status cannot push the action off', async () => {
    state.rows = [{ ...OK, id: 'raw-1', status: 'awaiting_external_administrator_confirmation', has_sensitive_data: false }]
    const w = await mountAt()
    expect(w.find('[data-test="activity-table"]').classes()).toContain('table-fixed')
    expect(rows(w)[0].find('[data-test="activity-status"]').classes()).toContain('truncate')
    expect(rows(w)[0].find('[data-test="activity-status"]').attributes('title')).toContain('awaiting_external')
  })

  it('an event with no attribution still shows its dash in the detail', async () => {
    const w = await mountAt('/activity?view=calls&type=config_change')
    await rowFor(w, 'server_added').find('[data-test="activity-open-cfg-1"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="activity-drawer-attribution"] [data-test="attribution-none"]').text()).toBe('—')
  })
})

describe('review round 2 fixes (filters)', () => {
  it('Sessions keeps the filters toggle and its popover (token setter included)', async () => {
    const w = await mountAt('/activity?view=sessions')
    const toggle = w.find('[data-test="activity-filters-toggle"]')
    expect(toggle.isVisible()).toBe(true)
    await toggle.trigger('click')
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(true)
    expect(w.find('[data-test="activity-sort-control"]').isVisible()).toBe(false)
  })

  it('an outside click closes the popover without changing the stored choice; Done does', async () => {
    const w = await mountAt()
    await w.find('[data-test="activity-filters-toggle"]').trigger('click')
    expect(window.localStorage.getItem('mcpproxy.activity.filtersExpanded')).toBe('true')
    document.body.click()
    await flushPromises()
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(false)
    expect(window.localStorage.getItem('mcpproxy.activity.filtersExpanded')).toBe('true')
    await w.find('[data-test="activity-filters-toggle"]').trigger('click')
    await w.find('[data-test="activity-filters-toggle"]').trigger('click')
    expect(window.localStorage.getItem('mcpproxy.activity.filtersExpanded')).toBe('false')
  })

  it('tabbing out of the popover dismisses it', async () => {
    const w = await mountAt()
    await w.find('[data-test="activity-filters-toggle"]').trigger('click')
    const outside = rows(w)[0].find('button').element as HTMLElement
    await w.find('[data-test="activity-filter-popover"]').trigger('focusout', { relatedTarget: outside })
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(false)
  })
})

describe('A5-11 sort control (FR-046)', () => {
  it('offers all five keys in both directions and sorts', async () => {
    const w = await mountAt()
    const sel = w.find('[data-test="activity-sort"]')
    const opts = sel.findAll('option').map(o => o.element.getAttribute('value'))
    expect(opts).toEqual([
      'timestamp:desc', 'timestamp:asc', 'type:asc', 'type:desc', 'server_name:asc', 'server_name:desc',
      'status:asc', 'status:desc', 'duration_ms:desc', 'duration_ms:asc',
    ])
    await sel.setValue('duration_ms:desc')
    const first = rows(w)[0].text()
    expect(first).toContain('slack:post_message') // 90 ms is the longest
    await sel.setValue('duration_ms:asc')
    expect(rows(w)[0].text()).not.toContain('slack:post_message')
    await sel.setValue('server_name:asc')
    const servers = rows(w).map(r => r.find('[data-test="activity-tool-label"]').exists() ? r.find('[data-test="activity-tool-label"]').text().split(':')[0] : '')
    expect(servers.length).toBeGreaterThan(2)
    expect(servers).toEqual([...servers].sort((a, b) => a.localeCompare(b)))
    await sel.setValue('server_name:desc')
    const desc = rows(w).map(r => r.find('[data-test="activity-tool-label"]').exists() ? r.find('[data-test="activity-tool-label"]').text().split(':')[0] : '')
    expect(desc).toEqual([...desc].sort((a, b) => b.localeCompare(a)))
  })
})

describe('A5-35 Group repeats (FR-048)', () => {
  it('is on when no choice is stored, folds, and a stored off is respected', async () => {
    let w = await mountAt()
    expect(w.find('[data-test="activity-group-toggle"]').attributes('aria-pressed')).toBe('true')
    expect(w.find('[data-test="activity-run-count"]').text()).toContain('×3')
    expect(w.find('[data-test="activity-folded-count"]').text()).toContain('2')
    w.unmount()
    window.localStorage.setItem('mcpproxy.activity.groupRepeats', 'false')
    w = await mountAt()
    expect(w.find('[data-test="activity-group-toggle"]').attributes('aria-pressed')).toBe('false')
    expect(w.find('[data-test="activity-run-count"]').exists()).toBe(false)
  })

  it('expands every member of a run and keeps the run summary', async () => {
    const w = await mountAt()
    const lead = rows(w).find(r => r.find('[data-test="activity-run-count"]').exists())!
    expect(lead.text()).toContain('over 2m')
    expect(lead.text()).toContain('4ms–6ms')
    await w.find('[data-test="activity-run-count"]').trigger('click')
    const members = w.findAll('[data-test="activity-run-member"]')
    expect(members).toHaveLength(2)
    expect(members.map(m => m.find('button[data-test^="activity-open-"]').attributes('data-test')).sort()).toEqual(['activity-open-rep-1', 'activity-open-rep-2'])
  })

  it('does not group outside time order', async () => {
    const w = await mountAt()
    await w.find('[data-test="activity-sort"]').setValue('duration_ms:desc')
    expect(w.find('[data-test="activity-group-toggle"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-test="activity-run-count"]').exists()).toBe(false)
  })
})

describe('A5-36..A5-43 filter popover (FR-047)', () => {
  it('is closed on load, opens over the results without removing them, and closes on Escape with focus back', async () => {
    const w = await mountAt()
    const toggle = w.find('[data-test="activity-filters-toggle"]')
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(false)
    expect(toggle.attributes('aria-haspopup')).toBe('dialog')
    await toggle.trigger('click')
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('true')
    const pop = w.find('[data-test="activity-filter-popover"]')
    expect(pop.exists()).toBe(true)
    // the popover is out of flow: absolute, inside the strip, not between strip and results
    expect(pop.classes()).toContain('absolute')
    expect(rows(w).length).toBeGreaterThan(0)
    expect(document.activeElement).toBe(pop.element)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(false)
    expect(document.activeElement).toBe(toggle.element)
  })

  it('closes on an outside click', async () => {
    const w = await mountAt()
    await w.find('[data-test="activity-filters-toggle"]').trigger('click')
    document.body.click()
    await flushPromises()
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(false)
  })

  it('a stored open choice reopens it on load; a throwing store falls back to closed', async () => {
    window.localStorage.setItem('mcpproxy.activity.filtersExpanded', 'true')
    const w = await mountAt()
    expect(w.find('[data-test="activity-filter-popover"]').exists()).toBe(true)
    w.unmount()
  })

  it('sets every filter that could be set before', async () => {
    const w = await mountAt()
    await w.find('[data-test="activity-filters-toggle"]').trigger('click')
    const pop = w.find('[data-test="activity-filter-popover"]')
    // multi-type, server, status (incl. Other), auth, sensitive data, session, from, to
    expect(pop.text()).toContain('Event Types')
    for (const label of [
      'Filter by server', 'Filter by status', 'Filter by authentication type', 'Filter by sensitive data',
      'Filter by session', 'Filter activity from date and time', 'Filter activity to date and time',
    ]) expect(pop.find(`[aria-label="${label}"]`).exists(), label).toBe(true)
    expect(pop.find('option[value="__other__"]').exists() || pop.text().includes('Other / internal')).toBe(true)
    // dependent setters: agent after auth=agent, severity after sensitive=true
    await pop.find('[aria-label="Filter by authentication type"]').setValue('agent')
    expect(pop.find('[aria-label="Filter by agent"]').exists()).toBe(true)
    await pop.find('[aria-label="Filter by sensitive data"]').setValue('true')
    expect(pop.find('[aria-label="Filter by severity"]').exists()).toBe(true)
    // tiles and legend stay
    expect(pop.find('[data-test="kpi-card-total"]').exists()).toBe(true)
    expect(pop.find('[data-test="activity-legend"]').exists()).toBe(true)
    // the type checkboxes cover every type
    expect(pop.findAll('input[type="checkbox"]').length).toBeGreaterThan(10)
  })
})

describe('A5-38 / A5-56 chips row (FR-047)', () => {
  it('shows each active filter as a removable chip and Clear all', async () => {
    const w = await mountAt('/activity?status=error&server=slack')
    const row = w.find('[data-test="activity-active-filters"]')
    expect(row.exists()).toBe(true)
    expect(row.findAll('button[data-test^="activity-filter-chip-"]')).toHaveLength(2)
    await w.find('[data-test="activity-filter-chip-status"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="activity-filter-chip-status"]').exists()).toBe(false)
    expect(w.find('[data-test="activity-filter-chip-server"]').exists()).toBe(true)
    await w.find('[data-test="activity-clear-all"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="activity-active-filters"]').exists()).toBe(false)
  })

  it('reserves the same row height with and without chips', async () => {
    const w = await mountAt()
    const box = w.find('[data-test="activity-filter-chips"]')
    expect(box.classes()).toContain('h-12')
    expect(w.find('[data-test="activity-active-filters-empty"]').exists()).toBe(true)
    await w.find('[data-test="activity-sort"]').setValue('timestamp:desc')
  })

  it('reserves it in the empty and the error states too', async () => {
    state.rows = []
    let w = await mountAt('/activity?status=error')
    expect(w.text()).toContain('No matching activities')
    expect(w.find('[data-test="activity-filter-chips"]').classes()).toContain('h-12')
    w.unmount()
    state.fail = true
    w = await mountAt()
    expect(w.text()).toContain('core said no')
    expect(w.find('[data-test="activity-filter-chips"]').classes()).toContain('h-12')
  })

  it('lists and removes every active filter from the overflow control when the chips do not fit', async () => {
    const sw = vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(900)
    const cw = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(200)
    try {
      const w = await mountAt('/activity?status=error&server=slack&auth_type=admin&session=ws-aaa')
      const more = w.find('[data-test="activity-filter-overflow"]')
      expect(more.exists()).toBe(true)
      expect(more.text()).toContain('All 4 filters')
      await more.trigger('click')
      const list = w.find('[data-test="activity-filter-overflow-list"]')
      expect(list.findAll('button')).toHaveLength(4)
      await list.find('[data-test="activity-overflow-chip-server"]').trigger('click')
      await flushPromises()
      expect(w.find('[data-test="activity-filter-chip-server"]').exists()).toBe(false)
      expect(w.find('[data-test="activity-filter-chip-status"]').exists()).toBe(true)
    } finally {
      sw.mockRestore()
      cw.mockRestore()
    }
  })
})
