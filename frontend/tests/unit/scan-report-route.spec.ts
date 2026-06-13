import { describe, it, expect } from 'vitest'
import { createRouter, createWebHistory } from 'vue-router'
import { scanReportPath } from '@/utils/serverRoute'

// MCP-2125 (Defect B of MCP-2123): scan ids embed the raw upstream server name,
// so official-registry servers whose names contain '/' (e.g.
// "com.pulsemcp/google-flights") produce a scan id like
// "scan-com.pulsemcp/google-flights-1781284446323229000". The scan-report route
// is a single `:jobId` segment, so an unencoded '/' splits the path and falls
// through to the catch-all 404. The id MUST be percent-encoded; vue-router v4
// decodes the param back on read (same class as MCP-1112 / serverDetailPath).

const SLASH_SCAN_ID = 'scan-com.pulsemcp/google-flights-1781284446323229000'

describe('scanReportPath (MCP-2125)', () => {
  it('percent-encodes a "/"-containing scan id into a single path segment', () => {
    expect(scanReportPath(SLASH_SCAN_ID)).toBe(
      '/security/scans/scan-com.pulsemcp%2Fgoogle-flights-1781284446323229000'
    )
  })

  it('leaves a plain scan id untouched (no "/" to encode)', () => {
    expect(scanReportPath('scan-github-123')).toBe('/security/scans/scan-github-123')
  })
})

describe('scan-report route round-trip (MCP-2125)', () => {
  it('decodes the encoded "/" back into the jobId param (no 404)', async () => {
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
        { path: '/:pathMatch(.*)*', name: 'not-found', component: { template: '<div>404</div>' } },
      ],
    })
    await router.push(scanReportPath(SLASH_SCAN_ID))
    await router.isReady()
    // It must match scan-report (NOT the catch-all 404)...
    expect(router.currentRoute.value.name).toBe('scan-report')
    // ...and the param must be decoded back to the original scan id.
    expect(router.currentRoute.value.params.jobId).toBe(SLASH_SCAN_ID)
  })
})
