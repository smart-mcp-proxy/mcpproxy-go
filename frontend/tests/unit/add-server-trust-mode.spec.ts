import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AddServerModal from '@/components/AddServerModal.vue'
import api from '@/services/api'

// Spec 088 US1 / FR-006 (research D6): the add-server form chooses the trust
// mode at add time — "manual" preselected as the safe default — and the request
// body carries `trust_mode` while OMITTING `quarantined` entirely. The backend
// derives admission from the trust mode (`QuarantineDefaultForServer`) and
// treats an explicit `quarantined` as an override applied AFTER that
// derivation, so shipping both controls lets the operator contradict the mode
// semantics. The independent quarantine checkbox is therefore removed from the
// manual-entry tab; the Import tab (and registry/onboarding paths) are
// untouched and keep their existing safe defaults.

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
}

const OPTION = (mode: string) => `[data-test="trust-mode-option-${mode}"]`
const RADIO = (mode: string) => `${OPTION(mode)} input[type="radio"]`

function mountModal() {
  return mount(AddServerModal, { props: { show: true } })
}

type Wrapper = ReturnType<typeof mountModal>

/** The payload the modal hands to the servers store → `upstream_servers` add. */
function submittedPayload(): Record<string, unknown> {
  expect(mockedApi.callTool).toHaveBeenCalledTimes(1)
  const [tool, payload] = mockedApi.callTool.mock.calls[0]
  expect(tool).toBe('upstream_servers')
  return payload as Record<string, unknown>
}

async function fillMinimalStdio(wrapper: Wrapper, name = 'demo-server') {
  await wrapper.find('input[type="text"]').setValue(name)
  await wrapper.find('select').setValue('npx')
}

async function submit(wrapper: Wrapper) {
  await wrapper.find('[data-test="add-server-modal-box"] form').trigger('submit')
  await flushPromises()
}

async function chooseMode(wrapper: Wrapper, mode: 'auto' | 'scan' | 'manual') {
  await wrapper.find(RADIO(mode)).setValue(true)
  const confirm = wrapper.find('[data-test="trust-mode-auto-confirm-accept"]')
  if (confirm.exists()) await confirm.trigger('click')
}

function isChecked(wrapper: Wrapper, mode: string) {
  return (wrapper.find(RADIO(mode)).element as HTMLInputElement).checked
}

