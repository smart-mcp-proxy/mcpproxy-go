import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import ErrorPanel from '@/components/diagnostics/ErrorPanel.vue'
import ToastContainer from '@/components/ToastContainer.vue'
import { useSystemStore } from '@/stores/system'

// The diagnostics self-heal button `stdio_show_last_logs` used to return a
// one-sentence placeholder ("log tail unavailable in this build"). It now
// returns a real ~50-line log tail, and that payload is delivered through the
// toast system — which was built for one-line status messages.
//
// Two things had to hold for the button to be worth clicking, and neither did:
//   1. The toast must not collapse the tail's newlines into one run-on
//      paragraph (default HTML whitespace handling does exactly that).
//   2. The toast must not auto-dismiss after the default 5s. Nobody reads 50
//      lines of stderr in five seconds.
//
// These are the assertions that fail if either regresses.

// vi.hoisted: vi.mock's factory is lifted above module scope, so the fixture it
// closes over has to be lifted with it.
const { LOG_TAIL } = vi.hoisted(() => ({
  LOG_TAIL: [
    'Last 3 log line(s) for "flaky-stdio":',
    'connect failed url=https://host/mcp?token=****',
    'Error: MISTRAL_API_KEY environment variable is not set',
    'child said: ghp_****',
  ].join('\n'),
}))

vi.mock('@/services/api', () => ({
  default: {
    invokeDiagnosticFix: vi.fn().mockResolvedValue({
      success: true,
      data: { outcome: 'success', duration_ms: 12, mode: 'execute', preview: LOG_TAIL },
    }),
  },
}))

const DIAGNOSTIC = {
  code: 'MCPX_STDIO_EXIT_BEFORE_INITIALIZE',
  severity: 'error' as const,
  user_message: 'The server exited before it finished starting up.',
  fix_steps: [
    {
      type: 'button' as const,
      label: 'Show last server log lines',
      fixer_key: 'stdio_show_last_logs',
      // Non-destructive: the UI renders a single Execute button for these.
      destructive: false,
    },
  ],
}

describe('diagnostics log-tail fix delivery', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('gives a multi-line fix preview long enough to actually read', async () => {
    const store = useSystemStore()
    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: DIAGNOSTIC, serverName: 'flaky-stdio' },
    })

    await wrapper.find('[data-testid="error-panel-execute-button-0"]').trigger('click')
    await flushPromises()

    expect(store.toasts).toHaveLength(1)
    const toast = store.toasts[0]
    expect(toast.message).toBe(LOG_TAIL)
    // The whole product of this fixer is text to read. The 5s default would
    // pull it off screen before the operator finished the first line.
    expect(toast.duration).toBeGreaterThan(5000)
  })

  it('leaves a single-line fix message on the default toast timing', async () => {
    const api = (await import('@/services/api')).default as unknown as {
      invokeDiagnosticFix: ReturnType<typeof vi.fn>
    }
    api.invokeDiagnosticFix.mockResolvedValueOnce({
      success: true,
      data: { outcome: 'failed', duration_ms: 3, mode: 'execute', failure_msg: 'server not found' },
    })

    const store = useSystemStore()
    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: DIAGNOSTIC, serverName: 'flaky-stdio' },
    })

    await wrapper.find('[data-testid="error-panel-execute-button-0"]').trigger('click')
    await flushPromises()

    expect(store.toasts).toHaveLength(1)
    // Ordinary one-line outcomes keep the store's 5s default; only readable
    // payloads get the long dwell, so the change cannot leave the corner
    // cluttered with stale notifications.
    expect(store.toasts[0].duration).toBe(5000)
  })

  it('supersedes its own long-lived preview instead of stacking tall toasts', async () => {
    const store = useSystemStore()
    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: DIAGNOSTIC, serverName: 'flaky-stdio' },
    })

    // Three clicks inside the 60s dwell. Without superseding, all three tall
    // previews stay up; the stack is anchored to the bottom of the viewport and
    // grows upward, so the earliest ones leave the screen — close button and all.
    for (let i = 0; i < 3; i++) {
      await wrapper.find('[data-testid="error-panel-execute-button-0"]').trigger('click')
      await flushPromises()
    }

    expect(store.toasts).toHaveLength(1)
    expect(store.toasts[0].message).toBe(LOG_TAIL)
  })

  it('removes its long-lived preview when the panel unmounts', async () => {
    const store = useSystemStore()
    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: DIAGNOSTIC, serverName: 'flaky-stdio' },
    })

    await wrapper.find('[data-testid="error-panel-execute-button-0"]').trigger('click')
    await flushPromises()
    expect(store.toasts).toHaveLength(1)

    // The superseding id lives in the component, the 60s toast in the global
    // store. ServerDetail unmounts this panel on every navigation between
    // servers, so without disposal the id is lost, the toast stays, and the
    // next server's preview stacks on top of it — the accumulation the
    // superseding guard was meant to prevent.
    wrapper.unmount()

    expect(store.toasts).toHaveLength(0)
  })

  it('renders a multi-line toast message with its line breaks preserved', () => {
    const store = useSystemStore()
    store.addToast({ type: 'success', title: 'Executed: Show last server log lines', message: LOG_TAIL })

    const wrapper = mount(ToastContainer)
    const message = wrapper.findAll('div').find((d) => d.text().includes('MISTRAL_API_KEY'))
    expect(message).toBeDefined()

    // Without whitespace-pre-wrap the browser folds every newline into a space
    // and the tail arrives as one paragraph — the defect this pins.
    const classed = wrapper.findAll('div').some((d) => {
      const cls = d.attributes('class') ?? ''
      return cls.includes('whitespace-pre-wrap') && d.text().includes('MISTRAL_API_KEY')
    })
    expect(classed).toBe(true)
  })
})
