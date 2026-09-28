import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109-ux-navigation-consistency FR-043 / T031: the Servers step's import
// footer has exactly one primary action ("Import N servers"), a visible
// per-import "Quarantine imported servers for review" checkbox (default on,
// confirmed on uncheck), and Back — not two competing import buttons. The
// global Docker isolation + quarantine defaults move out of this step into a
// one-line summary linking to Settings; they are no longer editable here.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    markOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    importServersFromPath: vi.fn(),
  },
}))

function onboardingState() {
  return {
    success: true,
    data: {
      has_connected_client: true,
      has_configured_server: false,
      connected_client_count: 1,
      connected_client_ids: ['cursor'],
      configured_server_count: 0,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 1,
      has_usable_server: false,
      usable_servers: [],
    },
  }
}

const CURSOR_PATH = '/Users/test/.cursor/mcp.json'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/settings', name: 'settings', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function openServersTab(importable: string[]) {
  const paths = importable.length > 0
    ? [{ name: 'Cursor', format: 'json', path: CURSOR_PATH, exists: true }]
    : []
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths } })
  ;(api.importServersFromPath as any).mockResolvedValue({
    success: true,
    data: { imported: importable.map(name => ({ name })) },
  })

  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const wrapper = mount(OnboardingWizard, {
    props: { show: false },
    global: {
      plugins: [router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        AddServerModal: {
          name: 'AddServerModal',
          props: ['show'],
          template: '<div class="add-server-modal" :data-show="String(show)" />',
        },
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  await wrapper.find('[data-test="tab-servers"]').trigger('click')
  await flushPromises()
  return { wrapper, router }
}

async function selectFirstServer(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('[data-test="server-checkbox-json-memory"]').setValue(true)
  await flushPromises()
}

describe('OnboardingWizard import footer (Spec 109-ux-navigation-consistency FR-043)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({
      success: true,
      data: { config: { quarantine_enabled: true, docker_isolation: { enabled: true } } },
    })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { docker_available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('has exactly one primary import action labelled with the selected count', async () => {
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    const primary = wrapper.find('[data-test="bulk-import-primary"]')
    expect(primary.exists()).toBe(true)
    expect(primary.classes()).toContain('btn-primary')
    expect(primary.text()).toContain('Import 1 server')

    // No second competing import action anywhere in the footer.
    expect(wrapper.find('[data-test="bulk-import-active"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="bulk-import-quarantine"]').exists()).toBe(false)
  })

  it('pluralizes the primary label for more than one selected server', async () => {
    const { wrapper } = await openServersTab(['memory', 'everything'])
    await wrapper.find('[data-test="server-checkbox-json-memory"]').setValue(true)
    await wrapper.find('[data-test="server-checkbox-json-everything"]').setValue(true)
    await flushPromises()

    expect(wrapper.find('[data-test="bulk-import-primary"]').text()).toContain('Import 2 servers')
  })

  it('disables the primary action until at least one server is selected', async () => {
    const { wrapper } = await openServersTab(['memory'])

    const primary = wrapper.find('[data-test="bulk-import-primary"]')
    expect(primary.attributes('disabled')).toBeDefined()

    await selectFirstServer(wrapper)
    expect(wrapper.find('[data-test="bulk-import-primary"]').attributes('disabled')).toBeUndefined()
  })

  it('shows a visible, checked-by-default quarantine checkbox', async () => {
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    const checkbox = wrapper.find('[data-test="footer-quarantine-checkbox"]')
    expect(checkbox.exists()).toBe(true)
    expect((checkbox.element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.text()).toContain('Quarantine imported servers for review')
  })

  it('asks for confirmation before unchecking the quarantine checkbox, and reverts on cancel', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    const checkbox = wrapper.find('[data-test="footer-quarantine-checkbox"]')
    await checkbox.setValue(false)

    expect(confirmSpy).toHaveBeenCalled()
    // Cancelled: the checkbox reverts to checked.
    expect((wrapper.find('[data-test="footer-quarantine-checkbox"]').element as HTMLInputElement).checked).toBe(true)
  })

  // Review round 3 finding: the test above only checks the DOM `checked`
  // state after cancel — it never imports afterward. onToggleQuarantineOnImport
  // reverts `target.checked` on cancel but returns before touching the
  // `quarantineOnImport` ref; a regression that set the ref BEFORE the
  // confirm() call (and only reverted the DOM on cancel) would leave that
  // test green while still sending skip_quarantine: true.
  it('never sends skip_quarantine after the uncheck is cancelled', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    await wrapper.find('[data-test="footer-quarantine-checkbox"]').setValue(false)

    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    expect(api.importServersFromPath).toHaveBeenCalledWith(
      expect.objectContaining({ skip_quarantine: false })
    )
  })

  it('unchecks the quarantine checkbox once the confirmation is accepted', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    await wrapper.find('[data-test="footer-quarantine-checkbox"]').setValue(false)

    expect((wrapper.find('[data-test="footer-quarantine-checkbox"]').element as HTMLInputElement).checked).toBe(false)
  })

  it('checking the box back on never asks for confirmation', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    await wrapper.find('[data-test="footer-quarantine-checkbox"]').setValue(false)
    confirmSpy.mockClear()
    await wrapper.find('[data-test="footer-quarantine-checkbox"]').setValue(true)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect((wrapper.find('[data-test="footer-quarantine-checkbox"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('imports with quarantine when the checkbox is left checked', async () => {
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)

    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    expect(api.importServersFromPath).toHaveBeenCalledWith(
      expect.objectContaining({ skip_quarantine: false })
    )
  })

  it('imports without quarantine once the checkbox is unchecked and confirmed', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)
    await wrapper.find('[data-test="footer-quarantine-checkbox"]').setValue(false)

    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    expect(api.importServersFromPath).toHaveBeenCalledWith(
      expect.objectContaining({ skip_quarantine: true })
    )
  })

  // Review round 3 finding: onOpened() (Spec 078 US1-3) resets previews,
  // backups and undo state on every reopen but left quarantineOnImport,
  // selection and selectionImportMessage untouched. The wizard is mounted
  // once by Dashboard.vue and only toggled via `show` (never unmounted), so
  // an unchecked-and-confirmed "skip quarantine" choice from one session used
  // to carry over — unconfirmed — into the next reopen of the same tab.
  it('resets the quarantine checkbox and selection on reopen, without asking again', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { wrapper } = await openServersTab(['memory'])
    await selectFirstServer(wrapper)
    await wrapper.find('[data-test="footer-quarantine-checkbox"]').setValue(false)
    expect((wrapper.find('[data-test="footer-quarantine-checkbox"]').element as HTMLInputElement).checked).toBe(false)

    // Close the wizard, then reopen it — the component stays mounted the
    // whole time, exactly like Dashboard.vue's `:show="wizardOpen"` binding.
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    const checkbox = wrapper.find('[data-test="footer-quarantine-checkbox"]')
    expect(checkbox.exists()).toBe(true)
    expect((checkbox.element as HTMLInputElement).checked).toBe(true)

    // The reset must not itself prompt — only an explicit uncheck should.
    confirmSpy.mockClear()
    expect(confirmSpy).not.toHaveBeenCalled()

    // Selection is cleared too: the primary import action is disabled again.
    expect(wrapper.find('[data-test="bulk-import-primary"]').attributes('disabled')).toBeDefined()

    // And importing now sends skip_quarantine: false — the stale unchecked
    // choice from before the reopen must never reach the API silently.
    await selectFirstServer(wrapper)
    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()
    expect(api.importServersFromPath).toHaveBeenCalledWith(
      expect.objectContaining({ skip_quarantine: false })
    )
  })

  it('still offers Back', async () => {
    const { wrapper } = await openServersTab(['memory'])
    expect(wrapper.find('[data-test="wizard-back"]').exists()).toBe(true)
  })

  it('replaces the global Docker isolation and quarantine controls with a one-line Settings summary', async () => {
    const { wrapper } = await openServersTab(['memory'])

    expect(wrapper.find('[data-test="toggle-docker-isolation"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="toggle-quarantine"]').exists()).toBe(false)

    const summary = wrapper.find('[data-test="security-defaults-summary"]')
    expect(summary.exists()).toBe(true)
    expect(summary.text()).toContain('Docker isolation')
    expect(summary.text()).toContain('quarantine')

    const link = summary.find('a')
    expect(link.exists()).toBe(true)
    expect(link.text().toLowerCase()).toContain('settings')
  })

  it('shows the Settings summary even when there is nothing to import', async () => {
    const { wrapper } = await openServersTab([])

    const summary = wrapper.find('[data-test="security-defaults-summary"]')
    expect(summary.exists()).toBe(true)
    expect(wrapper.find('[data-test="toggle-docker-isolation"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="toggle-quarantine"]').exists()).toBe(false)
  })

  it('the Settings summary link closes the wizard before navigating away (review round 5)', async () => {
    // Same unmount-before-clear race as goToRegistry(): dismiss() must run
    // before the route changes, or the wizard springs back open on return.
    const { wrapper, router } = await openServersTab(['memory'])

    const link = wrapper.find('[data-test="security-defaults-summary"] a')
    expect(link.exists()).toBe(true)

    await link.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('close')).toBeTruthy()
    expect(router.currentRoute.value.path).toBe('/settings')
  })
})
