import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 107 PR-C cross-review round 2, chunk 4 (P1): the tenant dashboard's
// default landing panel is Usage (analytics), which calls
// GET /api/v1/activity/usage on mount and every 30s — core `/activity*` is
// on the FR-045 must-refuse list for a tenant session (rest-endpoints.md
// §"core reads": "/activity*" — outside the allowlist), so every tenant page
// load and refresh interval drew a spurious 403 there, exactly the pattern
// round 1 fixed for `loadActivitySummary`. `refreshSecurityScannerStatus()`
// (Dashboard.vue's onMounted, hitting GET /api/v1/security/overview, also
// must-refuse) had the same gap: it is called unconditionally, unlike its
// four sibling loaders which all carry `principalKind === 'tenant'` guards.
//
// Both must be silent for a tenant principal — no call at all, not a
// call-then-403 — matching FR-041's "hidden rather than issued-and-403'd".

const usageSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    success: true,
    data: { window: '24h', tokens_saved: 0, tokens_saved_percentage: 0, tools: [], timeline: [] },
  })
)
const refreshSecuritySpy = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const fakeEventSource = {
    onopen: null,
    onmessage: null,
    onerror: null,
    addEventListener() {},
    removeEventListener() {},
    close() {},
  }
  const base: Record<string, unknown> = {
    getActivityUsage: usageSpy,
    getServers: ok({ servers: [{ name: 'srv-a', enabled: true, connected: true, tool_count: 1 }] }),
    createEventSource: vi.fn(() => fakeEventSource),
    hasAPIKey: vi.fn(() => false),
    getAPIKeyPreview: vi.fn(() => 'none'),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = ok()
        return target[prop]
      },
    }),
  }
})

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  refreshSecurityScannerStatus: refreshSecuritySpy,
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import Dashboard from '@/views/Dashboard.vue'
import UsageView from '@/views/Usage.vue'
import { useAuthStore } from '@/stores/auth'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountDashboardAsTenant() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const authStore = useAuthStore()
  authStore.isTeamsEdition = true
  authStore.user = {
    id: 'carol',
    email: 'carol@example.com',
    display_name: 'Carol',
    role: 'user',
    provider: 'oidc',
    created_at: '',
    last_login_at: '',
  }

  return shallowMount(Dashboard, {
    global: {
      plugins: [router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        Suspense: false,
        UsageView,
      },
    },
  })
}

describe('Dashboard tenant gating (Spec 107 FR-041, cross-review round 2 P1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageSpy.mockClear()
    refreshSecuritySpy.mockClear()
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('never calls GET /api/v1/activity/usage (Usage panel) for a tenant principal', async () => {
    await mountDashboardAsTenant()
    await flushPromises()

    expect(usageSpy).not.toHaveBeenCalled()
  })

  it('never calls refreshSecurityScannerStatus for a tenant principal', async () => {
    await mountDashboardAsTenant()
    await flushPromises()

    expect(refreshSecuritySpy).not.toHaveBeenCalled()
  })
})
