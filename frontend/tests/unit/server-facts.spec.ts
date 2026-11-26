import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ServerFacts from '@/components/ServerFacts.vue'
import type { Server } from '@/types'

// Spec 134 PR 2 (FR-020, inventory P2-30, P2-46, P2-49, P2-60): the facts the
// row dropped are on the first screen of server detail.

function srv(over: Record<string, unknown> = {}): Server {
  return { name: 'io.github/x', protocol: 'stdio', enabled: true, quarantined: false, connected: true, connecting: false, tool_count: 1, ...over } as Server
}

describe('ServerFacts', () => {
  it('shows the id only when a title hides it', () => {
    expect(mount(ServerFacts, { props: { server: srv() } }).find('[data-test="server-facts-id"]').exists()).toBe(false)
    const w = mount(ServerFacts, { props: { server: srv({ title: 'Nice name' }) } })
    expect(w.get('[data-test="server-facts-id"]').text()).toContain('io.github/x')
  })

  it('shows trust mode with its label as text', () => {
    const w = mount(ServerFacts, { props: { server: srv({ trust_mode: 'manual' }) } })
    expect(w.get('[data-test="server-facts-trust-label"]').text()).toBe('Manual')
  })

  it('shows pending and changed counts (not for a quarantined server, whose note covers it)', () => {
    const w = mount(ServerFacts, { props: { server: srv({ quarantine: { pending_count: 1, changed_count: 2, blocked_count: 0 } }) } })
    expect(w.get('[data-test="server-facts-held"]').text()).toBe('1 pending, 2 changed')
    const q = mount(ServerFacts, { props: { server: srv({ quarantined: true, quarantine: { pending_count: 1, changed_count: 0, blocked_count: 0 } }) } })
    expect(q.find('[data-test="server-facts-held"]').exists()).toBe(false)
  })

  it('"Full diagnostic" opens the untruncated detail, diagnostic and raw error', async () => {
    const long = 'x'.repeat(400)
    const w = mount(ServerFacts, {
      props: { server: srv({ last_error: `a: b: ${long}`, health: { level: 'unhealthy', admin_state: 'enabled', summary: 's', detail: `detail ${long}`, status: 'error', usable: false, actions: [] }, diagnostic: { code: 'MCPX_X', severity: 'error', user_message: 'It broke', cause: 'because' } }) },
      attachTo: document.body,
    })
    const details = w.get('[data-test="server-facts-diagnostic"]')
    expect(details.find('summary').text()).toBe('Full diagnostic')
    expect(w.text()).not.toContain('It broke')
    ;(details.element as HTMLDetailsElement).open = true
    await details.trigger('toggle')
    expect(w.get('[data-test="server-facts-raw-error"]').text()).toBe(`a: b: ${long}`)
    expect(w.get('[data-test="server-facts-health-detail"]').text()).toBe(`detail ${long}`)
    expect(w.get('[data-test="server-facts-diagnostic-message"]').text()).toContain('It broke (MCPX_X)')
    w.unmount()
  })

  it('has no diagnostic control when there is nothing to show', () => {
    expect(mount(ServerFacts, { props: { server: srv() } }).find('[data-test="server-facts-diagnostic"]').exists()).toBe(false)
  })
})
