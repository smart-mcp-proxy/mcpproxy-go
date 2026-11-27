import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { useServersStore } from '@/stores/servers'

// Shared by the Spec 134 PR 6 Tools specs: mounts the page with a router, a
// tools response and, optionally, a fleet list the page may read health from.
export const LAYOUT_KEY = 'mcpproxy.tools.layout'

export const tool = (over: Record<string, unknown>) => ({
  name: 't', server_name: 's', description: 'does a thing', approval_status: 'approved', disabled: false, config_denied: false, usage: 0, tier: 'read', ...over,
})

export async function mountTools(opts: {
  api: any
  tools: any[]
  servers?: any[]
  path?: string
  layout?: 'flat' | 'grouped'
  stats?: any
  partial?: boolean
  failed?: string[]
}) {
  window.localStorage.removeItem(LAYOUT_KEY)
  if (opts.layout) window.localStorage.setItem(LAYOUT_KEY, opts.layout)
  opts.api.getGlobalTools.mockResolvedValue({
    success: true,
    data: {
      tools: opts.tools,
      stats: opts.stats ?? { total: opts.tools.length, enabled: opts.tools.length, disabled: 0, pending_approval: 0 },
      partial: opts.partial ?? false,
      failed_servers: opts.failed ?? [],
    },
  })
  const Tools = (await import('@/views/Tools.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/', component: { template: '<div/>' } },
      { path: '/tools', component: Tools },
      { path: '/review', component: { template: '<div/>' } },
      { path: '/review/:server', component: { template: '<div/>' } },
      { path: '/servers', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/activity', name: 'activity', component: { template: '<div/>' } },
    ],
  })
  await router.push(opts.path ?? '/tools')
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  if (opts.servers) useServersStore().servers = opts.servers as any
  const wrapper = mount(Tools, { attachTo: document.body, global: { plugins: [pinia, router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}
