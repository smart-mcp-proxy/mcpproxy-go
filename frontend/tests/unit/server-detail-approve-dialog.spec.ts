import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Review round 1 of the F09 fix. The approve-dialog paragraph exists TWICE —
// once in ServerCard.vue and once here in ServerDetail.vue — and only the
// ServerCard copy was pinned (scanner-gate-wording.spec.ts). Reverting the
// ServerDetail hunk to the old "Force-approving bypasses the scanner gate."
// left the whole 1236-test suite green, so the half of the fix that lands on
// the screen F09 was actually reported against was unverified and free to
// drift back.
//
// This file pins it at the surface a user reaches: quarantined server with a
// dangerous verdict, click the banner's Approve, read the dialog.

const SERVER = 'context7-docs'

let serverQuarantined = true

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: SERVER,
              protocol: 'http',
              url: 'https://mcp.context7.com/mcp',
              enabled: true,
              connected: false,
              quarantined: serverQuarantined,
              trust_mode: 'scan',
              tool_count: 0,
              security_scan: {
                status: 'dangerous',
                risk_score: 60,
                last_scan_at: '2026-09-05T10:00:00Z',
                finding_counts: { dangerous: 2, warning: 0, info: 0, total: 2 },
              },
            },
          ],
        })
      ),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 1, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() => Promise.resolve({ success: false, error: 'none' })),
      getScanStatus: vi.fn(() => ok({ id: 'scan-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-2' })),
      getToolDiff: vi.fn(() => ok({})),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
      securityApprove: vi.fn(() => ok({})),
    },
  }
})

async function mountDetail() {
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
    ],
  })
  await router.push(`/servers/${encodeURIComponent(SERVER)}`)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: SERVER },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverQuarantined = true
})

describe('ServerDetail — the approve dialog names the gate it skips (F09)', () => {
  it('says what force approval does instead of naming an undefined "scanner gate"', async () => {
    const wrapper = await mountDetail()
    await wrapper.get('[data-test="quarantine-action-approve"]').trigger('click')

    const modal = wrapper.get('.modal-open')
    expect(modal.text()).toContain('2 dangerous findings')
    expect(modal.text()).not.toContain('the scanner gate')
    expect(modal.text()).toContain('skips the scan-based approval gate')
    expect(modal.text()).toContain('unquarantines this server')
  })
})
