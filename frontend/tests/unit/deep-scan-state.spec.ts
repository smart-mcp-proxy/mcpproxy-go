import { describe, it, expect } from 'vitest'
import {
  deepScanSummary,
  enabledDockerScanners,
  isDockerScanner,
  isScannerEnabled,
  scannerWontRun,
} from '../../src/views/security/deepScanState'

const docker = (status: string) => ({ status, docker_image: 'ghcr.io/example/scanner' })
const baseline = (status: string) => ({ status, docker_image: '' })

describe('scannerWontRun', () => {
  it('flags an enabled Docker scanner when the deep-scan layer is off', () => {
    expect(scannerWontRun(docker('installed'), false)).toBe(true)
    expect(scannerWontRun(docker('configured'), false)).toBe(true)
  })

  it('never flags the built-in baseline — it always runs, deep scan or not', () => {
    // tpa-descriptions ships in the same list with no docker_image; calling
    // it "won\u2019t run" would be the same lie in the other direction.
    expect(scannerWontRun(baseline('installed'), false)).toBe(false)
  })

  it('never flags when deep scan is on', () => {
    expect(scannerWontRun(docker('installed'), true)).toBe(false)
  })

  it('never flags a disabled or transitional scanner', () => {
    for (const status of ['available', 'pulling', 'error']) {
      expect(scannerWontRun(docker(status), false)).toBe(false)
    }
  })

  it('stays quiet until the config has loaded', () => {
    // Accusing a row of "won\u2019t run" from an unloaded config would flicker
    // the truth badge on every page load.
    expect(scannerWontRun(docker('installed'), null)).toBe(false)
  })
})

describe('isScannerEnabled / isDockerScanner', () => {
  it('treats installed and configured as on, everything else as off', () => {
    expect(isScannerEnabled('installed')).toBe(true)
    expect(isScannerEnabled('configured')).toBe(true)
    expect(isScannerEnabled('available')).toBe(false)
    expect(isScannerEnabled('pulling')).toBe(false)
    expect(isScannerEnabled('error')).toBe(false)
  })

  it('separates Docker scanners from the built-in baseline', () => {
    expect(isDockerScanner(docker('installed'))).toBe(true)
    expect(isDockerScanner(baseline('installed'))).toBe(false)
  })
})

describe('enabledDockerScanners', () => {
  it('counts only enabled Docker scanners, never the baseline', () => {
    expect(
      enabledDockerScanners([
        baseline('installed'), // always-on tpa-descriptions
        docker('installed'),
        docker('configured'),
        docker('available'),
      ])
    ).toBe(2)
  })
})

describe('deepScanSummary', () => {
  it('says what runs when the layer is on', () => {
    expect(deepScanSummary(true, 3)).toContain('run in Docker with every scan')
  })

  it('counts the scanners that will not run when the layer is off', () => {
    expect(deepScanSummary(false, 5)).toContain('5 scanners enabled below will not run')
    expect(deepScanSummary(false, 1)).toContain('1 scanner enabled below will not run')
  })

  it('invites setup when nothing is enabled yet', () => {
    expect(deepScanSummary(false, 0)).toContain('Enable scanners below')
  })

  it('admits when the state is not yet known', () => {
    expect(deepScanSummary(null, 5)).toBe('Checking configuration\u2026')
  })
})
