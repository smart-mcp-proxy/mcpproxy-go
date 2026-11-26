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
  it('always shows the id, and the title beside it only when it differs (PR 3)', () => {
    const plain = mount(ServerFacts, { props: { server: srv() } })
    expect(plain.get('[data-test="fact-id"]').text()).toBe('io.github/x')
    expect(plain.find('[data-test="fact-title"]').exists()).toBe(false)
    const w = mount(ServerFacts, { props: { server: srv({ title: 'Nice name' }) } })
    expect(w.get('[data-test="fact-id"]').text()).toContain('io.github/x')
    expect(w.get('[data-test="fact-title"]').text()).toBe('Nice name')
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

  it('"Full diagnostic" opens a dialog with the untruncated detail, diagnostic and raw error', async () => {
    const long = 'x'.repeat(400)
    const w = mount(ServerFacts, {
      props: { server: srv({ last_error: `a: b: ${long}`, health: { level: 'unhealthy', admin_state: 'enabled', summary: 's', detail: `detail ${long}`, status: 'error', usable: false, actions: [] }, diagnostic: { code: 'MCPX_X', severity: 'error', user_message: 'It broke', cause: 'because' } }) },
      attachTo: document.body,
    })
    expect(w.get('[data-test="open-diagnostic"]').text()).toBe('Full diagnostic')
    expect(w.text()).not.toContain('It broke')
    await w.get('[data-test="open-diagnostic"]').trigger('click')
    const text = w.findAll('[data-test="diagnostic-value"]').map(x => x.text()).join('\n')
    expect(text).toContain(`a: b: ${long}`)
    expect(text).toContain(`detail ${long}`)
    expect(text).toContain('It broke')
    expect(text).toContain('MCPX_X')
    w.unmount()
  })

  it('shows no diagnostic text until the dialog is opened', () => {
    const w = mount(ServerFacts, { props: { server: srv() } })
    expect(w.find('[data-test="diagnostic-value"]').exists()).toBe(false)
  })
})
