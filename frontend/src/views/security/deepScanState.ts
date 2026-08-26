// Deep-scan state helpers for the Security page (pure, unit-tested).
//
// The page's one job is telling the truth about what a scan will actually run:
// a scanner row must never read "enabled" in green while the deep-scan layer
// that would run it is off — that mismatch is exactly what confused operators
// (owner report, 2026-08-04: five enabled scanners, every report tpa-only).

/** The shape these helpers need from a scanner-list entry. */
export interface ScannerLike {
  status?: string
  docker_image?: string
}

/** Scanner statuses that mean "the operator has turned this scanner on". */
const enabledStatuses = new Set(['installed', 'configured'])

export function isScannerEnabled(status: string): boolean {
  return enabledStatuses.has(status)
}

/**
 * Deep scan governs only Docker-based scanners. The built-in baseline
 * (`tpa-descriptions`) is in the same list with no docker_image and ALWAYS
 * runs — counting it as "won't run" would be the same lie in the other
 * direction.
 */
export function isDockerScanner(scanner: ScannerLike): boolean {
  return Boolean(scanner.docker_image)
}

/**
 * Whether a row must show the "won't run" truth instead of a green "enabled":
 * a Docker scanner is on, but the deep-scan layer that would run it is off.
 * `null` deep-scan state (config not loaded yet) never accuses — the green
 * badge stays until the truth is known.
 */
export function scannerWontRun(scanner: ScannerLike, deepScanEnabled: boolean | null): boolean {
  return (
    deepScanEnabled === false &&
    isDockerScanner(scanner) &&
    isScannerEnabled(String(scanner.status ?? ''))
  )
}

/** Docker scanners the operator has turned on — the ones deep scan governs. */
export function enabledDockerScanners(scanners: ScannerLike[]): number {
  return scanners.filter(s => isDockerScanner(s) && isScannerEnabled(String(s.status ?? ''))).length
}

/**
 * Tooltip for the page-level "Scan All Servers" action.
 *
 * Docker is NOT a precondition for scanning: the offline baseline scanner is
 * built into mcpproxy and runs in-process for every server. Only the optional
 * deep scanners need Docker. The old copy ("Docker is required to run security
 * scanners") came with a hard `disabled` on the button, which left a default
 * install — no Docker, no installed scanner — with no way to scan at all, the
 * same bug already fixed for the per-server Scan Now button (spec 088 FR-016).
 */
export function scanAllTooltip(dockerAvailable: boolean | null | undefined): string {
  if (dockerAvailable === false) {
    return 'Optional deep scanners need Docker; the offline baseline scan is built in'
  }
  return 'Runs the built-in offline baseline scan on every server'
}

/**
 * The master card's one-line status. Names what a scan will do right now,
 * from the operator's side of the screen.
 */
export function deepScanSummary(enabled: boolean | null, enabledScanners: number): string {
  if (enabled === null) {
    return 'Checking configuration…'
  }
  if (enabled) {
    return 'Enabled scanners below run in Docker with every scan, alongside the built-in offline baseline.'
  }
  if (enabledScanners > 0) {
    const n = enabledScanners
    return `Off — scans run only the built-in offline baseline. The ${n === 1 ? '1 scanner' : `${n} scanners`} enabled below will not run until deep scan is on.`
  }
  return 'Off — scans run only the built-in offline baseline. Enable scanners below and turn deep scan on to add them.'
}
