import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Issue #2008 — the Configuration tab's Process card lets the operator change
// the command and arguments of a stdio server. Only what changed is sent
// (masked values the user did not touch must never be echoed back), `args`
// replaces the whole vector, and a backend refusal stays beside the fields.

let serverCommand: string
let serverArgs: string[] | undefined
let patchResponse: { success: boolean; data?: unknown; error?: string }

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: 'sqlite',
              protocol: 'stdio',
              enabled: true,
              connected: true,
              quarantined: false,
              tool_count: 1,
              command: serverCommand,
              ...(serverArgs === undefined ? {} : { args: serverArgs }),
            },
            {
              name: 'other',
              protocol: 'stdio',
              enabled: true,
              connected: true,
              quarantined: false,
              tool_count: 1,
              command: 'node',
              args: ['other.js'],
            },
          ],
        })
      ),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => Promise.resolve(patchResponse)),
    },
  }
})

async function mountDetail() {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/sqlite?tab=config')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'sqlite' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return { wrapper, api }
}

type Wrapper = Awaited<ReturnType<typeof mountDetail>>['wrapper']

async function openEditor(wrapper: Wrapper) {
  await wrapper.find('[data-test="server-process-edit"]').trigger('click')
  await flushPromises()
}

async function save(wrapper: Wrapper) {
  await wrapper.find('[data-test="server-process-form"]').trigger('submit')
  await flushPromises()
}

const commandInput = (w: Wrapper) => w.find('[data-test="server-command-input"]')
const argsInput = (w: Wrapper) => w.find('[data-test="server-args-input"]')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverCommand = 'uvx'
  serverArgs = ['mcp-server-sqlite', '--db-path', '/data/old.db']
  patchResponse = { success: true, data: { message: 'ok' } }
})

