import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import api from '@/services/api'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import AssignClientDialog from '@/components/profiles/AssignClientDialog.vue'
import BulkMoveDialog from '@/components/clients/BulkMoveDialog.vue'
import ClientConnectList from '@/components/ClientConnectList.vue'
import { WORK_RO, WORK_FULL, makeClient } from './fixtures/profiles108i'

// Issue #1446 (Spec 108-i review): stale-async and scope-list follow-ups.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getClients: vi.fn(),
    getClient: vi.fn(),
    getRouting: vi.fn(),
    getProfiles: vi.fn(),
    setClientBinding: vi.fn(),
    bulkAssignClients: vi.fn(),
    getConnectStatus: vi.fn(),
    getConnectClientStatus: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    disconnectClient: vi.fn(),
    getOnboardingState: vi.fn(),
  },
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(r => { resolve = r })
  return { promise, resolve }
}
const ok = (clients: any[]) => ({ success: true, data: { clients, warnings: [] } })
const live = (id: string) => makeClient(id, { active_sessions: 1 })
const idle = (id: string) => makeClient(id, { active_sessions: 0 })

beforeEach(() => {
  setActivePinia(createPinia())
  for (const fn of Object.values(api) as any[]) fn.mockReset?.()
  ;(api.hasAPIKey as any).mockReturnValue(true)
  ;(api.getRouting as any).mockResolvedValue({ success: true, data: null })
})

describe('clients store (1446-4, 1446-6, 1446-3)', () => {
  it('an older load() cannot overwrite a newer refreshPresence()', async () => {
    const store = useClientsStore()
    const slow = deferred<any>()
    ;(api.getClients as any).mockReturnValueOnce(slow.promise)
    const loading = store.load()
    ;(api.getClients as any).mockResolvedValue(ok([live('new')]))
    await store.refreshPresence()
    expect(store.clients.map(c => c.id)).toEqual(['new'])
    slow.resolve(ok([idle('old')]))
    await loading
    expect(store.clients.map(c => c.id)).toEqual(['new'])
    expect(store.loading).toBe(false)
  })

  it('an older refreshPresence() cannot overwrite a newer load()', async () => {
    const store = useClientsStore()
    const slow = deferred<any>()
    ;(api.getClients as any).mockReturnValueOnce(slow.promise)
    const polling = store.refreshPresence()
    ;(api.getClients as any).mockResolvedValue(ok([live('new')]))
    await store.load()
    slow.resolve(ok([idle('old')]))
    await polling
    expect(store.clients.map(c => c.id)).toEqual(['new'])
  })

  it('liveCount counts the unscoped roster, so a scoped page load does not shrink it', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockImplementation(async (scope: any = {}) => ok(scope?.client ? [live('cursor')] : [live('cursor'), live('codex'), idle('zed')]))
    await store.load({ client: 'cursor' })
    expect(store.clients.map(c => c.id)).toEqual(['cursor'])
    expect(store.liveCount).toBe(2)
    await store.refreshPresence()
    expect(store.clients).toHaveLength(1)
    expect(store.liveCount).toBe(2)
  })

  it('clearScope() marks the unscoped roster for refetch until the next refresh', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue(ok([live('cursor')]))
    await store.load({ client: 'cursor' })
    store.clearScope()
    expect(store.stale).toBe(true)
    await store.refreshPresence()
    expect(store.stale).toBe(false)
  })
})

describe('AssignClientDialog (1446-3)', () => {
  it('lists eligible clients from the unscoped roster and refreshes it on open even when scoped rows exist', async () => {
    const store = useClientsStore()
    const bound = (id: string) => makeClient(id, { credential_state: 'client' })
    ;(api.getClients as any).mockImplementation(async (scope: any = {}) => ok(scope?.client ? [bound('cursor')] : [bound('cursor'), bound('codex')]))
    await store.load({ client: 'cursor' })
    vi.mocked(api.getClients).mockClear()
    HTMLDialogElement.prototype.showModal = vi.fn()
    const wrapper = mount(AssignClientDialog, { props: { open: false, profileName: 'work-ro' } })
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(api.getClients).toHaveBeenCalled()
    const options = wrapper.findAll('option').map(o => o.attributes('value'))
    expect(options).toContain('codex')
    expect(options).toContain('cursor')
  })
})

