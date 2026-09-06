import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AddServerModal from '@/components/AddServerModal.vue'
import { useServersStore } from '@/stores/servers'
import api from '@/services/api'

// UX audit F09. Adding a second upstream that points at an endpoint the user
// has ALREADY configured is a configuration mistake, but nothing said so at add
// time: every "duplicate" check in the tree keys on the server NAME only
// (internal/server/server.go `server '%s' already exists`,
// internal/config/config.go `duplicate server name`, internal/configimport
// `already_exists`). The add succeeded silently, and the first thing the user
// heard about it was the scanner reporting the new server as DANGEROUS —
// detect.shadowing.cross_server fires one hard-tier "possible impersonation"
// finding per tool, because two entries on one endpoint expose byte-identical
// tool names AND descriptions.
//
// This panel is the observation the user should have had first: neutral, at
// add time, and NON-BLOCKING. Submit stays enabled, nothing is merged, nothing
// is reused — a second entry on one endpoint is legitimate (different headers,
// different OAuth identity), it is just rarely what someone means.
//
// Matching is exact string equality after trim, deliberately. Case folding,
// trailing-slash normalisation and query stripping are all judgement calls
// about what "the same endpoint" means; exact match catches the real case (a
// copy-pasted URL) and cannot produce a false accusation. It can MISS — the
// list payload masks credential-shaped url/args values
// (internal/oauth/serverfields.go), so a secret-bearing endpoint simply does
// not match — which is why the copy is a hint, not a guarantee.

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

const NOTE = '[data-test="addserver-duplicate-endpoint"]'

function seedServers(servers: Array<Record<string, unknown>>, loaded = true) {
  const store = useServersStore()
  store.servers = servers as never
  store.loaded = loaded
  return store
}

function mountModal() {
  return mount(AddServerModal, { props: { show: true } })
}

type Wrapper = ReturnType<typeof mountModal>

async function selectHttp(wrapper: Wrapper) {
  await wrapper.findAll('input[type="radio"][name="serverType"]')[1].setValue(true)
}

const HTTP_SERVER = {
  id: 'context7',
  name: 'context7',
  protocol: 'http',
  url: 'https://mcp.context7.com/mcp',
  enabled: true,
  quarantined: false,
  connected: true,
  status: 'ready',
  reconnect_count: 0,
  tool_count: 2,
  created: '2026-09-05T00:00:00Z',
  updated: '2026-09-05T00:00:00Z',
}

const STDIO_SERVER = {
  id: 'files',
  name: 'files',
  protocol: 'stdio',
  command: 'npx',
  args: ['@modelcontextprotocol/server-filesystem', '/tmp'],
  enabled: true,
  quarantined: false,
  connected: true,
  status: 'ready',
  reconnect_count: 0,
  tool_count: 3,
  created: '2026-09-05T00:00:00Z',
  updated: '2026-09-05T00:00:00Z',
}

