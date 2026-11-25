import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerRow from '@/components/ServerRow.vue'
import type { Server } from '@/types'

// Spec 134 PR 2 (FR-020/021): the row. Inventory rows P2-30, P2-31, P2-44,
// P2-45, P2-47, P2-54.

vi.mock('@/composables/useSecurityScannerStatus', () => ({ useSecurityScannerStatus: () => ({ hasEnabledScanners: () => true }) }))
const Link = { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>' }

function srv(over: Record<string, unknown> = {}): Server {
  return { name: 'alpha', protocol: 'stdio', enabled: true, quarantined: false, connected: true, connecting: false, tool_count: 7, ...over } as Server
}
const mountRow = (server: Server) => mount(ServerRow, { props: { server }, global: { plugins: [createPinia()], stubs: { RouterLink: Link, 'router-link': Link } } })
beforeEach(() => setActivePinia(createPinia()))

describe('ServerRow', () => {
  it('the name opens server detail, with the transport under it, and there is no Details link or expansion', () => {
    const w = mountRow(srv({ name: 'io.x/y', title: 'Nice', protocol: 'http' }))
    const link = w.get('[data-test="server-detail-link"]')
    expect(link.text()).toBe('Nice')
    expect(link.attributes('href')).toBe('/servers/io.x%2Fy')
    expect(w.get('[data-test="server-card-protocol"]').text()).toBe('http')
    expect(w.findAll('a').filter(a => a.text() === 'Details')).toHaveLength(0)
    expect(w.find('details').exists()).toBe(false)
  })

  it('prints the status label through the pill, the reason as one line, the tool count', () => {
    const w = mountRow(srv({ health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Host not found', status: 'error', usable: false, actions: ['edit_url'], action: 'edit_url' } }))
    expect(w.get('[data-test="server-status-chip"]').text()).toBe('Error')
    expect(w.get('[data-test="status-pill"], [data-test="server-status-chip"]').attributes('data-tone')).toBe('error')
    expect(w.get('[data-test="server-row-reason"]').text()).toBe('Host not found')
    expect(w.get('[data-test="server-card-tool-count"]').text()).toContain('7')
  })

  it('a long reason stays one line: it is cut by CSS, never wrapped, and the title carries it', () => {
    const long = 'z'.repeat(300)
    const w = mountRow(srv({ last_error: long }))
    const reason = w.get('[data-test="server-row-reason"]')
    expect(reason.text()).toBe(long)
    expect(reason.attributes('title')).toContain(long)
    expect(reason.classes()).not.toContain('whitespace-normal')
  })

  it('no action cell content when health.actions is empty', () => {
    const w = mountRow(srv({ health: { level: 'healthy', admin_state: 'enabled', summary: 'ok', status: 'ready', usable: true, actions: [] } }))
    expect(w.find('[data-test="server-card-primary-action"]').exists()).toBe(false)
  })

  it('the menu carries every secondary action', () => {
    const w = mountRow(srv({ quarantined: true, authenticated: true, protocol: 'http', health: { level: 'healthy', admin_state: 'quarantined', summary: 's', status: 'needs_review', usable: false, actions: ['approve'] } }))
    for (const id of ['review', 'toggle', 'scan', 'restart', 'logs', 'edit', 'trust', 'delete']) {
      expect(w.find(`[data-test="server-card-menu-${id}"]`).exists(), id).toBe(true)
    }
  })
})
