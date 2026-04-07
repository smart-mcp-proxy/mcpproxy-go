<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex items-center gap-4">
      <router-link to="/security" class="btn btn-ghost btn-sm gap-1">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
        </svg>
        Security
      </router-link>
      <div class="flex-1">
        <h1 class="text-3xl font-bold">Scan Report</h1>
        <div v-if="report" class="flex items-center gap-2 mt-1">
          <router-link
            :to="{ name: 'server-detail', params: { serverName: report.server_name } }"
            class="link link-primary text-sm"
          >{{ report.server_name }}</router-link>
          <span v-if="report.risk_score !== undefined"
            class="badge"
            :class="riskScoreClass"
          >Risk: {{ report.risk_score }}/100</span>
        </div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading scan report...</p>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error">
      <div>
        <h3 class="font-bold">Error</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="loadReport" class="btn btn-sm">Retry</button>
    </div>

    <template v-else-if="report">
      <!-- Metadata Card -->
      <div class="card bg-base-100 shadow-xl">
        <div class="card-body">
          <h2 class="card-title text-lg">Scan Metadata</h2>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mt-2">
            <div>
              <div class="text-xs text-base-content/50">Scan ID</div>
              <code class="font-mono text-sm select-all break-all">{{ report.job_id }}</code>
            </div>
            <div>
              <div class="text-xs text-base-content/50">Status</div>
              <span class="badge badge-sm" :class="statusBadgeClass">{{ reportStatus }}</span>
            </div>
            <div>
              <div class="text-xs text-base-content/50">Scanned At</div>
              <span class="text-sm">{{ formatDate(report.scanned_at) }}</span>
            </div>
            <div>
              <div class="text-xs text-base-content/50">Scanners</div>
              <span class="text-sm">{{ report.scanners_run ?? 0 }} run, {{ report.scanners_failed ?? 0 }} failed, {{ report.scanners_total ?? 0 }} total</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Scan Context Card -->
      <div v-if="scanContext" class="card bg-base-100 shadow-xl">
        <div class="card-body">
          <h2 class="card-title text-lg">Scan Context</h2>
          <div class="flex flex-wrap gap-2 mt-2">
            <span v-if="scanContext.source_method" class="badge badge-outline badge-sm">
              Source: {{ scanContext.source_method }}
            </span>
            <span v-if="scanContext.docker_isolation" class="badge badge-info badge-sm">
              Docker Isolated
            </span>
            <span v-if="!scanContext.docker_isolation" class="badge badge-warning badge-sm">
              Local (no Docker)
            </span>
            <span v-if="scanContext.server_protocol" class="badge badge-outline badge-sm">
              Protocol: {{ scanContext.server_protocol }}
            </span>
            <span v-if="scanContext.total_files" class="badge badge-outline badge-sm">
              {{ scanContext.total_files }} files
            </span>
            <span v-if="scanContext.container_image" class="badge badge-ghost badge-sm font-mono">
              {{ scanContext.container_image }}
            </span>
          </div>
        </div>
      </div>

      <!-- Threat Summary Stats -->
      <div class="flex flex-wrap gap-3">
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Dangerous</div>
            <div class="stat-value text-lg text-error">{{ report.summary?.dangerous ?? 0 }}</div>
          </div>
        </div>
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Warnings</div>
            <div class="stat-value text-lg text-warning">{{ report.summary?.warnings ?? 0 }}</div>
          </div>
        </div>
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Info</div>
            <div class="stat-value text-lg text-info">{{ report.summary?.info_level ?? 0 }}</div>
          </div>
        </div>
        <div class="stats shadow bg-base-100">
          <div class="stat py-3 px-4">
            <div class="stat-title text-xs">Total</div>
            <div class="stat-value text-lg">{{ report.summary?.total ?? 0 }}</div>
          </div>
        </div>
      </div>

      <!-- Scan incomplete warnings -->
      <div v-if="report.scan_complete === false && report.empty_scan" class="alert alert-warning">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
        </svg>
        <div>
          <div class="font-semibold">No Files Scanned</div>
          <span>Scanners ran but found no files to analyze. The server may have been disconnected during source extraction.</span>
        </div>
      </div>
      <div v-else-if="report.scan_complete === false" class="alert alert-error">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <div>
          <div class="font-semibold">Scan Incomplete</div>
          <span>{{ report.scanners_failed ?? 0 }} of {{ report.scanners_total ?? 0 }} scanner(s) failed. Check scanner logs for details.</span>
        </div>
      </div>

      <!-- Clean state: no findings -->
      <div v-else-if="!report.findings || report.findings.length === 0" class="alert alert-success">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
        </svg>
        <span>No security issues detected. This server appears to be safe.</span>
      </div>

      <!-- Findings grouped by threat type -->
      <div v-else class="space-y-4">
        <h3 class="text-lg font-semibold">Findings</h3>

        <div v-for="group in groupedFindings" :key="group.type"
          class="collapse collapse-arrow bg-base-100 shadow-md"
          :class="{ 'collapse-open': group.defaultOpen }"
        >
          <input type="checkbox" :checked="group.defaultOpen" />
          <div class="collapse-title font-medium flex items-center gap-2">
            <span>{{ group.label }}</span>
            <span class="badge badge-sm" :class="group.badgeClass">{{ group.findings.length }}</span>
          </div>
          <div class="collapse-content">
            <div class="space-y-2">
              <div v-for="(finding, idx) in group.findings" :key="idx"
                class="collapse collapse-arrow bg-base-200 rounded-lg"
              >
                <input type="checkbox" />
                <div class="collapse-title py-2 px-4 min-h-0 flex items-center gap-3">
                  <span
                    class="badge badge-sm flex-shrink-0"
                    :class="{
                      'badge-error': finding.threat_level === 'dangerous',
                      'badge-warning': finding.threat_level === 'warning',
                      'badge-info': finding.threat_level === 'info',
                    }"
                  >
                    {{ finding.threat_level }}
                  </span>
                  <span class="font-medium text-sm flex-1">
                    {{ finding.rule_id || finding.title }}
                  </span>
                  <span v-if="finding.package_name" class="font-mono text-xs text-base-content/50">
                    {{ finding.package_name }}
                  </span>
                  <span v-if="finding.fixed_version" class="badge badge-xs badge-success badge-outline">
                    fix: {{ finding.fixed_version }}
                  </span>
                </div>
                <div class="collapse-content px-4 pb-3">
                  <div class="space-y-2 text-sm">
                    <p class="text-base-content/80">{{ finding.description }}</p>
                    <!-- Evidence -->
                    <div v-if="finding.evidence" class="mt-2">
                      <div class="text-xs text-base-content/50 mb-1">Triggering content:</div>
                      <pre class="bg-base-300 text-xs p-3 rounded-lg max-h-32 overflow-auto whitespace-pre-wrap break-words border border-base-content/10">{{ finding.evidence }}</pre>
                    </div>
                    <div class="grid grid-cols-2 gap-2 text-xs">
                      <div v-if="finding.rule_id">
                        <span class="text-base-content/50">Rule:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.rule_id }}</code>
                      </div>
                      <div v-if="finding.severity">
                        <span class="text-base-content/50">CVSS Severity:</span>
                        <span class="ml-1 font-medium">{{ finding.severity }}</span>
                        <span v-if="finding.cvss_score" class="ml-1">({{ finding.cvss_score }})</span>
                      </div>
                      <div v-if="finding.package_name">
                        <span class="text-base-content/50">Package:</span>
                        <span class="ml-1 font-mono">{{ finding.package_name }}</span>
                        <span v-if="finding.installed_version" class="ml-1 text-base-content/50">v{{ finding.installed_version }}</span>
                      </div>
                      <div v-if="finding.fixed_version">
                        <span class="text-base-content/50">Fixed in:</span>
                        <span class="ml-1 font-mono text-success">{{ finding.fixed_version }}</span>
                      </div>
                      <div v-if="finding.location">
                        <span class="text-base-content/50">Location:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.location }}</code>
                      </div>
                      <div v-if="finding.scanner">
                        <span class="text-base-content/50">Scanner:</span>
                        <span class="ml-1">{{ scannerDisplayName(finding.scanner) }}</span>
                      </div>
                    </div>
                    <a
                      v-if="finding.help_uri"
                      :href="finding.help_uri"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="link link-primary text-xs inline-flex items-center gap-1"
                    >
                      View Advisory Details &rarr;
                    </a>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Supply Chain Audit (Pass 2) -->
      <div v-if="report.pass2_running" class="alert alert-info">
        <span class="loading loading-spinner loading-sm"></span>
        <div>
          <h3 class="font-bold">Supply Chain Audit</h3>
          <p class="text-sm">Deep dependency analysis running in background. Results will appear here when complete.</p>
        </div>
      </div>
      <div v-else-if="report.pass2_complete && pass2Findings.length > 0" class="space-y-4">
        <div class="collapse collapse-arrow bg-base-100 shadow-md">
          <input type="checkbox" />
          <div class="collapse-title font-medium flex items-center gap-2">
            <span>Supply Chain Audit (CVEs)</span>
            <span class="badge badge-sm" :class="pass2HasDangerous ? 'badge-error' : pass2HasWarnings ? 'badge-warning' : 'badge-info'">{{ pass2Findings.length }}</span>
          </div>
          <div class="collapse-content">
            <div class="space-y-2">
              <div v-for="(finding, idx) in pass2Findings" :key="'p2-' + idx"
                class="collapse collapse-arrow bg-base-200 rounded-lg"
              >
                <input type="checkbox" />
                <div class="collapse-title py-2 px-4 min-h-0 flex items-center gap-3">
                  <span
                    class="badge badge-sm flex-shrink-0"
                    :class="{
                      'badge-error': finding.threat_level === 'dangerous',
                      'badge-warning': finding.threat_level === 'warning',
                      'badge-info': finding.threat_level === 'info',
                    }"
                  >
                    {{ finding.threat_level }}
                  </span>
                  <span class="font-medium text-sm flex-1">
                    {{ finding.rule_id || finding.title }}
                  </span>
                  <span v-if="finding.package_name" class="font-mono text-xs text-base-content/50">
                    {{ finding.package_name }}
                  </span>
                  <span v-if="finding.fixed_version" class="badge badge-xs badge-success badge-outline">
                    fix: {{ finding.fixed_version }}
                  </span>
                </div>
                <div class="collapse-content px-4 pb-3">
                  <div class="space-y-2 text-sm">
                    <p class="text-base-content/80">{{ finding.description }}</p>
                    <div v-if="finding.evidence" class="mt-2">
                      <div class="text-xs text-base-content/50 mb-1">Triggering content:</div>
                      <pre class="bg-base-300 text-xs p-3 rounded-lg max-h-32 overflow-auto whitespace-pre-wrap break-words border border-base-content/10">{{ finding.evidence }}</pre>
                    </div>
                    <div class="grid grid-cols-2 gap-2 text-xs">
                      <div v-if="finding.rule_id">
                        <span class="text-base-content/50">Rule:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.rule_id }}</code>
                      </div>
                      <div v-if="finding.severity">
                        <span class="text-base-content/50">CVSS Severity:</span>
                        <span class="ml-1 font-medium">{{ finding.severity }}</span>
                        <span v-if="finding.cvss_score" class="ml-1">({{ finding.cvss_score }})</span>
                      </div>
                      <div v-if="finding.package_name">
                        <span class="text-base-content/50">Package:</span>
                        <span class="ml-1 font-mono">{{ finding.package_name }}</span>
                        <span v-if="finding.installed_version" class="ml-1 text-base-content/50">v{{ finding.installed_version }}</span>
                      </div>
                      <div v-if="finding.fixed_version">
                        <span class="text-base-content/50">Fixed in:</span>
                        <span class="ml-1 font-mono text-success">{{ finding.fixed_version }}</span>
                      </div>
                      <div v-if="finding.location">
                        <span class="text-base-content/50">Location:</span>
                        <code class="ml-1 bg-base-300 px-1 rounded">{{ finding.location }}</code>
                      </div>
                      <div v-if="finding.scanner">
                        <span class="text-base-content/50">Scanner:</span>
                        <span class="ml-1">{{ scannerDisplayName(finding.scanner) }}</span>
                      </div>
                    </div>
                    <a
                      v-if="finding.help_uri"
                      :href="finding.help_uri"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="link link-primary text-xs inline-flex items-center gap-1"
                    >
                      View Advisory Details &rarr;
                    </a>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <div v-else-if="report.pass2_complete && pass2Findings.length === 0"
        class="alert"
        :class="pass1SupplyChainCount > 0 ? 'alert-info' : 'alert-success'"
      >
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
        </svg>
        <span v-if="pass1SupplyChainCount > 0">Supply chain audit complete. No additional CVEs beyond the {{ pass1SupplyChainCount }} already reported above.</span>
        <span v-else>Supply chain audit complete. No CVEs found in dependencies.</span>
      </div>

      <!-- Scanner Execution Logs -->
      <div v-if="report.scanner_statuses && report.scanner_statuses.length > 0" class="collapse collapse-arrow bg-base-100 shadow-md">
        <input type="checkbox" />
        <div class="collapse-title font-medium">
          Scanner Execution Logs
          <span class="badge badge-sm badge-ghost ml-2">{{ report.scanner_statuses.length }} scanners</span>
        </div>
        <div class="collapse-content">
          <div class="space-y-4">
            <div v-for="ss in report.scanner_statuses" :key="ss.scanner_id" class="border border-base-300 rounded-lg p-3">
              <div class="flex items-center justify-between mb-2">
                <span class="font-medium">{{ scannerDisplayName(ss.scanner_id) }}</span>
                <div class="flex items-center gap-2">
                  <span class="badge badge-sm" :class="{
                    'badge-success': ss.status === 'completed',
                    'badge-error': ss.status === 'failed',
                    'badge-info': ss.status === 'running',
                    'badge-ghost': !ss.status,
                  }">{{ ss.status || 'unknown' }}</span>
                  <span v-if="ss.findings_count" class="text-xs text-base-content/60">{{ ss.findings_count }} findings</span>
                  <span v-if="ss.exit_code !== undefined && ss.exit_code !== 0" class="text-xs text-error">exit {{ ss.exit_code }}</span>
                </div>
              </div>
              <div v-if="ss.error" class="text-sm text-error mb-2">{{ ss.error }}</div>
              <div v-if="ss.stdout" class="mb-2">
                <div class="text-xs text-base-content/50 mb-1">stdout</div>
                <pre class="bg-base-200 text-xs p-3 rounded-lg max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ ss.stdout }}</pre>
              </div>
              <div v-if="ss.stderr">
                <div class="text-xs text-base-content/50 mb-1">stderr</div>
                <pre class="bg-base-200 text-xs p-3 rounded-lg max-h-48 overflow-auto whitespace-pre-wrap break-words text-warning">{{ ss.stderr }}</pre>
              </div>
              <div v-if="!ss.stdout && !ss.stderr && !ss.error" class="text-xs text-base-content/40 italic">No output captured</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Server Status & Actions -->
      <div class="card bg-base-100 shadow-xl">
        <div class="card-body py-4">
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-3">
              <span class="text-sm text-base-content/60">Server Status:</span>
              <span v-if="serverStatus === 'loading'" class="loading loading-spinner loading-xs"></span>
              <span v-else class="badge" :class="{
                'badge-success': serverAdminState === 'enabled',
                'badge-warning': serverAdminState === 'disabled',
                'badge-error': serverAdminState === 'quarantined',
              }">{{ serverAdminState }}</span>
            </div>
            <div class="flex gap-2">
              <button
                v-if="serverAdminState === 'enabled' && report.summary?.dangerous > 0"
                @click="quarantineServer"
                :disabled="actionLoading"
                class="btn btn-error btn-sm"
              >
                <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                Quarantine Server
              </button>
              <button
                v-if="serverAdminState === 'quarantined'"
                @click="unquarantineServer"
                :disabled="actionLoading"
                class="btn btn-success btn-sm"
              >
                <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                Unquarantine Server
              </button>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'
