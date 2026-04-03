<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Security</h1>
        <p class="text-base-content/70 mt-1">Manage security scanners and scan quarantined servers</p>
      </div>
      <button @click="refresh" :disabled="loading" class="btn btn-outline">
        <span v-if="loading" class="loading loading-spinner loading-sm"></span>
        {{ loading ? 'Refreshing...' : 'Refresh' }}
      </button>
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
                        v-if="scanner.status === 'installed' && scanner.required_env?.length"
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
                    <th>Title</th>
                    <th>Location</th>
                    <th>Scanner</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(finding, idx) in scanResult.findings" :key="idx">
                    <td>
                      <span class="badge badge-sm" :class="severityBadgeClass(finding.severity)">{{ finding.severity }}</span>
                    </td>
                    <td>{{ finding.title }}</td>
                    <td class="font-mono text-xs">{{ finding.location || '-' }}</td>
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
      <div class="modal-box">
        <h3 class="font-bold text-lg">Configure {{ configScanner?.name }}</h3>
        <div class="py-4 space-y-4" v-if="configScanner">
          <div v-for="env in configScanner.required_env" :key="env.key" class="form-control">
            <label class="label"><span class="label-text">{{ env.label }}</span></label>
            <input
              v-model="configValues[env.key]"
              :type="env.secret ? 'password' : 'text'"
              :placeholder="env.key"
              class="input input-bordered"
            />
          </div>
          <div v-for="env in (configScanner.optional_env || [])" :key="env.key" class="form-control">
            <label class="label">
              <span class="label-text">{{ env.label }}</span>
              <span class="label-text-alt">Optional</span>
            </label>
            <input
              v-model="configValues[env.key]"
              :type="env.secret ? 'password' : 'text'"
              :placeholder="env.key"
              class="input input-bordered"
            />
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
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'

const loading = ref(false)
const error = ref('')
const scanners = ref<any[]>([])
const overview = ref<any>({})
const installing = ref<string | null>(null)
const scanServerName = ref('')
const scanning = ref(false)
const scanResult = ref<any>(null)

// Config dialog
const configDialog = ref<HTMLDialogElement>()
const configScanner = ref<any>(null)
const configValues = ref<Record<string, string>>({})

const totalFindings = computed(() => overview.value?.findings_by_severity?.total || 0)

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
  configValues.value = {}
  configDialog.value?.showModal()
}

function closeConfigDialog() {
  configDialog.value?.close()
}

async function saveConfig() {
  if (!configScanner.value) return
  const nonEmpty: Record<string, string> = {}
  for (const [k, v] of Object.entries(configValues.value)) {
    if (v) nonEmpty[k] = v
  }
  await api.configureScanner(configScanner.value.id, nonEmpty)
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

onMounted(refresh)
</script>
