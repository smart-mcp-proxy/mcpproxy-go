// Reactive composable that exposes whether any security scanner is currently
// enabled. Used by Servers.vue, ServerDetail.vue, and other places that show
// scan-trigger buttons — buttons should be hidden entirely when no scanner is
// active.
//
// The result is cached at module scope so multiple components share a single
// network call. Call refreshSecurityScannerStatus() after the user
// installs/removes a scanner to update the state.

import { ref } from 'vue'
import api from '@/services/api'

const enabledCount = ref<number | null>(null)
const dockerAvailable = ref<boolean>(true)
const loaded = ref(false)
const totalFindings = ref<number>(0)
const totalScans = ref<number>(0)
let inflight: Promise<void> | null = null

export async function refreshSecurityScannerStatus(): Promise<void> {
  if (inflight) {
    return inflight
  }
  inflight = (async () => {
    try {
      const res = await api.getSecurityOverview()
      const data: any = res?.data ?? {}
      // Prefer the new scanners_enabled field; fall back to scanners_installed
      // for backwards compatibility with older mcpproxy builds.
      const enabled = data.scanners_enabled ?? data.scanners_installed ?? 0
      enabledCount.value = typeof enabled === 'number' ? enabled : 0
      dockerAvailable.value = data.docker_available !== false
      // Aggregated finding count from the /security/overview endpoint used by
      // the Dashboard security chip (F-12). The backend returns this under
      // `findings_by_severity.total`.
      const fbs = data.findings_by_severity ?? {}
      totalFindings.value = typeof fbs.total === 'number' ? fbs.total : 0
      totalScans.value = typeof data.total_scans === 'number' ? data.total_scans : 0
      loaded.value = true
    } catch {
      // On error, keep current values; the UI will fall back to dockerAvailable
      // checks. We don't want a transient API blip to make all scan buttons
      // disappear.
    } finally {
      inflight = null
    }
  })()
  return inflight
}

export function useSecurityScannerStatus() {
  if (!loaded.value && !inflight) {
    void refreshSecurityScannerStatus()
  }
  return {
    enabledCount,
    dockerAvailable,
    loaded,
    totalFindings,
    totalScans,
    /** True when at least one scanner is enabled and docker is available. */
    hasEnabledScanners: () => (enabledCount.value ?? 0) > 0,
    /** True when at least one scan has completed (ever). */
    hasAnyScans: () => totalScans.value > 0,
    refresh: refreshSecurityScannerStatus,
  }
}