import type { SecurityScanFinding, ThreatType } from '@/types/api'

const props = defineProps<{
  jobId: string
}>()

const loading = ref(false)
const error = ref('')
const report = ref<any>(null)
const actionLoading = ref(false)
const serverStatus = ref<'loading' | 'loaded'>('loading')
const serverAdminState = ref('unknown')

const scannerNames: Record<string, string> = {
  'mcp-ai-scanner': 'MCP AI Scanner',
  'trivy': 'Trivy',
  'cisco-mcp-scanner': 'Cisco MCP Scanner',
  'mcp-scan': 'MCP Scan (Invariant)',
}

function scannerDisplayName(id: string): string {
  return scannerNames[id] || id
}

// Scan context from the aggregated report (populated from job's ScanContext)
const scanContext = computed(() => {
  return report.value?.scan_context || null
})

// Status display
const reportStatus = computed(() => {
  if (!report.value) return 'unknown'
  if (report.value.scan_complete === false) return 'incomplete'
  if (report.value.empty_scan) return 'empty'
  if (!report.value.findings || report.value.findings.length === 0) return 'clean'
  if (report.value.summary?.dangerous > 0) return 'dangerous'
  if (report.value.summary?.warnings > 0) return 'warnings'
  return 'clean'
})

const statusBadgeClass = computed(() => {
  switch (reportStatus.value) {
    case 'dangerous': return 'badge-error'
    case 'warnings': return 'badge-warning'
    case 'incomplete': return 'badge-error'
    case 'empty': return 'badge-warning'
    case 'clean': return 'badge-success'
    default: return 'badge-ghost'
  }
})

