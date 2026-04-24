import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ErrorPanel from '@/components/diagnostics/ErrorPanel.vue'
import api from '@/services/api'
import type { Diagnostic } from '@/types'

// Mock the API module so tests don't hit the network.
vi.mock('@/services/api', () => ({
  default: {
    invokeDiagnosticFix: vi.fn(),
  },
}))

describe('ErrorPanel (spec 044)', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    ;(api.invokeDiagnosticFix as any).mockReset()
  })

  function makeDiag(overrides: Partial<Diagnostic> = {}): Diagnostic {
    return {
      code: 'MCPX_STDIO_SPAWN_ENOENT',
      severity: 'error',
      cause: 'exec: "/nonexistent": file not found',
      user_message: 'The configured command was not found on PATH.',
      fix_steps: [
        {
          type: 'command',
          label: 'Check PATH',
          command: 'which npx',
        },
        {
          type: 'button',
          label: 'Show last logs',
          fixer_key: 'stdio_show_last_logs',
        },
      ],
      docs_url: 'docs/errors/MCPX_STDIO_SPAWN_ENOENT.md',
      ...overrides,
    }
  }

  it('renders the stable code, severity badge, and user_message', () => {
    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: makeDiag(), serverName: 'test-broken' },
      global: { plugins: [pinia] },
    })

    expect(wrapper.find('[data-testid="error-panel-code"]').text())
      .toBe('MCPX_STDIO_SPAWN_ENOENT')
    expect(wrapper.find('[data-testid="error-panel-severity"]').text()).toBe('error')
    expect(wrapper.find('[data-testid="error-panel-message"]').text())
      .toContain('configured command was not found')
  })

  it('non-destructive fix: Execute button runs fixer with mode=execute (no Preview button)', async () => {
    ;(api.invokeDiagnosticFix as any).mockResolvedValue({
      success: true,
      data: { outcome: 'success', duration_ms: 12, mode: 'execute', preview: 'last 20 log lines' },
    })

    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: makeDiag(), serverName: 'test-broken' },
      global: { plugins: [pinia] },
    })

    // The button step index is 1 (command step was 0). Non-destructive -> no Preview button.
    expect(wrapper.find('[data-testid="error-panel-fix-button-1"]').exists()).toBe(false)

    const execBtn = wrapper.find('[data-testid="error-panel-execute-button-1"]')
    expect(execBtn.exists()).toBe(true)
    expect(execBtn.attributes('disabled')).toBeUndefined()
    await execBtn.trigger('click')
    await flushPromises()

    expect(api.invokeDiagnosticFix).toHaveBeenCalledTimes(1)
    const call = (api.invokeDiagnosticFix as any).mock.calls[0][0]
    expect(call).toMatchObject({
      server: 'test-broken',
      code: 'MCPX_STDIO_SPAWN_ENOENT',
      fixer_key: 'stdio_show_last_logs',
      mode: 'execute',
    })
  })

  it('destructive fix: Preview sends dry_run, Execute is gated by window.confirm()', async () => {
    ;(api.invokeDiagnosticFix as any).mockResolvedValue({
      success: true,
      data: { outcome: 'success', duration_ms: 12, mode: 'dry_run', preview: 'would remove oauth tokens' },
    })

    const destructiveDiag = makeDiag({
      fix_steps: [
        {
          type: 'button',
          label: 'Clear OAuth tokens',
          fixer_key: 'oauth_clear_tokens',
          destructive: true,
        },
      ],
    })

    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: destructiveDiag, serverName: 'test-broken' },
      global: { plugins: [pinia] },
    })

    // Both Preview and Execute buttons exist for destructive.
    const previewBtn = wrapper.find('[data-testid="error-panel-fix-button-0"]')
    const execBtn = wrapper.find('[data-testid="error-panel-execute-button-0"]')
    expect(previewBtn.exists()).toBe(true)
    expect(execBtn.exists()).toBe(true)

    // Preview sends dry_run.
    await previewBtn.trigger('click')
    await flushPromises()
    expect((api.invokeDiagnosticFix as any).mock.calls[0][0]).toMatchObject({ mode: 'dry_run' })

    // Execute is gated by window.confirm — reject path.
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await execBtn.trigger('click')
    await flushPromises()
    expect(confirmSpy).toHaveBeenCalledTimes(1)
    // No second call to the API because the user cancelled.
    expect(api.invokeDiagnosticFix).toHaveBeenCalledTimes(1)

    // Execute is gated by window.confirm — accept path.
    confirmSpy.mockReturnValue(true)
    ;(api.invokeDiagnosticFix as any).mockResolvedValue({
      success: true,
      data: { outcome: 'success', duration_ms: 12, mode: 'execute' },
    })
    await execBtn.trigger('click')
    await flushPromises()
    expect(api.invokeDiagnosticFix).toHaveBeenCalledTimes(2)
    expect((api.invokeDiagnosticFix as any).mock.calls[1][0]).toMatchObject({ mode: 'execute' })

    confirmSpy.mockRestore()
  })

  it('maps severity to alert colour classes', async () => {
    const errorWrap = mount(ErrorPanel, {
      props: { diagnostic: makeDiag({ severity: 'error' }), serverName: 's' },
      global: { plugins: [pinia] },
    })
    expect(errorWrap.find('.alert').classes()).toContain('alert-error')

    const warnWrap = mount(ErrorPanel, {
      props: { diagnostic: makeDiag({ severity: 'warn' }), serverName: 's' },
      global: { plugins: [pinia] },
    })
    expect(warnWrap.find('.alert').classes()).toContain('alert-warning')
  })

  it('renders nothing when diagnostic is null', () => {
    const wrapper = mount(ErrorPanel, {
      props: { diagnostic: null, serverName: 's' },
      global: { plugins: [pinia] },
    })
    expect(wrapper.find('.alert').exists()).toBe(false)
  })
})
