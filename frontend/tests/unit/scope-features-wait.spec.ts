import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// Spec 108-j J3 (url-filter-contract.md rule 1): a page whose URL carries
// profile/client/token must not fetch before GET /status has said whether the
// build supports them, or the first request goes out unfiltered and the page
// flashes every row before the refetch. waitForScopeFeatures() is that gate.

const getStatusSpy = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({ default: { getStatus: getStatusSpy } }))

import { useSystemStore } from '@/stores/system'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

describe('systemStore.waitForScopeFeatures (Spec 108-j J3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getStatusSpy.mockReset()
    setAvailableFeatures([])
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('resolves once /status answers with the features', async () => {
    let resolveStatus!: (v: unknown) => void
    getStatusSpy.mockReturnValue(new Promise(r => { resolveStatus = r }))
    const store = useSystemStore()
    const fetching = store.fetchScopeFilterFeatures()
    let waited = false
    const waiting = store.waitForScopeFeatures().then(() => { waited = true })
    await Promise.resolve()
    expect(waited).toBe(false)
    expect(store.scopeFeaturesKnown).toBe(false)

    resolveStatus({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    await fetching
    await waiting
    expect(waited).toBe(true)
    expect(store.scopeFeaturesKnown).toBe(true)
  })

  it('resolves when /status fails, so a page never hangs on an old or offline core', async () => {
    getStatusSpy.mockRejectedValue(new Error('network down'))
    const store = useSystemStore()
    const waiting = store.waitForScopeFeatures()
    await store.fetchScopeFilterFeatures()
    await expect(waiting).resolves.toBeUndefined()
    expect(store.scopeFeaturesKnown).toBe(true)
  })

  it('resolves after the timeout when /status never answers', async () => {
    vi.useFakeTimers()
    getStatusSpy.mockReturnValue(new Promise(() => {}))
    const store = useSystemStore()
    void store.fetchScopeFilterFeatures()
    let waited = false
    const waiting = store.waitForScopeFeatures(2000).then(() => { waited = true })
    await vi.advanceTimersByTimeAsync(1999)
    expect(waited).toBe(false)
    await vi.advanceTimersByTimeAsync(2)
    await waiting
    expect(waited).toBe(true)
    expect(store.scopeFeaturesKnown).toBe(false)
  })

  it('resolves immediately when the features are already known', async () => {
    getStatusSpy.mockResolvedValue({ success: true, data: {} })
    const store = useSystemStore()
    await store.fetchScopeFilterFeatures()
    await expect(store.waitForScopeFeatures()).resolves.toBeUndefined()
  })

  it('resolves immediately when a scope parameter is already available (features set elsewhere)', async () => {
    setAvailableFeatures(['profile', 'client', 'token'])
    const store = useSystemStore()
    await expect(store.waitForScopeFeatures()).resolves.toBeUndefined()
  })
})