const riskScoreClass = computed(() => {
  const score = report.value?.risk_score ?? 0
  if (score >= 70) return 'badge-error'
  if (score >= 30) return 'badge-warning'
  return 'badge-success'
})

// Threat type grouping (same logic as ServerDetail.vue)
const threatTypeLabels: Record<ThreatType, string> = {
  tool_poisoning: 'Tool Poisoning',
  prompt_injection: 'Prompt Injection',
  rug_pull: 'Rug Pull Detection',
  supply_chain: 'Supply Chain (CVEs)',
  malicious_code: 'Malicious Code',
}

const dangerousTypes: ThreatType[] = ['tool_poisoning', 'prompt_injection', 'rug_pull', 'malicious_code']

interface FindingGroup {
  type: ThreatType
  label: string
  findings: SecurityScanFinding[]
  defaultOpen: boolean
  badgeClass: string
}

const groupedFindings = computed<FindingGroup[]>(() => {
  if (!report.value?.findings) return []

  const pass1Findings = report.value.findings.filter(
    (f: SecurityScanFinding) => !f.scan_pass || f.scan_pass === 1
  )

  const groups = new Map<ThreatType, SecurityScanFinding[]>()
  for (const f of pass1Findings) {
    const type = (f.threat_type || 'supply_chain') as ThreatType
    if (!groups.has(type)) groups.set(type, [])
    groups.get(type)!.push(f)
  }

  const result: FindingGroup[] = []
  const typeOrder: ThreatType[] = ['tool_poisoning', 'prompt_injection', 'rug_pull', 'malicious_code', 'supply_chain']
  for (const type of typeOrder) {
    const findings = groups.get(type)
    if (!findings) continue
    const hasDangerous = findings.some(f => f.threat_level === 'dangerous')
    result.push({
      type,
      label: threatTypeLabels[type] || type,
      findings,
      defaultOpen: dangerousTypes.includes(type),
      badgeClass: hasDangerous ? 'badge-error' : findings.some(f => f.threat_level === 'warning') ? 'badge-warning' : 'badge-info',
    })
  }
  return result
})