describe('AddServerModal — duplicate endpoint observation (F09)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.callTool.mockResolvedValue({ success: true })
    mockedApi.getServers.mockResolvedValue({ success: true, data: { servers: [] } })
    mockedApi.getCanonicalConfigPaths.mockResolvedValue({ success: true, data: { paths: [] } })
  })

  // Both halves bite: an empty form field must not match a server that simply
  // has no value for that field. STDIO_SERVER carries no `url` and HTTP_SERVER
  // carries no `command`, so dropping the empty-input guard makes each of these
  // pair up with '' and fire on an untouched form.
  it('says nothing while the URL field is empty', async () => {
    seedServers([HTTP_SERVER, STDIO_SERVER])
    const wrapper = mountModal()
    await selectHttp(wrapper)
    expect(wrapper.find(NOTE).exists()).toBe(false)
  })

  it('says nothing while no command has been chosen', () => {
    seedServers([HTTP_SERVER, STDIO_SERVER])
    const wrapper = mountModal()
    expect(wrapper.find(NOTE).exists()).toBe(false)
  })

  it('names the server already configured at the typed URL', async () => {
    seedServers([HTTP_SERVER])
    const wrapper = mountModal()
    await selectHttp(wrapper)
    await wrapper.find('input[type="url"]').setValue('https://mcp.context7.com/mcp')

    const note = wrapper.get(NOTE)
    expect(note.text()).toContain('context7')
    expect(note.text()).toContain('already configured at this endpoint')
  })

  it('keeps the note advisory: submit stays enabled and it is not an error', async () => {
    seedServers([HTTP_SERVER])
    const wrapper = mountModal()
    await selectHttp(wrapper)
    await wrapper.find('input[type="text"]').setValue('context7-docs')
    await wrapper.find('input[type="url"]').setValue('https://mcp.context7.com/mcp')

    const submit = wrapper.get('[data-test="add-server-modal-box"] button[type="submit"]')
    expect(submit.attributes('disabled')).toBeUndefined()
    // Neutral note, not an alert-error: this is a configuration observation,
    // never a threat claim.
    expect(wrapper.get(NOTE).classes()).not.toContain('alert-error')
  })

  it('ignores surrounding whitespace but not a differing path', async () => {
    seedServers([HTTP_SERVER])
    const wrapper = mountModal()
    await selectHttp(wrapper)

    await wrapper.find('input[type="url"]').setValue('  https://mcp.context7.com/mcp  ')
    expect(wrapper.find(NOTE).exists()).toBe(true)

    // Exact match only — a trailing slash is a different string, and inventing
    // a normalisation policy here would be a guess.
    await wrapper.find('input[type="url"]').setValue('https://mcp.context7.com/mcp/')
    expect(wrapper.find(NOTE).exists()).toBe(false)
  })

  it('stays silent until the server list has actually been loaded', async () => {
    seedServers([HTTP_SERVER], false)
    const wrapper = mountModal()
    await selectHttp(wrapper)
    await wrapper.find('input[type="url"]').setValue('https://mcp.context7.com/mcp')
    expect(wrapper.find(NOTE).exists()).toBe(false)
  })

  it('matches a stdio server on command AND the full argument list', async () => {
    seedServers([STDIO_SERVER])
    const wrapper = mountModal()

    await wrapper.find('select').setValue('npx')
    await wrapper
      .find('textarea')
      .setValue('@modelcontextprotocol/server-filesystem\n/tmp')
    const note = wrapper.get(NOTE)
    expect(note.text()).toContain('files')

    // A different argument list is a different endpoint.
    await wrapper
      .find('textarea')
      .setValue('@modelcontextprotocol/server-filesystem\n/var')
    expect(wrapper.find(NOTE).exists()).toBe(false)
  })

  // The modal itself SENDS working_dir on every stdio add (handleSubmit builds
  // `serverData.working_dir`), so it is part of the endpoint this form
  // describes. `node server.js` in /projects/a and the same line in
  // /projects/b are two different programs exposing two different toolsets —
  // naming one as "already configured at this endpoint" is the false
  // accusation the exact-match rule exists to avoid, and the copy's claim that
  // "the security scan will flag each as a possible clone" would be wrong too.
  it('treats a different working directory as a different endpoint', async () => {
    seedServers([{ ...STDIO_SERVER, working_dir: '/projects/a' }])
    const wrapper = mountModal()

    await wrapper.find('select').setValue('npx')
    await wrapper
      .find('textarea')
      .setValue('@modelcontextprotocol/server-filesystem\n/tmp')
    await wrapper.find('input[placeholder="/path/to/project"]').setValue('/projects/b')
    expect(wrapper.find(NOTE).exists()).toBe(false)

    await wrapper.find('input[placeholder="/path/to/project"]').setValue('/projects/a')
    expect(wrapper.get(NOTE).text()).toContain('files')
  })

  // A hand-edited config can leave the other transport's fields populated on a
  // server (converting an entry from http to stdio and not deleting `url`, or
  // the reverse). Matching a populated-but-inactive field would name a server
  // that is not at the typed endpoint at all. The guard is deliberately
  // NEGATIVE — it excludes the opposite family only — so `streamable-http`,
  // `sse`, `auto` and an absent protocol all stay eligible for a URL match,
  // which is exactly the shape the real repro has (an existing entry recorded
  // as `streamable-http` while this modal always sends `http`).
  it('ignores a field left over from the other transport', async () => {
    seedServers([
      { ...STDIO_SERVER, url: 'https://mcp.context7.com/mcp' },
      { ...HTTP_SERVER, name: 'ctx-streamable', protocol: 'streamable-http' },
    ])
    const wrapper = mountModal()
    await selectHttp(wrapper)
    await wrapper.find('input[type="url"]').setValue('https://mcp.context7.com/mcp')

    // The stdio server's stale `url` must not match; the streamable-http one must.
    expect(wrapper.get(NOTE).text()).toContain('ctx-streamable')
    expect(wrapper.get(NOTE).text()).not.toContain('files')
  })

  it('does not match an http server against a stdio form, or vice versa', async () => {
    seedServers([HTTP_SERVER, STDIO_SERVER])
    const wrapper = mountModal()

    // stdio form, http server configured: the url must not be consulted.
    await wrapper.find('select').setValue('npx')
    expect(wrapper.find(NOTE).exists()).toBe(false)

    // http form, stdio server configured.
    await selectHttp(wrapper)
    await wrapper.find('input[type="url"]').setValue('npx')
    expect(wrapper.find(NOTE).exists()).toBe(false)
  })
})
