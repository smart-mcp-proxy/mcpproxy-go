import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import ServerCard from '@/components/ServerCard.vue'
import { serverDetailPath } from '@/utils/serverRoute'

// MCP-1112 (#598): official-registry server names contain '/'
// (e.g. "io.github.owner/repo"). An unencoded `/servers/<namespace>/<name>`
// path splits into two segments and falls through to the catch-all 404, so the
// '/' MUST be percent-encoded. vue-router decodes the param back on read.

const SLASH_NAME = 'io.github.owner/repo'

describe('serverDetailPath (MCP-1112)', () => {
  it('percent-encodes a "/"-containing server name into a single path segment', () => {
    expect(serverDetailPath(SLASH_NAME)).toBe('/servers/io.github.owner%2Frepo')
  })

  it('appends a tab query without encoding the "?"/"="', () => {
    expect(serverDetailPath(SLASH_NAME, 'tools')).toBe(
      '/servers/io.github.owner%2Frepo?tab=tools'
    )
  })

  it('leaves a plain name untouched (no "/" to encode)', () => {
    expect(serverDetailPath('github')).toBe('/servers/github')
    expect(serverDetailPath('github', 'logs')).toBe('/servers/github?tab=logs')
  })
})

describe('server-detail route round-trip (MCP-1112)', () => {
  it('decodes the encoded "/" back into the serverName param', async () => {
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div/>' } },
        { path: '/:pathMatch(.*)*', name: 'not-found', component: { template: '<div>404</div>' } },
      ],
    })
    await router.push(serverDetailPath(SLASH_NAME))
    await router.isReady()
    // It must match server-detail (NOT the catch-all 404)...
    expect(router.currentRoute.value.name).toBe('server-detail')
    // ...and the param must be decoded back to the original name.
    expect(router.currentRoute.value.params.serverName).toBe(SLASH_NAME)
  })
})

describe('ServerCard slash-name links + title preference (MCP-1112)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  function mountCard(server: Record<string, unknown>) {
    const router = createRouter({
      history: createWebHistory(),
      routes: [{ path: '/', component: { template: '<div>Home</div>' } }],
    })
    return mount(ServerCard, {
      props: { server },
      global: { plugins: [createPinia(), router] },
    })
  }

  it('renders the server-detail link with the "/" percent-encoded', () => {
    const wrapper = mountCard({
      name: SLASH_NAME,
      protocol: 'stdio',
      enabled: true,
      connected: true,
      tool_count: 3,
    })
    const detailLink = wrapper.find('[data-test="server-detail-link"]')
    expect(detailLink.exists()).toBe(true)
    const href = detailLink.attributes('href') || ''
    expect(href).toContain('io.github.owner%2Frepo')
    expect(href).not.toContain('io.github.owner/repo')

    // Belt-and-suspenders: NO server-detail link anywhere on the card may
    // leave the '/' unencoded (it would split the path and 404).
    const allDetailHrefs = wrapper
      .findAll('a')
      .map((a) => a.attributes('href') || '')
      .filter((h) => h.startsWith('/servers/'))
    expect(allDetailHrefs.length).toBeGreaterThan(0)
    for (const h of allDetailHrefs) {
      expect(h).not.toContain('io.github.owner/repo')
    }
  })

  it('prefers the registry-provided title over the raw reverse-DNS name', () => {
    const wrapper = mountCard({
      name: SLASH_NAME,
      title: 'Owner Repo',
      protocol: 'stdio',
      enabled: true,
      connected: true,
      tool_count: 0,
    })
    expect(wrapper.find('[data-test="server-card-title"]').text()).toBe('Owner Repo')
  })

  it('falls back to the name when no title is present', () => {
    const wrapper = mountCard({
      name: 'github',
      protocol: 'stdio',
      enabled: true,
      connected: true,
      tool_count: 0,
    })
    expect(wrapper.find('[data-test="server-card-title"]').text()).toBe('github')
  })
})
