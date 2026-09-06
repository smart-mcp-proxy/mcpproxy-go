import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AddServerModal from '@/components/AddServerModal.vue'
import api from '@/services/api'

// UX audit F07. Three defects on the Add New Server modal, all cosmetic-or-flow
// only — no payload, predicate or security surface changes:
//
//  1. The Docker Isolation toggle was rendered DISABLED for an http upstream.
//     Isolation is structurally impossible there (internal/config/
//     isolation_resolve.go returns IsolationSourceNotStdio when Command == ""),
//     so the control is noise, not a choice. Hide it instead of disabling it.
//  2. "Idle on Inactivity" was a permanently-disabled "Coming soon" placeholder
//     with no backend field and no read in handleSubmit — pure attention cost.
//  3. On save the user was left where they were, while the real activation work
//     (connect, scan, review, approve) waited on the server-detail page. The
//     modal now names the server it added so the CONSUMER can hand off; the
//     bulk/import path stays payload-free so it keeps its list-refresh flow.
//
// The name payload is order-sensitive: handleClose() blanks formData.name, so
// `emit('added', formData.name)` must stay above it.

vi.mock('@/services/api', () => ({
  default: {
    callTool: vi.fn(),
    getServers: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    importServersFromFile: vi.fn(),
    importServersFromJSON: vi.fn(),
    importServersFromPath: vi.fn(),
  },
}))

const mockedApi = api as unknown as {
  callTool: ReturnType<typeof vi.fn>
  getServers: ReturnType<typeof vi.fn>
  getCanonicalConfigPaths: ReturnType<typeof vi.fn>
  importServersFromJSON: ReturnType<typeof vi.fn>
}

function mountModal() {
  return mount(AddServerModal, { props: { show: true } })
}

type Wrapper = ReturnType<typeof mountModal>

function optionsText(wrapper: Wrapper): string {
  const options = wrapper.find('[data-test="addserver-options"]')
  expect(options.exists()).toBe(true)
  return options.text()
}

async function selectHttp(wrapper: Wrapper) {
  await wrapper.findAll('input[type="radio"][name="serverType"]')[1].setValue(true)
}

async function submitManual(wrapper: Wrapper) {
  await wrapper.find('[data-test="add-server-modal-box"] form').trigger('submit')
  await flushPromises()
}

const PREVIEW = {
  format: 'claude-desktop',
  format_name: 'Claude Desktop',
  summary: { total: 1, imported: 1, skipped: 0, failed: 0 },
  imported: [
    {
      name: 'imported-one',
      protocol: 'stdio',
      command: 'uvx',
      source_format: 'claude-desktop',
      original_name: 'imported-one',
    },
  ],
  skipped: [],
  failed: [],
  warnings: [],
}

describe('AddServerModal — protocol-conditional isolation + post-add hand-off (F07)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.callTool.mockResolvedValue({ success: true })
    mockedApi.getServers.mockResolvedValue({ success: true, data: { servers: [] } })
    mockedApi.getCanonicalConfigPaths.mockResolvedValue({ success: true, data: { paths: [] } })
    mockedApi.importServersFromJSON.mockResolvedValue({ success: true, data: PREVIEW })
  })

  it('offers the Docker Isolation toggle for a stdio server', () => {
    const wrapper = mountModal()
    expect(optionsText(wrapper)).toContain('Docker Isolation')
  })

  it('hides the Docker Isolation toggle for an http server rather than disabling it', async () => {
    const wrapper = mountModal()
    await selectHttp(wrapper)

    const text = optionsText(wrapper)
    expect(text).not.toContain('Docker Isolation')
    // Nothing disabled-but-visible is left behind in the Options block either.
    const disabled = wrapper
      .find('[data-test="addserver-options"]')
      .findAll('input[disabled], input:disabled')
    expect(disabled).toHaveLength(0)
    // The toggles that DO apply to an http upstream are untouched.
    expect(text).toContain('Enabled')
  })

  it('drops the dead "Idle on Inactivity" placeholder from the Options block', () => {
    const wrapper = mountModal()
    const text = optionsText(wrapper)
    expect(text).not.toContain('Idle on Inactivity')
    expect(text).not.toContain('Coming soon')
  })

  it('names the server it added so the consumer can hand off (stdio)', async () => {
    const wrapper = mountModal()
    await wrapper.find('input[type="text"]').setValue('fs-server')
    await wrapper.find('select').setValue('npx')
    await submitManual(wrapper)

    expect(mockedApi.callTool).toHaveBeenCalledTimes(1)
    const added = wrapper.emitted('added')
    expect(added).toBeTruthy()
    expect(added).toHaveLength(1)
    expect(added![0]).toEqual(['fs-server'])
  })

  it('names the server it added so the consumer can hand off (http)', async () => {
    const wrapper = mountModal()
    await selectHttp(wrapper)
    await wrapper.find('input[type="text"]').setValue('remote-mcp')
    await wrapper.find('input[type="url"]').setValue('https://api.example.com/mcp')
    await submitManual(wrapper)

    const added = wrapper.emitted('added')
    expect(added![0]).toEqual(['remote-mcp'])
  })

  it('emits the name BEFORE the form is reset (handleClose blanks it)', async () => {
    const wrapper = mountModal()
    await wrapper.find('input[type="text"]').setValue('order-sensitive')
    await wrapper.find('select').setValue('npx')
    await submitManual(wrapper)

    // A regression that moved the emit below handleClose() would silently
    // publish '' here and push consumers at "/servers/".
    expect(wrapper.emitted('added')![0][0]).toBe('order-sensitive')
    expect(wrapper.emitted('added')![0][0]).not.toBe('')
  })

  it('leaves the bulk/import path payload-free so consumers keep the list view', async () => {
    const wrapper = mountModal()
    await wrapper.findAll('.tabs .tab')[1].trigger('click')
    await flushPromises()

    // Paste mode, then set the format — its watcher previews immediately,
    // bypassing the 500 ms debounce on the textarea.
    const pasteBtn = wrapper.findAll('button').find((b) => b.text() === 'Paste Content')
    expect(pasteBtn).toBeTruthy()
    await pasteBtn!.trigger('click')
    await wrapper.find('textarea').setValue('{"mcpServers":{"imported-one":{"command":"uvx"}}}')
    await wrapper.find('select').setValue('claude-desktop')
    await flushPromises()

    const importBtn = wrapper
      .findAll('[data-test="add-server-modal-box"] button')
      .find((b) => b.text().includes('Import 1 Server'))
    expect(importBtn).toBeTruthy()
    await importBtn!.trigger('click')
    await flushPromises()

    const added = wrapper.emitted('added')
    expect(added).toBeTruthy()
    expect(added).toHaveLength(1)
    // No name → the consumer must NOT navigate; a bulk import has no single
    // destination and the user stays on the list they were reading.
    expect(added![0]).toEqual([])
  })
})