describe('AddServerModal — trust mode replaces the quarantine checkbox (FR-006)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.callTool.mockResolvedValue({ success: true })
    mockedApi.getServers.mockResolvedValue({ success: true, data: { servers: [] } })
    mockedApi.getCanonicalConfigPaths.mockResolvedValue({ success: true, data: { paths: [] } })
  })

  it('renders the trust-mode selector on the manual-entry tab', () => {
    const wrapper = mountModal()
    expect(wrapper.find('[data-test="addserver-trust-mode"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-selector"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-test^="trust-mode-option-"]')).toHaveLength(3)
  })

  it('preselects manual — the safe default — before the operator touches anything', () => {
    const wrapper = mountModal()
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(isChecked(wrapper, 'scan')).toBe(false)
    expect(isChecked(wrapper, 'auto')).toBe(false)
  })

  it('no independent quarantine checkbox remains anywhere on the manual-entry tab', () => {
    const wrapper = mountModal()
    const options = wrapper.find('[data-test="addserver-options"]')
    expect(options.exists()).toBe(true)
    // The old control was a `toggle-warning` checkbox labelled "Quarantined".
    expect(options.findAll('input.toggle-warning')).toHaveLength(0)
    const labels = wrapper.findAll('label').map((l) => l.text())
    expect(labels.some((t) => /^\s*Quarantined/.test(t))).toBe(false)
    // Toggles the form still owns are untouched.
    expect(options.text()).toContain('Enabled')
    expect(options.text()).toContain('Docker Isolation')
  })

  it('submits trust_mode and OMITS quarantined entirely (backend derives admission)', async () => {
    const wrapper = mountModal()
    await fillMinimalStdio(wrapper)
    await submit(wrapper)

    const payload = submittedPayload()
    expect(payload.trust_mode).toBe('manual')
    expect(payload).not.toHaveProperty('quarantined')
    expect(Object.keys(payload)).not.toContain('quarantined')
  })

  it('submits the operator-chosen scan mode', async () => {
    const wrapper = mountModal()
    await fillMinimalStdio(wrapper)
    await chooseMode(wrapper, 'scan')
    await submit(wrapper)

    const payload = submittedPayload()
    expect(payload.trust_mode).toBe('scan')
    expect(payload).not.toHaveProperty('quarantined')
  })

  it('submits auto only after its warning is acknowledged (FR-003 gate survives)', async () => {
    const wrapper = mountModal()
    await fillMinimalStdio(wrapper)

    await wrapper.find(RADIO('auto')).setValue(true)
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(true)

    // Cancelling leaves the safe default in place.
    await wrapper.find('[data-test="trust-mode-auto-confirm-cancel"]').trigger('click')
    await submit(wrapper)
    expect(submittedPayload().trust_mode).toBe('manual')

    vi.clearAllMocks()
    mockedApi.callTool.mockResolvedValue({ success: true })
    mockedApi.getServers.mockResolvedValue({ success: true, data: { servers: [] } })

    const wrapper2 = mountModal()
    await fillMinimalStdio(wrapper2)
    await chooseMode(wrapper2, 'auto')
    await submit(wrapper2)
    const payload = submittedPayload()
    expect(payload.trust_mode).toBe('auto')
    expect(payload).not.toHaveProperty('quarantined')
  })

  it('keeps the rest of the manual submit payload unchanged (stdio)', async () => {
    const wrapper = mountModal()
    await wrapper.find('input[type="text"]').setValue('fs-server')
    await wrapper.find('select').setValue('npx')
    const textareas = wrapper.findAll('textarea')
    await textareas[0].setValue('@modelcontextprotocol/server-filesystem\n')
    await textareas[1].setValue('API_KEY=secret')
    await submit(wrapper)

    const payload = submittedPayload()
    expect(payload.operation).toBe('add')
    expect(payload.name).toBe('fs-server')
    expect(payload.protocol).toBe('stdio')
    expect(payload.enabled).toBe(true)
    expect(payload.command).toBe('npx')
    expect(payload.args_json).toBe(JSON.stringify(['@modelcontextprotocol/server-filesystem']))
    expect(payload.env_json).toBe(JSON.stringify({ API_KEY: 'secret' }))
  })

  it('keeps the rest of the manual submit payload unchanged (http)', async () => {
    const wrapper = mountModal()
    await wrapper.findAll('input[type="radio"][name="serverType"]')[1].setValue(true)
    await wrapper.find('input[type="text"]').setValue('remote')
    await wrapper.find('input[type="url"]').setValue('https://api.example.com/mcp')
    await chooseMode(wrapper, 'scan')
    await submit(wrapper)

    const payload = submittedPayload()
    expect(payload.protocol).toBe('http')
    expect(payload.url).toBe('https://api.example.com/mcp')
    expect(payload.trust_mode).toBe('scan')
    expect(payload).not.toHaveProperty('quarantined')
  })

  it('resets the trust mode back to manual when the form is closed', async () => {
    const wrapper = mountModal()
    await chooseMode(wrapper, 'scan')
    expect(isChecked(wrapper, 'scan')).toBe(true)

    await wrapper.find('[data-test="add-server-modal-box"] .modal-action .btn-ghost').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('close')).toBeTruthy()
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(isChecked(wrapper, 'scan')).toBe(false)
  })

  it('leaves the Import tab free of the trust-mode control (other add paths unchanged)', async () => {
    const wrapper = mountModal()
    await wrapper.findAll('.tabs .tab')[1].trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="addserver-options"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="addserver-trust-mode"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="trust-mode-selector"]').exists()).toBe(false)
    // The import surface itself still renders.
    expect(wrapper.text()).toContain('Upload File')
  })
})
