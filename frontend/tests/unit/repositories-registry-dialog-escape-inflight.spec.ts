import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// Spec 109 PR-a review round 2 (finding 1): closeAddRegistry()/closeDeleteRegistry()
// each early-return while a submit is in flight, and the round-1 fix wired
// these same functions as useDialogOpen's native-close (Escape) handler. A
// real <dialog> already closes itself in the DOM on Escape regardless of any
// guard in Vue-land, so skipping the state flip there just desyncs Vue's
// `showAddRegistry`/`showDeleteRegistry` from the (already-closed) DOM —
// bricking the dialog until reload, since the button that would reopen it
// sets the ref to a value it already holds and the driving watch never fires.
//
// jsdom implements the `open` attribute but not showModal()/close(), so we
// polyfill just enough of the native behaviour (same approach as
// use-dialog-open-native-close.spec.ts) to exercise the real browser path.
function polyfillNativeDialog(el: HTMLDialogElement) {
  el.showModal = function (this: HTMLDialogElement) {
    this.open = true
  }
  el.close = function (this: HTMLDialogElement) {
    if (!this.open) return
    this.open = false
    this.dispatchEvent(new Event('close'))
  }
}

vi.mock('@/services/api', () => ({
  default: {
    listRegistries: vi.fn(),
    searchRegistryServers: vi.fn(),
    addRegistrySource: vi.fn(),
    editRegistrySource: vi.fn(),
    removeRegistrySource: vi.fn(),
    addServerFromRegistry: vi.fn(),
  },
}))

const globalStubs = {
  CollapsibleHintsPanel: { template: '<div />' },
}

function mountView() {
  return mount(Repositories, {
    global: { plugins: [createPinia()], stubs: globalStubs },
    attachTo: document.body,
  })
}

const officialRegistry = {
  id: 'official',
  name: 'Official MCP Registry',
  description: 'The official registry',
  url: 'https://registry.modelcontextprotocol.io/',
  provenance: 'official',
  trusted: true,
}

const customRegistry = {
  id: 'acme',
  name: 'Acme Registry',
  description: 'A custom source',
  url: 'https://acme.example/registry',
  provenance: 'custom',
  trusted: false,
}

describe('Repositories — Add/Delete registry dialog survives Escape mid-submit (round 2)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    ;(api.listRegistries as any).mockResolvedValue({
      success: true,
      data: { registries: [officialRegistry, customRegistry], total: 2 },
    })
    ;(api.searchRegistryServers as any).mockResolvedValue({
      success: true,
      data: { registry_id: 'official', servers: [], total: 0 },
    })
    ;(api.addRegistrySource as any).mockReset()
    ;(api.removeRegistrySource as any).mockReset()
  })

  it('reopens the Add Registry dialog after Escape closes it while the add is in flight', async () => {
    let resolveAdd!: (v: any) => void
    ;(api.addRegistrySource as any).mockImplementation(
      () => new Promise((resolve) => { resolveAdd = resolve })
    )

    const wrapper = mountView()
    await flushPromises()

    const dialog = wrapper.find('[data-test="registry-add-source-dialog"]')
    polyfillNativeDialog(dialog.element as HTMLDialogElement)

    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await flushPromises()
    expect((dialog.element as HTMLDialogElement).open).toBe(true)

    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://acme.example/registry')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises() // addingRegistry is now true; addRegistrySource is still pending

    // The browser closes the dialog natively (Escape) while the submit is
    // still in flight — this is not driven by any Vue click handler.
    ;(dialog.element as HTMLDialogElement).close()
    await flushPromises()

    // Let the in-flight add finish (failure — the specific outcome doesn't
    // matter, only that it resolves after the native close happened).
    resolveAdd({ success: false, code: 'invalid_registry_url', error: 'bad url' })
    await flushPromises()

    // Reopening from the page's own trigger must work — the dialog must not
    // stay bricked shut.
    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await flushPromises()
    expect((dialog.element as HTMLDialogElement).open).toBe(true)
  })

  it('reopens the Delete Registry confirm dialog after Escape closes it while the delete is in flight', async () => {
    let resolveDelete!: (v: any) => void
    ;(api.removeRegistrySource as any).mockImplementation(
      () => new Promise((resolve) => { resolveDelete = resolve })
    )

    const wrapper = mountView()
    await flushPromises()

    const dialog = wrapper.find('[data-test="registry-delete-dialog"]')
    polyfillNativeDialog(dialog.element as HTMLDialogElement)

    await wrapper.find('[data-test="registry-delete-acme"]').trigger('click')
    await flushPromises()
    expect((dialog.element as HTMLDialogElement).open).toBe(true)

    await wrapper.find('[data-test="registry-delete-confirm"]').trigger('click')
    await flushPromises() // deletingRegistry is now true; removeRegistrySource is still pending

    ;(dialog.element as HTMLDialogElement).close()
    await flushPromises()

    resolveDelete({ success: false, error: 'boom' })
    await flushPromises()

    // The dialog must be reopenable again afterwards (e.g. the user retries).
    await wrapper.find('[data-test="registry-delete-acme"]').trigger('click')
    await flushPromises()
    expect((dialog.element as HTMLDialogElement).open).toBe(true)
  })
})
