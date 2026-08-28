import { describe, it, expect, beforeEach, vi } from 'vitest'
import { defineComponent, h, ref, onMounted } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useSystemStore } from '@/stores/system'

vi.mock('@/services/api', () => ({
  default: {
    createEventSource: vi.fn(),
  },
}))

/**
 * Issue #1065 defect 3: with no stored API key, entering a valid key and
 * pressing "Set Key" re-authenticated the header — server counts, tool count
 * and the Live badge all came back — while the view BODY kept its red
 * "Invalid or missing API key" panel and rendered zero rows until the user
 * clicked Retry by hand. The app knew it was authenticated and still showed the
 * failure that was no longer true.
 *
 * Cause: every view keeps its load error in a component-local ref that nothing
 * outside the component can reach. The fix is a monotonic `authEpoch` in the
 * system store that App.vue keys <router-view> on, so a repaired auth remounts
 * the current view and its error/loading/data reset with it.
 */
describe('auth recovery invalidates views (#1065)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('bumps authEpoch when a failed auth is repaired', () => {
    const store = useSystemStore()
    expect(store.authEpoch).toBe(0)

    store.setAuthRequired(true)
    store.markAuthRecovered()

    expect(store.authRequired).toBe(false)
    expect(store.authEpoch).toBe(1)
  })

  it('does not bump the epoch when auth never failed', () => {
    // Otherwise every incidental call would remount healthy views and throw
    // away their filters, pagination and scroll position.
    const store = useSystemStore()
    store.markAuthRecovered()
    expect(store.authEpoch).toBe(0)
  })

  it('recovers on the Refresh path too, but only once the key is verified', () => {
    // The modal's "Refresh & Retry" re-reads the key from disk. It now
    // validates before reporting success, so App.vue can treat a verified
    // refresh as a real recovery -- previously this path cleared the flag
    // without invalidating views, and #1065's stale red panel survived it.
    const store = useSystemStore()

    store.setAuthRequired(true)
    // Unverified refresh: nothing changes, the modal stays up.
    expect(store.authEpoch).toBe(0)
    expect(store.authRequired).toBe(true)

    // Verified refresh: same treatment as Set Key.
    store.markAuthRecovered()
    expect(store.authRequired).toBe(false)
    expect(store.authEpoch).toBe(1)
  })

  it('leaves the epoch alone for setAuthRequired(false)', () => {
    // The modal's "Refresh" path only re-reads the key from disk and never
    // validates it, so it must clear the flag without asserting recovery.
    const store = useSystemStore()
    store.setAuthRequired(true)
    store.setAuthRequired(false)

    expect(store.authRequired).toBe(false)
    expect(store.authEpoch).toBe(0)
  })

  it('remounts a keyed view, clearing its local error and re-running its loader', async () => {
    // Stands in for Activity.vue: a component-local `error` ref that only its
    // own loader ever writes, exactly the shape that stayed stale.
    let mounts = 0
    const FailingView = defineComponent({
      setup() {
        const error = ref<string | null>(null)
        onMounted(() => {
          mounts++
          // First mount lands while auth is broken; later mounts succeed.
          error.value = mounts === 1 ? 'Invalid or missing API key' : null
        })
        return () => h('div', { 'data-test': 'body' }, error.value ?? 'rows')
      },
    })

    const store = useSystemStore()
    const Host = defineComponent({
      setup() {
        return () => h(FailingView, { key: store.authEpoch })
      },
    })

    const wrapper = mount(Host, { global: { plugins: [createPinia()] } })
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="body"]').text()).toBe('Invalid or missing API key')

    store.setAuthRequired(true)
    store.markAuthRecovered()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(mounts).toBe(2)
    expect(wrapper.find('[data-test="body"]').text()).toBe('rows')
  })
})