const pass1SupplyChainCount = computed(() => {
  if (!report.value?.findings) return 0
  return report.value.findings.filter(
    (f: SecurityScanFinding) => (!f.scan_pass || f.scan_pass === 1) && f.threat_type === 'supply_chain'
  ).length
})

const pass2Findings = computed<SecurityScanFinding[]>(() => {
  if (!report.value?.findings) return []
  return report.value.findings.filter((f: SecurityScanFinding) => f.scan_pass === 2)
})

const pass2HasDangerous = computed(() => {
  return pass2Findings.value.some(f => f.threat_level === 'dangerous')
})

const pass2HasWarnings = computed(() => {
  return pass2Findings.value.some(f => f.threat_level === 'warning')
})

function formatDate(dateStr: string): string {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  return d.toLocaleString()
}

async function loadReport() {
  loading.value = true
  error.value = ''
  try {
    const res = await api.getScanReportByJobId(props.jobId)
    if (res.success && res.data) {
      report.value = res.data
    } else {
      error.value = res.error || 'Failed to load scan report'
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function loadServerStatus() {
  if (!report.value?.server_name) return
  serverStatus.value = 'loading'
  try {
    const res = await api.getServers()
    if (res.success && res.data?.servers) {
      const server = res.data.servers.find((s: any) => s.name === report.value.server_name)
      if (server?.health?.admin_state) {
        serverAdminState.value = server.health.admin_state
      } else {
        serverAdminState.value = 'unknown'
      }
    }
  } catch {
    serverAdminState.value = 'unknown'
  } finally {
    serverStatus.value = 'loaded'
  }
}

async function quarantineServer() {
  if (!report.value?.server_name) return
  if (!confirm(`Quarantine ${report.value.server_name}? This will disconnect the server.`)) return
  actionLoading.value = true
  try {
    await api.quarantineServer(report.value.server_name)
    await loadServerStatus()
  } finally {
    actionLoading.value = false
  }
}

async function unquarantineServer() {
  if (!report.value?.server_name) return
  if (!confirm(`Unquarantine ${report.value.server_name}? This will re-enable the server.`)) return
  actionLoading.value = true
  try {
    await api.unquarantineServer(report.value.server_name)
    await loadServerStatus()
  } finally {
    actionLoading.value = false
  }
}

onMounted(async () => {
  await loadReport()
  await loadServerStatus()
})
</script>