describe('ServerDetail — edit command and arguments (issue #2008)', () => {
  it('shows the command read-only with an Edit button', async () => {
    const { wrapper } = await mountDetail()
    const card = wrapper.find('[data-test="server-process-card"]')
    expect(card.exists()).toBe(true)
    expect(card.text()).toContain('uvx')
    expect(card.text()).toContain('mcp-server-sqlite --db-path /data/old.db')
    expect(wrapper.find('[data-test="server-process-edit"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(false)
  })

  it('opens an editor filled with the command and one argument per line', async () => {
    const { wrapper } = await mountDetail()
    await openEditor(wrapper)
    expect((commandInput(wrapper).element as HTMLInputElement).value).toBe('uvx')
    expect((argsInput(wrapper).element as HTMLTextAreaElement).value).toBe(
      'mcp-server-sqlite\n--db-path\n/data/old.db'
    )
  })

  it('sends only the command when only the command changed', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('  /usr/local/bin/uvx  ')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledTimes(1)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', { command: '/usr/local/bin/uvx' })
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(false)
  })

  it('sends the whole argument list, and not the command, when only arguments changed', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await argsInput(wrapper).setValue('mcp-server-sqlite\n\n  --db-path  \r\n/data/new file.db\n')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', {
      args: ['mcp-server-sqlite', '--db-path', '/data/new file.db'],
    })
  })

  it('sends an empty list when every argument is removed', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await argsInput(wrapper).setValue('')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', { args: [] })
  })

  it('lets a server without arguments gain some', async () => {
    serverArgs = undefined
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    expect((argsInput(wrapper).element as HTMLTextAreaElement).value).toBe('')
    await argsInput(wrapper).setValue('--verbose')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', { args: ['--verbose'] })
  })

  it('makes no request when nothing changed', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await save(wrapper)
    expect(api.patchServer).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(false)
  })

  it('never echoes untouched masked arguments back when only the command changed', async () => {
    serverArgs = ['--api-key', '••••••••']
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('node')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', { command: 'node' })
  })

  it('refuses an empty command without calling the API', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('   ')
    expect(wrapper.find('[data-test="server-process-save"]').attributes('disabled')).toBeDefined()
    await save(wrapper)
    expect(api.patchServer).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="server-process-error"]').text()).toContain('Enter a command')
  })

  it('keeps the editor open and shows the reason when the backend refuses', async () => {
    patchResponse = { success: false, error: 'args[1] is a redaction placeholder, not an argument value' }
    const { wrapper } = await mountDetail()
    await openEditor(wrapper)
    await argsInput(wrapper).setValue('--api-key\n••••••••\n--verbose')
    await save(wrapper)
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(true)
    // The backend's zero-based args[1] is the second line of the text box.
    const error = wrapper.find('[data-test="server-process-error"]').text()
    expect(error).toContain('Argument line 2 is a masked credential')
    expect(error).not.toContain('redaction placeholder')
    expect((argsInput(wrapper).element as HTMLTextAreaElement).value).toContain('--verbose')
  })

  it('shows any other backend error as it is', async () => {
    patchResponse = { success: false, error: "server 'sqlite' not found" }
    const { wrapper } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('node')
    await save(wrapper)
    expect(wrapper.find('[data-test="server-process-error"]').text()).toBe("server 'sqlite' not found")
  })

  it('names the text-box line of a refused argument, counting blank lines', async () => {
    patchResponse = { success: false, error: 'args[1] is a redaction placeholder, not an argument value' }
    const { wrapper } = await mountDetail()
    await openEditor(wrapper)
    await argsInput(wrapper).setValue('--api-key\n\n••••••••')
    await save(wrapper)
    expect(wrapper.find('[data-test="server-process-error"]').text()).toContain('Argument line 3 is a masked credential')
  })

  it('does not rewrite arguments it cannot show one per line', async () => {
    for (const args of [['--prefix', ' padded '], ['--suffix', '']]) {
      serverArgs = args
      const { wrapper, api } = await mountDetail()
      await openEditor(wrapper)
      expect(argsInput(wrapper).attributes('disabled')).toBeDefined()
      expect(wrapper.find('[data-test="server-args-multiline-note"]').exists()).toBe(true)
      await save(wrapper)
      expect(api.patchServer).not.toHaveBeenCalled()
      await openEditor(wrapper)
      await commandInput(wrapper).setValue('node')
      await save(wrapper)
      expect(api.patchServer).toHaveBeenCalledWith('sqlite', { command: 'node' })
      vi.clearAllMocks()
      wrapper.unmount()
    }
  })

  it('keeps a stored command with surrounding space when the field was not touched', async () => {
    serverCommand = '/opt/mcp/server '
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await argsInput(wrapper).setValue('--verbose')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', { args: ['--verbose'] })
  })

  it('refuses to save over a command line that changed elsewhere while editing', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('node')

    // Another client changes the arguments; the store refreshes underneath.
    serverArgs = ['mcp-server-sqlite', '--db-path', '/data/new.db']
    const { useServersStore } = await import('@/stores/servers')
    await useServersStore((wrapper.vm as any).$pinia).fetchServers(true)
    await flushPromises()

    await save(wrapper)
    expect(api.patchServer).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-process-error"]').text()).toContain('changed somewhere else')

    // Reopening starts from the newer value.
    await wrapper.find('[data-test="server-process-cancel"]').trigger('click')
    await openEditor(wrapper)
    expect((argsInput(wrapper).element as HTMLTextAreaElement).value).toContain('/data/new.db')
  })

  it('drops the draft when the view moves to another server', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('rm')
    await wrapper.setProps({ serverName: 'other' })
    await flushPromises()
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="server-process-card"]').text()).toContain('other.js')
    await openEditor(wrapper)
    expect((commandInput(wrapper).element as HTMLInputElement).value).toBe('node')
    expect(api.patchServer).not.toHaveBeenCalled()
  })

  it('cancel discards the draft', async () => {
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    await commandInput(wrapper).setValue('node')
    await wrapper.find('[data-test="server-process-cancel"]').trigger('click')
    expect(wrapper.find('[data-test="server-process-form"]').exists()).toBe(false)
    expect(api.patchServer).not.toHaveBeenCalled()
    await openEditor(wrapper)
    expect((commandInput(wrapper).element as HTMLInputElement).value).toBe('uvx')
  })

  it('does not offer line editing for an argument that contains a line break', async () => {
    serverArgs = ['-c', 'echo one\necho two']
    const { wrapper, api } = await mountDetail()
    await openEditor(wrapper)
    expect(argsInput(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="server-args-multiline-note"]').exists()).toBe(true)
    await commandInput(wrapper).setValue('bash')
    await save(wrapper)
    expect(api.patchServer).toHaveBeenCalledWith('sqlite', { command: 'bash' })
  })
})