describe('BulkMoveDialog (1446-13, 1446-14)', () => {
  function mountDialog() {
    HTMLDialogElement.prototype.showModal = vi.fn()
    const profiles = useProfilesStore()
    profiles.profiles = [WORK_RO, WORK_FULL] as any
    return mount(BulkMoveDialog, { props: { open: false, clients: [makeClient('scoped', { profile: 'work-ro' })], initialFrom: 'work-ro' } })
  }

  it('shows counting and disables Move while the unscoped list is pending, never the filtered rows', async () => {
    const wrapper = mountDialog()
    const pending = deferred<any>()
    ;(api.getClients as any).mockReturnValue(pending.promise)
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(wrapper.get('[data-test="bulk-preview-line"]').text()).toMatch(/Counting/i)
    await wrapper.get('[data-test="bulk-to"]').setValue('work-full')
    expect(wrapper.get('[data-test="bulk-submit"]').attributes('disabled')).toBeDefined()
    pending.resolve(ok([makeClient('a', { profile: 'work-ro' }), makeClient('b', { profile: 'work-ro' })]))
    await flushPromises()
    expect(wrapper.get('[data-test="bulk-preview-line"]').text()).toMatch(/^2 clients use /)
    expect(wrapper.get('[data-test="bulk-submit"]').attributes('disabled')).toBeUndefined()
  })

  it('a failed load keeps Move disabled and offers a retry', async () => {
    const wrapper = mountDialog()
    ;(api.getClients as any).mockRejectedValueOnce(new Error('offline'))
    await wrapper.setProps({ open: true })
    await flushPromises()
    await wrapper.get('[data-test="bulk-to"]').setValue('work-full')
    expect(wrapper.find('[data-test="bulk-count-failed"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="bulk-submit"]').attributes('disabled')).toBeDefined()
    ;(api.getClients as any).mockResolvedValue(ok([makeClient('a', { profile: 'work-ro' })]))
    await wrapper.get('[data-test="bulk-count-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="bulk-count-failed"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="bulk-submit"]').attributes('disabled')).toBeUndefined()
  })

  it('a slow response of an earlier open is ignored', async () => {
    const wrapper = mountDialog()
    const first = deferred<any>()
    ;(api.getClients as any).mockReturnValueOnce(first.promise)
    await wrapper.setProps({ open: true })
    await wrapper.setProps({ open: false })
    ;(api.getClients as any).mockResolvedValueOnce(ok([makeClient('fresh', { profile: 'work-ro' })]))
    await wrapper.setProps({ open: true })
    await flushPromises()
    first.resolve(ok([makeClient('x', { profile: 'work-ro' }), makeClient('y', { profile: 'work-ro' }), makeClient('z', { profile: 'work-ro' })]))
    await flushPromises()
    expect(wrapper.get('[data-test="bulk-preview-line"]').text()).toMatch(/^1 client uses /)
  })
})

describe('ClientConnectList (1446-7, 1446-11, 1446-12)', () => {
  const guard = Object.assign(new Error('refused'), { name: 'ApiError', error: 'GUARD-TEXT', code: 'binding_bypassable_without_auth', status: 409, fixes: [{ kind: 'require_mcp_auth' }] })
  function previewOf(token: string) {
    return {
      success: true,
      data: { client: 'cursor', config_path: '/x/mcp.json', display_path: '~/x/mcp.json', format: 'json', server_key: 'mcpServers', server_name: 'mcpproxy', entry_text: '{}', entry_exists: false, contains_api_key: false, access_state: 'accessible', precondition_token: token },
    }
  }
  async function open() {
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [{ id: 'cursor', name: 'Cursor', config_path: '/x/mcp.json', exists: true, connected: false, supported: true, icon: 'cursor' }] })
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getClients as any).mockResolvedValue(ok([makeClient('cursor')]))
    useProfilesStore().profiles = [WORK_RO] as any
    HTMLDialogElement.prototype.showModal = vi.fn()
    const wrapper = mount(ClientConnectList, { props: { show: false } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    return wrapper
  }

  it('disables Connect while a changed intent refreshes the preview, and ignores a slower older preview', async () => {
    ;(api.getConnectPreview as any).mockResolvedValueOnce(previewOf('tok-1'))
    const wrapper = await open()
    await wrapper.get('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    const confirm = () => wrapper.get('[data-test="client-preview-confirm-cursor"]')
    expect(confirm().attributes('disabled')).toBeUndefined()

    const slow = deferred<any>()
    ;(api.getConnectPreview as any).mockReturnValueOnce(slow.promise)
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('work-ro')
    await flushPromises()
    expect(confirm().attributes('disabled')).toBeDefined()

    const fast = deferred<any>()
    ;(api.getConnectPreview as any).mockReturnValueOnce(fast.promise)
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('')
    fast.resolve(previewOf('tok-new'))
    await flushPromises()
    slow.resolve(previewOf('tok-old'))
    await flushPromises()
    expect(confirm().attributes('disabled')).toBeUndefined()
    ;(api.connectClient as any).mockResolvedValue({ success: true, data: { success: true, message: 'ok', config_path: '/x', backup_path: '' } })
    await confirm().trigger('click')
    await flushPromises()
    expect((api.connectClient as any).mock.calls[0][3].precondition_token).toBe('tok-new')
  })

  it('Cancel clears a guard refusal of that client', async () => {
    ;(api.getConnectPreview as any).mockResolvedValueOnce(previewOf('tok-1'))
    const wrapper = await open()
    await wrapper.get('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    ;(api.connectClient as any).mockRejectedValue(guard)
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    const vm: any = wrapper.vm
    expect(wrapper.find('[data-test="guard-refusal"]').exists()).toBe(true)
    await wrapper.get('[data-test="client-preview-cancel-cursor"]').trigger('click')
    await flushPromises()
    expect(vm).toBeTruthy()
    expect(wrapper.find('[data-test="connect-bulk-refusals"]').exists()).toBe(false)
    // Re-open: no stale refusal is rendered next to the new preview.
    ;(api.getConnectPreview as any).mockResolvedValueOnce(previewOf('tok-2'))
    await wrapper.get('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="guard-refusal"]').exists()).toBe(false)
  })
})
