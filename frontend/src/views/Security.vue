<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Security</h1>
        <p class="text-base-content/70 mt-1">Manage security scanners and scan quarantined servers</p>
      </div>
      <div class="flex gap-2">
        <button @click="startScanAll" :disabled="loading || scanAllRunning" class="btn btn-primary">
          <span v-if="scanAllRunning" class="loading loading-spinner loading-sm"></span>
          {{ scanAllRunning ? 'Scanning...' : 'Scan All Servers' }}
        </button>
        <button @click="refresh" :disabled="loading" class="btn btn-outline">
          <span v-if="loading" class="loading loading-spinner loading-sm"></span>
          {{ loading ? 'Refreshing...' : 'Refresh' }}
        </button>
      </div>
    </div>

    <!-- Scan All Progress Card -->
    <div v-if="queueProgress && queueProgress.status !== 'idle'" class="card bg-base-100 shadow-xl">
      <div class="card-body">
        <h2 class="card-title text-lg">Scanning All Servers</h2>
        <p class="text-sm text-base-content/70">
          Progress: {{ queueProgress.completed || 0 }}/{{ queueProgress.total || 0 }} completed,
          {{ queueProgress.running || 0 }} running<span v-if="queueProgress.skipped">, {{ queueProgress.skipped }} skipped</span>
          <span class="ml-2 font-mono text-base-content/50">{{ scanAllElapsedStr }}</span>
        </p>

        <!-- Progress bar -->
        <div class="w-full bg-base-200 rounded-full h-4 mt-2">
          <div
            class="h-4 rounded-full transition-all duration-500"
            :class="queueProgress.status === 'cancelled' ? 'bg-warning' : 'bg-primary'"
            :style="{ width: queueProgressPercent + '%' }"
          ></div>
        </div>
        <p class="text-xs text-base-content/50 mt-1">{{ queueProgressPercent }}%</p>

        <!-- Items table -->
        <div v-if="queueProgress.items?.length" class="overflow-x-auto mt-4">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>Server</th>
                <th>Status</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in queueProgress.items" :key="item.server_name">
                <td class="font-mono text-sm">{{ item.server_name }}</td>
                <td>
                  <span class="badge badge-sm" :class="queueItemBadgeClass(item.status)">{{ item.status }}</span>
                </td>
                <td class="text-xs text-base-content/60">{{ item.error || item.skip_reason || '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Cancel button -->
        <div class="card-actions justify-end mt-2" v-if="queueProgress.status === 'running'">
          <button @click="cancelAllScans" class="btn btn-sm btn-warning btn-outline">Cancel All</button>
        </div>
        <div v-else class="text-sm text-base-content/50 mt-2">
          Batch scan {{ queueProgress.status }}.
        </div>
      </div>
    </div>

    <!-- Overview Stats -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Scanners Installed</div>
        <div class="stat-value">{{ overview.scanners_installed || 0 }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Total Scans</div>
        <div class="stat-value">{{ overview.total_scans || 0 }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Active Scans</div>
        <div class="stat-value" :class="overview.active_scans > 0 ? 'text-warning' : ''">{{ overview.active_scans || 0 }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Findings</div>
        <div class="stat-value" :class="totalFindings > 0 ? 'text-error' : 'text-success'">{{ totalFindings }}</div>
        <div class="stat-desc" v-if="overview.findings_by_severity">
          {{ overview.findings_by_severity.critical || 0 }} critical, {{ overview.findings_by_severity.high || 0 }} high
        </div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading security data...</p>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error">
      <div>
        <h3 class="font-bold">Error</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="refresh" class="btn btn-sm">Retry</button>
    </div>

    <template v-else>
      <!-- Available Scanners -->
      <div class="card bg-base-100 shadow-xl">
        <div class="card-body">
          <h2 class="card-title">Security Scanners</h2>
          <p class="text-sm text-base-content/70 mb-4">Install and configure security scanners to analyze MCP servers</p>

          <div v-if="scanners.length === 0" class="text-center py-8 text-base-content/50">
            No scanners available. Check Docker connectivity.
          </div>

          <div v-else class="overflow-x-auto">
            <table class="table table-zebra">
              <thead>
                <tr>
                  <th>Scanner</th>
                  <th>Vendor</th>
                  <th>Inputs</th>
                  <th>Status</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="scanner in scanners" :key="scanner.id">
                  <td>
                    <div class="font-bold">{{ scanner.name }}</div>
                    <div class="text-sm text-base-content/50">{{ scanner.description }}</div>
                  </td>
                  <td>{{ scanner.vendor }}</td>
                  <td>
                    <div class="flex flex-wrap gap-1">
                      <span v-for="input in scanner.inputs" :key="input" class="badge badge-sm badge-outline">{{ input }}</span>
                    </div>
                  </td>
                  <td>
                    <span class="badge" :class="statusBadgeClass(scanner.status)">{{ scannerDisplayStatus(scanner.status) }}</span>
                  </td>
                  <td>
                    <div class="flex gap-2 items-center">
                      <input
                        type="checkbox"
                        class="toggle toggle-sm toggle-primary"
                        :checked="scanner.status !== 'available'"
                        :disabled="installing === scanner.id"
                        @change="toggleScanner(scanner)"
                      />
                      <span v-if="installing === scanner.id" class="loading loading-spinner loading-xs"></span>
                      <button
                        v-if="scanner.status === 'installed' || scanner.status === 'configured'"
                        @click="openConfigDialog(scanner)"
                        class="btn btn-sm btn-outline"
                      >
                        Configure
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- Scan History -->
      <div class="card bg-base-100 shadow-xl">
        <div class="card-body">
          <div class="flex justify-between items-start">
            <div>
              <h2 class="card-title">Scan History</h2>
              <p class="text-sm text-base-content/70 mb-4">All security scan results across servers</p>
            </div>
            <div class="flex items-center gap-2">
              <span class="text-sm text-base-content/50">Sort by:</span>
              <select v-model="historySort" @change="onSortChange" class="select select-sm select-bordered">
                <option value="started_at">Date</option>
                <option value="findings_count">Findings</option>
              </select>
              <button @click="toggleSortOrder" class="btn btn-sm btn-ghost btn-square" :title="'Order: ' + historyOrder">
                <svg v-if="historyOrder === 'desc'" xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" /></svg>
                <svg v-else xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" /></svg>
              </button>
            </div>
          </div>

          <div v-if="historyLoading && scanHistory.length === 0" class="text-center py-8">
            <span class="loading loading-spinner loading-md"></span>
            <p class="mt-2 text-sm text-base-content/50">Loading scan history...</p>
          </div>

          <div v-else-if="scanHistory.length === 0" class="text-center py-8 text-base-content/50">
            No scan history yet. Use "Scan All Servers" to start scanning.
          </div>

          <div v-else class="overflow-x-auto">
            <table class="table table-zebra">
              <thead>
                <tr>
                  <th>Server</th>
                  <th>Scan ID</th>
                  <th class="cursor-pointer select-none" @click="toggleSort('started_at')">
                    Date
                    <span v-if="historySort === 'started_at'" class="ml-1">{{ historyOrder === 'desc' ? '▼' : '▲' }}</span>
                  </th>
                  <th>Status</th>
                  <th class="cursor-pointer select-none" @click="toggleSort('findings_count')">
                    Findings
                    <span v-if="historySort === 'findings_count'" class="ml-1">{{ historyOrder === 'desc' ? '▼' : '▲' }}</span>
                  </th>
                  <th>Risk</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="scan in scanHistory" :key="scan.job_id">
                  <td>
                    <router-link :to="`/servers/${encodeURIComponent(scan.server_name)}`" class="link link-primary font-medium">
                      {{ scan.server_name }}
                    </router-link>
                    <div v-if="scan.pass === 2" class="text-xs text-base-content/50">(Pass 2)</div>
                  </td>
                  <td>
                    <span class="font-mono text-sm text-base-content/70">{{ (scan.job_id || '').substring(0, 8) }}</span>
                  </td>
                  <td>
                    <span class="tooltip" :data-tip="scan.started_at">
                      {{ timeAgo(scan.started_at) }}
                    </span>
                  </td>
                  <td>
                    <span class="badge badge-sm" :class="scanStatusBadge(scan.status)">
                      <span v-if="scan.status === 'running'" class="loading loading-spinner loading-xs mr-1"></span>
                      {{ scan.status }}
                    </span>
                  </td>
                  <td>
                    <span :class="{ 'font-bold': (scan.findings_count || 0) > 0 }">{{ scan.findings_count || 0 }}</span>
                  </td>
                  <td>
                    <span v-if="scan.risk_score != null" :class="riskScoreClass(scan.risk_score)">{{ scan.risk_score }}</span>
                    <span v-else class="text-base-content/30">-</span>
                  </td>
                  <td>
                    <router-link
                      v-if="scan.status === 'completed'"
                      :to="`/security/scans/${encodeURIComponent(scan.job_id)}`"
                      class="link link-primary text-sm whitespace-nowrap"
                    >
                      Details →
                    </router-link>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- Load More -->
          <div v-if="scanHistory.length < historyTotal" class="text-center mt-4">
            <button @click="loadMoreHistory" :disabled="historyLoading" class="btn btn-sm btn-outline">
              <span v-if="historyLoading" class="loading loading-spinner loading-xs"></span>
              Load More ({{ scanHistory.length }}/{{ historyTotal }})
            </button>
          </div>
        </div>
      </div>
    </template>

    <!-- Configure Scanner Dialog -->
    <dialog ref="configDialog" class="modal">
      <div class="modal-box max-w-lg">
        <h3 class="font-bold text-lg">Configure {{ configScanner?.name }}</h3>
        <p class="text-sm text-base-content/60 mt-1">Set API keys and environment variables. Secrets are stored in your OS keychain.</p>
        <div class="py-4 space-y-4" v-if="configScanner">
          <!-- Required env vars -->
          <div v-for="env in (configScanner.required_env || [])" :key="env.key" class="form-control">
            <label class="label">
              <span class="label-text font-medium">{{ env.label }}</span>
              <span class="badge badge-sm badge-error">Required</span>
            </label>
            <input
              v-model="configValues[env.key]"
              :type="env.secret ? 'password' : 'text'"
              :placeholder="configuredPlaceholder(env.key)"
              class="input input-bordered"
            />
          </div>
          <!-- Optional env vars -->
          <div v-for="env in (configScanner.optional_env || [])" :key="env.key" class="form-control">
            <label class="label">
              <span class="label-text">{{ env.label }}</span>
              <span class="badge badge-sm badge-ghost">Optional</span>
            </label>
            <input
              v-model="configValues[env.key]"
              :type="env.secret ? 'password' : 'text'"
              :placeholder="configuredPlaceholder(env.key)"
              class="input input-bordered"
            />
          </div>
          <!-- Custom env var -->
          <div class="divider text-xs">Add Custom Variable</div>
          <div class="flex gap-2">
            <input v-model="customEnvKey" type="text" placeholder="OPENAI_API_KEY" class="input input-bordered input-sm flex-1" />
            <input v-model="customEnvValue" type="password" placeholder="Value" class="input input-bordered input-sm flex-1" />
            <button @click="addCustomEnv" :disabled="!customEnvKey || !customEnvValue" class="btn btn-sm btn-outline">Add</button>
          </div>
          <!-- Show existing configured vars -->
          <div v-if="Object.keys(configValues).length > 0" class="mt-2">
            <div class="text-xs text-base-content/50 mb-1">Configured variables:</div>
            <div v-for="(val, key) in configValues" :key="key" class="flex items-center gap-2 text-sm py-1">
              <code class="font-mono text-xs bg-base-200 px-2 py-0.5 rounded">{{ key }}</code>
              <span class="text-base-content/50">{{ val.startsWith('${keyring:') ? 'stored in keyring' : 'set' }}</span>
              <button @click="delete configValues[key]" class="btn btn-ghost btn-xs text-error">x</button>
            </div>
          </div>
        </div>
        <div class="modal-action">
          <button @click="closeConfigDialog" class="btn">Cancel</button>
          <button @click="saveConfig" class="btn btn-primary">Save</button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop"><button>close</button></form>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import api from '@/services/api'

const loading = ref(false)
const error = ref('')
const scanners = ref<any[]>([])
const overview = ref<any>({})
const installing = ref<string | null>(null)

// Scan history state
const scanHistory = ref<any[]>([])
const historyLoading = ref(false)
const historySort = ref('started_at')
const historyOrder = ref('desc')
const historyTotal = ref(0)
const historyOffset = ref(0)
const HISTORY_PAGE_SIZE = 20

// Scan All state
const scanAllRunning = ref(false)
const scanAllStartTime = ref<number>(0)
const scanAllElapsed = ref(0)
let scanAllElapsedTimer: ReturnType<typeof setInterval> | null = null
const queueProgress = ref<any>(null)
let queuePollTimer: ReturnType<typeof setInterval> | null = null

const scanAllElapsedStr = computed(() => {
  const s = scanAllElapsed.value
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const sec = s % 60
  return `${m}m ${sec}s`
})

// Config dialog
const configDialog = ref<HTMLDialogElement>()
const configScanner = ref<any>(null)
const configValues = ref<Record<string, string>>({})
const customEnvKey = ref('')
const customEnvValue = ref('')

const totalFindings = computed(() => overview.value?.findings_by_severity?.total || 0)

const queueProgressPercent = computed(() => {
  const p = queueProgress.value
  if (!p || !p.total) return 0
  return Math.round(((p.completed || 0) + (p.failed || 0) + (p.skipped || 0)) / p.total * 100)
})

function queueItemBadgeClass(status: string) {
  switch (status) {
    case 'completed': return 'badge-success'
    case 'running': return 'badge-info'
    case 'failed': return 'badge-error'
    case 'skipped': return 'badge-ghost'
    case 'cancelled': return 'badge-warning'
    default: return 'badge-ghost'
  }
}

function statusBadgeClass(status: string) {
  switch (status) {
    case 'configured': return 'badge-success'
    case 'installed': return 'badge-info'
    case 'available': return 'badge-ghost'
    case 'error': return 'badge-error'
    default: return 'badge-ghost'
  }
}

function scannerDisplayStatus(status: string): string {
  switch (status) {
    case 'installed':
    case 'configured':
      return 'enabled'
    case 'available':
      return 'disabled'
    default:
      return status
  }
}

function riskScoreClass(score: number) {
  if (score >= 70) return 'text-error'
  if (score >= 40) return 'text-warning'
  return 'text-success'
}

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const [scannersRes, overviewRes] = await Promise.all([
      api.listScanners(),
      api.getSecurityOverview(),
    ])
    if (scannersRes.success) scanners.value = scannersRes.data || []
    if (overviewRes.success) overview.value = overviewRes.data || {}
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function toggleScanner(scanner: any) {
  installing.value = scanner.id
  try {
    if (scanner.status === 'available') {
      const res = await api.installScanner(scanner.id)
      if (!res.success) {
        error.value = `Failed to enable: ${res.error}`
      }
    } else {
      await api.removeScanner(scanner.id)
    }
    await refresh()
  } finally {
    installing.value = null
  }
}

function openConfigDialog(scanner: any) {
  configScanner.value = scanner
  // Pre-populate with existing configured values
  const existing = scanner.configured_env || {}
  configValues.value = { ...existing }
  customEnvKey.value = ''
  customEnvValue.value = ''
  configDialog.value?.showModal()
}

function closeConfigDialog() {
  configDialog.value?.close()
}

function configuredPlaceholder(key: string): string {
  const existing = configScanner.value?.configured_env?.[key]
  if (existing) {
    if (existing.startsWith('${keyring:')) return '(stored in keyring)'
    return '(configured)'
  }
  return key
}

function addCustomEnv() {
  if (customEnvKey.value && customEnvValue.value) {
    configValues.value[customEnvKey.value] = customEnvValue.value
    customEnvKey.value = ''
    customEnvValue.value = ''
  }
}

async function saveConfig() {
  if (!configScanner.value) return
  // Only send non-empty values that aren't keyring references (new values)
  const toSend: Record<string, string> = {}
  for (const [k, v] of Object.entries(configValues.value)) {
    if (v && !v.startsWith('${keyring:')) {
      toSend[k] = v
    }
  }
  if (Object.keys(toSend).length > 0) {
    await api.configureScanner(configScanner.value.id, toSend)
  }
  closeConfigDialog()
  await refresh()
}

function toggleSort(field: string) {
  if (historySort.value === field) {
    historyOrder.value = historyOrder.value === 'desc' ? 'asc' : 'desc'
  } else {
    historySort.value = field
    historyOrder.value = 'desc'
  }
  historyOffset.value = 0
  scanHistory.value = []
  loadHistory()
}

function onSortChange() {
  historyOffset.value = 0
  scanHistory.value = []
  loadHistory()
}

function toggleSortOrder() {
  historyOrder.value = historyOrder.value === 'desc' ? 'asc' : 'desc'
  historyOffset.value = 0
  scanHistory.value = []
  loadHistory()
}

function scanStatusBadge(status: string) {
  switch (status) {
    case 'completed': return 'badge-success'
    case 'running': return 'badge-info'
    case 'failed': return 'badge-error'
    case 'cancelled': return 'badge-warning'
    default: return 'badge-ghost'
  }
}

function timeAgo(dateStr: string): string {
  if (!dateStr) return '-'
  const diff = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

async function loadHistory() {
  historyLoading.value = true
  try {
    const res = await api.listScanHistory({
      sort: historySort.value,
      order: historyOrder.value,
      limit: HISTORY_PAGE_SIZE,
      offset: historyOffset.value,
    })
    if (res.success && res.data) {
      if (historyOffset.value === 0) {
        scanHistory.value = res.data.scans || []
      } else {
        scanHistory.value.push(...(res.data.scans || []))
      }
      historyTotal.value = res.data.total || 0
    }
  } catch {
    // Ignore history load errors
  } finally {
    historyLoading.value = false
  }
}

async function loadMoreHistory() {
  historyOffset.value += HISTORY_PAGE_SIZE
  await loadHistory()
}

async function startScanAll() {
  scanAllRunning.value = true
  scanAllStartTime.value = Date.now()
  scanAllElapsed.value = 0
  scanAllElapsedTimer = setInterval(() => {
    scanAllElapsed.value = Math.floor((Date.now() - scanAllStartTime.value) / 1000)
  }, 1000)
  try {
    const res = await api.scanAll()
    if (!res.success) {
      error.value = `Failed to start batch scan: ${res.error}`
      scanAllRunning.value = false
      if (scanAllElapsedTimer) { clearInterval(scanAllElapsedTimer); scanAllElapsedTimer = null }
      return
    }
    queueProgress.value = res.data
    // Start polling
    startQueuePolling()
  } catch (e: any) {
    error.value = e.message
    scanAllRunning.value = false
  }
}

function startQueuePolling() {
  stopQueuePolling()
  queuePollTimer = setInterval(async () => {
    try {
      const res = await api.getQueueProgress()
      if (res.success && res.data) {
        queueProgress.value = res.data
        // Stop polling when done
        if (res.data.status === 'completed' || res.data.status === 'cancelled') {
          stopQueuePolling()
          if (scanAllElapsedTimer) { clearInterval(scanAllElapsedTimer); scanAllElapsedTimer = null }
          scanAllRunning.value = false
          // Auto-refresh page data
          await refresh()
        }
      }
    } catch {
      // Ignore polling errors
    }
  }, 3000)
}

function stopQueuePolling() {
  if (queuePollTimer) {
    clearInterval(queuePollTimer)
    queuePollTimer = null
  }
}

async function cancelAllScans() {
  try {
    await api.cancelAllScans()
    // Progress will update via polling
  } catch (e: any) {
    error.value = e.message
  }
}

onMounted(async () => {
  await Promise.all([refresh(), loadHistory()])
  // Check if a batch scan is already running
  try {
    const res = await api.getQueueProgress()
    if (res.success && res.data && res.data.status === 'running') {
      queueProgress.value = res.data
      scanAllRunning.value = true
      startQueuePolling()
    }
  } catch {
    // Ignore
  }
})

onUnmounted(() => {
  stopQueuePolling()
  if (scanAllElapsedTimer) { clearInterval(scanAllElapsedTimer); scanAllElapsedTimer = null }
})
</script>
