import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAttentionStore } from '@/stores/attention'
import api from '@/services/api'

// Review finding: the attention list has three independent, uncancelled
// callers — SidebarNav.vue, TopHeader.vue and AttentionList.vue each fetch
// onMounted, plus the SSE-triggered silent refetch on `attention.changed` —
// and none of them are sequenced. A response that was already in flight when
// a newer one landed must not be able to resurrect a stale list, mirroring
// the servers.ts issueSeq/appliedSeq guard (servers-store-list-sequencing.spec.ts).

vi.mock('@/services/api', () => ({
  default: {
    getAttention: vi.fn(),
  },
}))

function mkItem(id: string) {
  return {
    id,
    kind: 'server_review',
    rank: 50,
    subject: { type: 'server', id, name: id },
    summary: `${id}: waiting for review`,
    fix: { verb: 'review', label: 'Review', target: `/review/${id}` },
    since: new Date().toISOString(),
  }
}

describe('useAttentionStore — fetch sequencing', () => {
  let store: ReturnType<typeof useAttentionStore>

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  afterEach(() => {
    // Each store instance registers its own window listener on creation and
    // never tears it down on its own — without this a listener from a prior
    // test's store instance leaks onto `window` and double-fires the next
    // test's SSE dispatch against a mock queue sized for one call.
    store?.cleanupEventListeners()
  })

  it('drops a stale fetch response that lands after a newer one', async () => {
    let resolveFirst: (v: unknown) => void = () => {}
    const first = new Promise((resolve) => {
      resolveFirst = resolve
    })
    ;(api.getAttention as any).mockReturnValueOnce(first)
    ;(api.getAttention as any).mockResolvedValueOnce({
      success: true,
      data: { count: 1, generated_at: new Date().toISOString(), items: [mkItem('fresh')] },
    })

    store = useAttentionStore()
    // Overlapping requests, e.g. SidebarNav's and TopHeader's mount fetches.
    const slow = store.fetchAttention()
    await store.fetchAttention()

    expect(store.items.map((i) => i.id)).toEqual(['fresh'])

    // The older, slower request (e.g. a stalled header GET) finally answers
    // with a pre-change list — it must not overwrite the newer result.
    resolveFirst({
      success: true,
      data: { count: 0, generated_at: new Date().toISOString(), items: [] },
    })
    await slow

    expect(store.items.map((i) => i.id)).toEqual(['fresh'])
  })

  it('does not let a stalled mount fetch overwrite a list delivered by a later SSE-triggered refetch', async () => {
    let resolveStalled: (v: unknown) => void = () => {}
    ;(api.getAttention as any).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveStalled = resolve
      })
    )
    ;(api.getAttention as any).mockResolvedValueOnce({
      success: true,
      data: { count: 1, generated_at: new Date().toISOString(), items: [mkItem('from-sse')] },
    })

    store = useAttentionStore()
    const stalled = store.fetchAttention()

    // attention.changed fires while the stalled mount fetch is still in flight.
    window.dispatchEvent(new CustomEvent('mcpproxy:attention-changed'))
    await Promise.resolve()
    await Promise.resolve()

    expect(store.items.map((i) => i.id)).toEqual(['from-sse'])

    resolveStalled({
      success: true,
      data: { count: 0, generated_at: new Date().toISOString(), items: [] },
    })
    await stalled

    expect(store.items.map((i) => i.id)).toEqual(['from-sse'])
  })
})
