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
                    <span class="badge" :class="statusBadgeClass(scanner.status)">{{ scanner.status }}</span>
                  </td>
                  <td>
                    <div class="flex gap-2">
                      <button
                        v-if="scanner.status === 'available'"
                        @click="installScanner(scanner.id)"
                        :disabled="installing === scanner.id"
                        class="btn btn-sm btn-primary"
                      >
                        <span v-if="installing === scanner.id" class="loading loading-spinner loading-xs"></span>
                        Install
                      </button>
                      <button
                        v-if="scanner.status === 'installed' || scanner.status === 'configured'"
                        @click="openConfigDialog(scanner)"
                        class="btn btn-sm btn-outline"
                      >
                        Configure
                      </button>
                      <button
                        v-if="scanner.status !== 'available'"
                        @click="removeScanner(scanner.id)"
                        class="btn btn-sm btn-ghost text-error"
                      >
                        Remove
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- Recent Scan Reports -->
      <div class="card bg-base-100 shadow-xl" v-if="scanners.some(s => s.status !== 'available')">
        <div class="card-body">
          <h2 class="card-title">Scan a Server</h2>
          <p class="text-sm text-base-content/70 mb-4">Select a quarantined server to scan with installed scanners</p>

          <div class="flex gap-4 items-end">
            <div class="form-control flex-1">
              <label class="label"><span class="label-text">Server Name</span></label>
              <input v-model="scanServerName" type="text" placeholder="e.g., github-server" class="input input-bordered" />
            </div>
            <button @click="startScan" :disabled="!scanServerName || scanning" class="btn btn-primary">
              <span v-if="scanning" class="loading loading-spinner loading-sm"></span>
              {{ scanning ? 'Scanning...' : 'Start Scan' }}
            </button>
          </div>

          <!-- Scan Result -->
          <div v-if="scanResult" class="mt-6">
            <div class="divider">Scan Result</div>
            <div class="flex gap-4 mb-4">
              <div class="stat bg-base-200 rounded-lg p-4">
                <div class="stat-title text-sm">Risk Score</div>
                <div class="stat-value text-2xl" :class="riskScoreClass(scanResult.risk_score)">{{ scanResult.risk_score }}/100</div>
              </div>
              <div class="stat bg-base-200 rounded-lg p-4" v-if="scanResult.summary">
                <div class="stat-title text-sm">Findings</div>
                <div class="stat-value text-2xl">{{ scanResult.summary.total }}</div>
                <div class="stat-desc">
                  <span class="text-error">{{ scanResult.summary.critical }} critical</span>,
                  <span class="text-warning">{{ scanResult.summary.high }} high</span>
                </div>
              </div>
            </div>

            <!-- Findings Table -->
            <div v-if="scanResult.findings?.length" class="overflow-x-auto">
              <table class="table table-sm">
                <thead>
                  <tr>
                    <th>Severity</th>
                    <th>Finding</th>
                    <th>Package</th>
                    <th>Fix</th>
                    <th>Scanner</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(finding, idx) in scanResult.findings" :key="idx">
                    <td>
                      <div class="flex flex-col items-center gap-1">
                        <span class="badge badge-sm" :class="severityBadgeClass(finding.severity)">{{ finding.severity }}</span>
                        <span v-if="finding.cvss_score" class="text-xs text-base-content/50">{{ finding.cvss_score.toFixed(1) }}</span>
                      </div>
                    </td>
                    <td>
                      <div class="font-medium">
                        <a v-if="finding.help_uri" :href="finding.help_uri" target="_blank" class="link link-primary">
                          {{ finding.rule_id || finding.title }}
                        </a>
                        <span v-else>{{ finding.rule_id || finding.title }}</span>
                      </div>
                      <div class="text-sm text-base-content/60 max-w-md truncate">{{ finding.title }}</div>
                      <div v-if="finding.location" class="text-xs font-mono text-base-content/40 mt-1">{{ finding.location }}</div>
                    </td>
                    <td>
                      <div v-if="finding.package_name" class="font-mono text-sm">{{ finding.package_name }}</div>
                      <div v-if="finding.installed_version" class="text-xs text-base-content/50">v{{ finding.installed_version }}</div>
                    </td>
                    <td>
                      <span v-if="finding.fixed_version" class="badge badge-sm badge-success badge-outline">{{ finding.fixed_version }}</span>
                      <span v-else class="text-xs text-base-content/30">-</span>
                    </td>
                    <td class="text-sm text-base-content/70">{{ finding.scanner }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div v-else class="alert alert-success mt-4">
              No security findings detected.
            </div>

            <!-- Approve/Reject Actions -->
            <div class="flex gap-2 mt-4">
              <button @click="approveServer(scanServerName)" class="btn btn-success">Approve Server</button>
              <button @click="rejectServer(scanServerName)" class="btn btn-error btn-outline">Reject Server</button>
            </div>
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
const scanServerName = ref('')
const scanning = ref(false)
const scanResult = ref<any>(null)

// Scan All state
const scanAllRunning = ref(false)
const queueProgress = ref<any>(null)
let queuePollTimer: ReturnType<typeof setInterval> | null = null

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

function severityBadgeClass(severity: string) {
  switch (severity) {
    case 'critical': return 'badge-error'
    case 'high': return 'badge-warning'
    case 'medium': return 'badge-info'
    case 'low': return 'badge-ghost'
    default: return 'badge-ghost'
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

async function installScanner(id: string) {
  installing.value = id
  try {
    const res = await api.installScanner(id)
    if (!res.success) {
      error.value = `Failed to install: ${res.error}`
    }
    await refresh()
  } finally {
    installing.value = null
  }
}

async function removeScanner(id: string) {
  if (!confirm(`Remove scanner ${id}?`)) return
  await api.removeScanner(id)
  await refresh()
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

async function startScan() {
  if (!scanServerName.value) return
  scanning.value = true
  scanResult.value = null
  try {
    const startRes = await api.startScan(scanServerName.value)
    if (!startRes.success) {
      error.value = `Scan failed: ${startRes.error}`
      return
    }
    // Poll for completion
    let attempts = 0
    while (attempts < 60) {
      await new Promise(r => setTimeout(r, 2000))
      const statusRes = await api.getScanStatus(scanServerName.value)
      if (statusRes.success && statusRes.data) {
        if (statusRes.data.status === 'completed' || statusRes.data.status === 'failed') {
          break
        }
      }
      attempts++
    }
    // Get report
    const reportRes = await api.getScanReport(scanServerName.value)
    if (reportRes.success) {
      scanResult.value = reportRes.data
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    scanning.value = false
    await refresh()
  }
}

async function approveServer(name: string) {
  const force = scanResult.value?.summary?.critical > 0
  if (force && !confirm('Server has critical findings. Force approve?')) return
  await api.securityApprove(name, force)
  scanResult.value = null
  await refresh()
}

async function rejectServer(name: string) {
  if (!confirm(`Reject and remove ${name}?`)) return
  await api.securityReject(name)
  scanResult.value = null
  await refresh()
}

async function startScanAll() {
  scanAllRunning.value = true
  try {
    const res = await api.scanAll()
    if (!res.success) {
      error.value = `Failed to start batch scan: ${res.error}`
      scanAllRunning.value = false
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
  await refresh()
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
})
</script>
