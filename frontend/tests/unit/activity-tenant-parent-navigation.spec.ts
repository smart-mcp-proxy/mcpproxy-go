import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 107 PR-C cross-review round 2, chunk 4 (P2): a tenant session reads
// its activity through GET /user/activity (getUserActivity), which has no
// request_id filter (T086: `{items,total}`, limit/offset only). When a
// child record's parent is not among the loaded rows, viewParentCall()
// fell back to GET /api/v1/activity?request_id=... — the core, admin-only
// door (named must-refuse) — even for a tenant, drawing a 403 from a
// perfectly ordinary tenant interaction.

const CHILD = {
  id: 'act-child',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-08-21T10:00:01Z',
  server_name: 'github',
  tool_name: 'create_issue',
  request_id: 'req-child',
  parent_id: 'req-parent-not-in-page',
  duration_ms: 120,
}

const getActivitiesMock = vi.hoisted(() => vi.fn())
const getUserActivityMock = vi.hoisted(() =>
  vi.fn(() => Promise.resolve({ success: true, data: { items: [CHILD], total: 1 } }))
)

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: getActivitiesMock,
      getUserActivity: getUserActivityMock,
      getActivitySummary: vi.fn(() => ok({ period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 })),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/user/activity/export?format=json'),
    },
  }
})

async function mountActivityAsTenant() {
  const { useAuthStore } = await import('@/stores/auth')
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push('/activity')
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

  const wrapper = mount(Activity, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('Activity Log tenant parent-call fallback (Spec 107, cross-review round 2 P2)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getActivitiesMock.mockClear()
    getUserActivityMock.mockClear()
  })

  it('never calls the core GET /activity door, even when the parent is not in the loaded page', async () => {
    const wrapper = await mountActivityAsTenant()
    expect(getUserActivityMock).toHaveBeenCalled()

    await wrapper.find('[data-test="activity-row"]').trigger('click')
    await flushPromises()

    const viewParent = wrapper.find('[data-test="activity-view-parent"]')
    expect(viewParent.exists()).toBe(true)
    await viewParent.trigger('click')
    await flushPromises()

    expect(getActivitiesMock).not.toHaveBeenCalled()
  })
})
