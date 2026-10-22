import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// The shared composable carries signature_bundle from the one
// /security/overview fetch, so Review and Home need no second request.

vi.mock('@/services/api', () => ({ default: { getSecurityOverview: vi.fn() } }))

describe('useSecurityScannerStatus signatureBundle', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetModules()
  })

  it('exposes the signature_bundle from the overview and shares one ref', async () => {
    const api = (await import('@/services/api')).default as any
    api.getSecurityOverview.mockResolvedValue({
      success: true,
      data: { scanners_enabled: 1, signature_bundle: { coverage_degraded: true, degraded_reason: 'x' } },
    })
    const mod = await import('@/composables/useSecurityScannerStatus')
    await mod.refreshSecurityScannerStatus()
    const a = mod.useSecurityScannerStatus()
    const b = mod.useSecurityScannerStatus()
    expect(a.signatureBundle.value).toEqual({ coverage_degraded: true, degraded_reason: 'x' })
    expect(b.signatureBundle).toBe(a.signatureBundle)
    expect(api.getSecurityOverview).toHaveBeenCalledTimes(1)
  })

  it('stays null when the overview carries no bundle', async () => {
    const api = (await import('@/services/api')).default as any
    api.getSecurityOverview.mockResolvedValue({ success: true, data: { scanners_enabled: 1 } })
    const mod = await import('@/composables/useSecurityScannerStatus')
    await mod.refreshSecurityScannerStatus()
    expect(mod.useSecurityScannerStatus().signatureBundle.value).toBeNull()
  })
})
