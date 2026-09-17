import { describe, it, expect, beforeEach } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 107 PR-C cross-review round 2, chunk 4 (P1): ModeSwitcher was
// unconditionally rendered in TopHeader for every principal kind, including
// a tenant session. routing_mode lives behind GET /routing and PATCH /config
// — both admin-only core doors (named must-refuse, rest-endpoints.md §8) —
// so a tenant who opened the panel and picked anything drew a fixed 403,
// contradicting FR-041's "hidden rather than issued-and-403'd" for
// tenant-inapplicable controls. TopHeader.vue must hide the control
// entirely for a tenant principal, not merely suppress its own fetch.

import TopHeader from '@/components/TopHeader.vue'
import { useAuthStore } from '@/stores/auth'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', name: 'dashboard', component: { template: '<div />' } }],
  })
}

async function mountTopHeaderAs(role: 'user' | 'admin') {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const authStore = useAuthStore()
  authStore.isTeamsEdition = true
  authStore.user = {
    id: 'u1',
    email: 'u1@example.com',
    display_name: 'U1',
    role,
    provider: 'oidc',
    created_at: '',
    last_login_at: '',
  }

  return shallowMount(TopHeader, {
    global: {
      plugins: [router],
      stubs: { RouterLink: true },
    },
  })
}

describe('TopHeader tenant gating (Spec 107 FR-041, cross-review round 2 P1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('hides the mode switcher for a tenant principal', async () => {
    const wrapper = await mountTopHeaderAs('user')
    expect(wrapper.find('[data-test="mode-switcher"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'ModeSwitcher' }).exists()).toBe(false)
  })

  it('still renders the mode switcher for an admin principal', async () => {
    const wrapper = await mountTopHeaderAs('admin')
    expect(wrapper.findComponent({ name: 'ModeSwitcher' }).exists()).toBe(true)
  })
})
