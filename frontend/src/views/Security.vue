<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Security</h1>
        <p class="text-base-content/70 mt-1">Configure security scanner plugins and review scan results</p>
      </div>
      <div class="flex gap-2">
        <div
          v-if="(overview?.scanners_enabled ?? overview?.scanners_installed ?? 0) > 0"
          class="tooltip"
          :data-tip="!overview?.docker_available ? 'Docker is required to run security scanners' : ''"
        >
          <button @click="startScanAll" :disabled="loading || scanAllRunning || !overview?.docker_available" class="btn btn-primary">
            <span v-if="scanAllRunning" class="loading loading-spinner loading-sm"></span>
            {{ scanAllRunning ? 'Scanning...' : 'Scan All Servers' }}
          </button>
        </div>
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

    <!-- Docker unavailable warning (only after overview has loaded) -->
    <div v-if="overviewLoaded && overview.docker_available === false" class="alert alert-warning">
      <svg class="w-5 h-5 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
      </svg>
      <span>Docker is not running. Security scanners require Docker to analyze MCP servers.</span>
    </div>

    <!-- Docker isolation nudge: show only when Docker is available, global
         isolation is OFF, user has at least one stdio server, and the user
         hasn't dismissed this banner before. -->
    <div v-if="showIsolationNudge" class="alert alert-info">
      <svg class="w-5 h-5 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div class="flex-1">
        <div class="font-bold">Docker isolation is off.</div>
        <div class="text-sm">
          You have {{ stdioServerCount }} stdio server{{ stdioServerCount === 1 ? '' : 's' }} running directly on your host. Enable Docker isolation to sandbox them.
        </div>
      </div>
      <div class="flex gap-2">
        <button @click="enableIsolationFromNudge" :disabled="togglingIsolation" class="btn btn-sm btn-primary">
          <span v-if="togglingIsolation" class="loading loading-spinner loading-xs"></span>
          Enable
        </button>
        <a :href="DOCKER_ISOLATION_DOCS_URL" target="_blank" rel="noopener noreferrer" class="btn btn-sm btn-ghost">Learn more</a>
        <button @click="dismissIsolationNudge" class="btn btn-sm btn-ghost">Dismiss</button>
      </div>
    </div>

    <!-- Docker isolation toggle card: shown whenever Docker is available.
         We don't show it when Docker is missing because the existing
         "Docker not running" alert above already covers that case. -->
    <div v-if="overviewLoaded && overview.docker_available === true" class="card bg-base-100 shadow-xl">
      <div class="card-body flex-row justify-between items-center gap-4 flex-wrap">
        <div class="flex-1 min-w-64">
          <h2 class="card-title text-lg">Docker Isolation</h2>
          <p class="text-sm text-base-content/70">
            Wrap each stdio MCP server in its own Docker container so it can't touch the host filesystem.
          </p>
          <p class="text-xs text-base-content/50 mt-1">
            Currently isolated: {{ isolatedServerCount }} of {{ stdioServerCount }} stdio server{{ stdioServerCount === 1 ? '' : 's' }}.
            <a :href="DOCKER_ISOLATION_DOCS_URL" target="_blank" rel="noopener noreferrer" class="link link-hover">
              Learn more
            </a>
          </p>
        </div>
        <div class="flex items-center gap-3">
          <span v-if="togglingIsolation" class="loading loading-spinner loading-sm"></span>
          <span class="text-sm text-base-content/60">{{ isolationEnabled ? 'On' : 'Off' }}</span>
          <input
            type="checkbox"
            class="toggle toggle-primary"
            :checked="isolationEnabled"
            :disabled="togglingIsolation"
            @change="onIsolationToggle"
          />
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
          <p class="text-sm text-base-content/70 mb-4">Each scanner is a third-party security tool that runs inside an isolated Docker container — sandboxed from your host, with no access to your filesystem or network beyond what the scan requires. Enable or disable individual scanners and configure their API keys below.</p>

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
                  <td>
                    <a v-if="scanner.homepage" :href="scanner.homepage" target="_blank" rel="noopener noreferrer" class="link link-primary">
                      {{ scanner.vendor }}
                    </a>
                    <span v-else>{{ scanner.vendor }}</span>
                  </td>
                  <td>
                    <div class="flex flex-wrap gap-1">
                      <span v-for="input in scanner.inputs" :key="input" class="badge badge-sm badge-outline">{{ input }}</span>
                    </div>
                  </td>
                  <td>
                    <div class="flex flex-col gap-1">
                      <span class="badge badge-sm gap-1" :class="statusBadgeClass(scanner.status)">
                        <span v-if="scanner.status === 'pulling'" class="loading loading-spinner loading-xs"></span>
                        {{ scannerDisplayStatus(scanner.status) }}
                      </span>
                      <!-- Error details -->
                      <span v-if="scanner.status === 'error' && scanner.error_message" class="text-xs text-error max-w-xs" :title="scanner.error_message">
                        {{ scanner.error_message }}
                      </span>
                      <!-- Pulling hint with image name -->
                      <span v-else-if="scanner.status === 'pulling'" class="text-xs text-info max-w-xs truncate font-mono" :title="scanner.image_override || scanner.docker_image">
                        pulling {{ scanner.image_override || scanner.docker_image }}
                      </span>
                      <!-- Missing required API key hint -->
                      <span v-else-if="scannerNeedsApiKey(scanner)" class="text-xs text-warning">
                        API key required
                      </span>
                    </div>
                  </td>
                  <td>
                    <div class="flex gap-2 items-center">
                      <input
                        type="checkbox"
                        class="toggle toggle-sm toggle-primary"
                        :checked="scanner.status !== 'available' && scanner.status !== 'error'"
                        :disabled="installing === scanner.id || scanner.status === 'pulling'"
                        @change="toggleScanner(scanner)"
                      />
                      <span v-if="installing === scanner.id" class="loading loading-spinner loading-xs"></span>
                      <button
                        v-if="scanner.status === 'error'"
                        @click="retryScanner(scanner)"
                        :disabled="installing === scanner.id"
                        class="btn btn-sm btn-error btn-outline"
                      >
                        Retry
                      </button>
                      <button
                        v-else-if="scannerNeedsApiKey(scanner)"
                        @click="openConfigDialog(scanner)"
                        :disabled="scanner.status === 'pulling'"
                        class="btn btn-sm btn-warning btn-outline"
                      >
                        Set API Key
                      </button>
                      <button
                        v-else
                        @click="openConfigDialog(scanner)"
                        :disabled="scanner.status === 'pulling'"
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
          <div>
            <h2 class="card-title">Scan History</h2>
            <p class="text-sm text-base-content/70 mb-4">All security scan results across servers</p>
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
                  <th class="cursor-pointer select-none" @click="toggleSort('server_name')">
                    Server {{ sortIndicator('server_name') }}
                  </th>
                  <th class="cursor-pointer select-none" @click="toggleSort('started_at')">
                    Date {{ sortIndicator('started_at') }}
                  </th>
                  <th class="cursor-pointer select-none" @click="toggleSort('status')">
                    Status {{ sortIndicator('status') }}
                  </th>
                  <th class="cursor-pointer select-none" @click="toggleSort('findings_count')">
                    Findings {{ sortIndicator('findings_count') }}
                  </th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="scan in scanHistory" :key="scan.id">
                  <td>
                    <router-link :to="`/servers/${encodeURIComponent(scan.server_name)}`" class="link link-primary font-medium">
                      {{ scan.server_name }}
                    </router-link>
                    <div v-if="scan.pass === 2" class="text-xs text-base-content/50">(Pass 2)</div>
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
                    <router-link
                      v-if="scan.status === 'completed'"
                      :to="`/security/scans/${encodeURIComponent(scan.id)}`"
                      class="link link-primary text-sm whitespace-nowrap"
                    >
                      Details →
                    </router-link>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- Pagination -->
          <div v-if="historyTotalPages > 1" class="flex justify-between items-center mt-4 pt-4 border-t border-base-300">
            <div class="text-sm text-base-content/60">
              Showing {{ (historyPage - 1) * HISTORY_PAGE_SIZE + 1 }}-{{ Math.min(historyPage * HISTORY_PAGE_SIZE, historyTotal) }} of {{ historyTotal }}
            </div>
            <div class="join">
              <button @click="historyPage = 1" :disabled="historyPage === 1" class="join-item btn btn-sm">&#xAB;</button>
              <button @click="historyPage = Math.max(1, historyPage - 1)" :disabled="historyPage === 1" class="join-item btn btn-sm">&#x2039;</button>
              <button class="join-item btn btn-sm">{{ historyPage }} / {{ historyTotalPages }}</button>
              <button @click="historyPage = Math.min(historyTotalPages, historyPage + 1)" :disabled="historyPage === historyTotalPages" class="join-item btn btn-sm">&#x203A;</button>
              <button @click="historyPage = historyTotalPages" :disabled="historyPage === historyTotalPages" class="join-item btn btn-sm">&#xBB;</button>
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
          <!-- Docker image override -->
          <div class="divider text-xs">Docker Image</div>
          <div class="form-control">
            <label class="label">
              <span class="label-text">Docker Image</span>
              <span class="badge badge-sm badge-ghost">Optional</span>
            </label>
            <input
              v-model="configDockerImage"
              type="text"
              :placeholder="configScanner?.image_override || configScanner?.docker_image || 'default image'"
              class="input input-bordered input-sm font-mono"
            />
            <label class="label" v-if="configScanner?.docker_image">
              <span class="label-text-alt text-base-content/40">Default: {{ configScanner.docker_image }}</span>
            </label>
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
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import api from '@/services/api'
import { refreshSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()

// Link to the canonical docs. Using the main-branch blob URL keeps this
// working for anyone reading the shipped UI — the in-app router has no
// route for arbitrary markdown docs, and we want to avoid hardcoding a
// version-specific URL that would go stale on the next release.
const DOCKER_ISOLATION_DOCS_URL = 'https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/docker-isolation.md'

// Nudge dismissal is intentionally stored client-side in localStorage (not
// in the server config) — it's a UI preference, not a policy setting, and
// it's fine if it resets when the user clears browser data. Matches the
// guidance in the spec to avoid inventing a new config-file schema.
const NUDGE_DISMISSED_KEY = 'mcpproxy.dockerIsolationNudgeDismissed'

const loading = ref(false)
const error = ref('')
const scanners = ref<any[]>([])
const overview = ref<any>({})
// overviewLoaded stays false until the first successful /security/overview
// response arrives. Template guards like the "Docker not running" banner use
// this to avoid flashing a false warning while the initial request is still
// in flight (overview.docker_available is undefined before the fetch lands).
const overviewLoaded = ref(false)
const installing = ref<string | null>(null)

// Scan history state
const scanHistory = ref<any[]>([])
const historyLoading = ref(false)
const historySort = ref('started_at')
const historyOrder = ref('desc')
const historyTotal = ref(0)
const historyPage = ref(1)
const HISTORY_PAGE_SIZE = 20
const historyTotalPages = computed(() => Math.max(1, Math.ceil(historyTotal.value / HISTORY_PAGE_SIZE)))

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
const configDockerImage = ref('')

const totalFindings = computed(() => overview.value?.findings_by_severity?.total || 0)

// Docker isolation state — populated from /api/v1/config + /api/v1/servers.
const isolationEnabled = ref(false)
const stdioServerCount = ref(0)
const isolatedServerCount = ref(0)
const togglingIsolation = ref(false)
const nudgeDismissed = ref<boolean>(
  // Read once synchronously on setup — template bindings are reactive via
  // dismissIsolationNudge() flipping the ref.
  typeof window !== 'undefined' && window.localStorage?.getItem(NUDGE_DISMISSED_KEY) === '1'
)

// showIsolationNudge is the conjunction the spec calls for: Docker present,
// isolation off, ≥1 stdio server, not dismissed. We gate on overviewLoaded
// so the banner doesn't flash during the initial fetch.
const showIsolationNudge = computed(() =>
  overviewLoaded.value &&
  overview.value?.docker_available === true &&
  !isolationEnabled.value &&
  stdioServerCount.value > 0 &&
  !nudgeDismissed.value
)

async function loadIsolationState() {
  try {
    const [cfgRes, serversRes] = await Promise.all([
      api.getConfig(),
      api.getServers(),
    ])
    if (cfgRes.success && cfgRes.data) {
      // config is typed as `any` server-side; docker_isolation.enabled may
      // be absent for very old configs, in which case we treat it as off.
      isolationEnabled.value = Boolean(cfgRes.data.config?.docker_isolation?.enabled)
    }
    if (serversRes.success && serversRes.data?.servers) {
      const servers = serversRes.data.servers
      const stdio = servers.filter((s: any) => s.protocol === 'stdio')
      stdioServerCount.value = stdio.length
      // "Isolated" means the server is stdio AND global isolation is on AND
      // the per-server opt-out (isolation.enabled=false) is not set. We
      // can't see the per-server isolation block from the Server endpoint
      // today, so this is a best-effort count that matches the common
      // case: global-on implies all stdio servers are isolated.
      isolatedServerCount.value = isolationEnabled.value ? stdio.length : 0
    }
  } catch {
    // Non-fatal: the banner just won't show. Page's main refresh() already
    // reports errors on the actual scanner endpoints.
  }
}

async function setDockerIsolation(enabled: boolean) {
  togglingIsolation.value = true
  try {
    const res = await api.setDockerIsolationEnabled(enabled)
    if (!res.success) {
      systemStore.addToast({
        type: 'error',
        title: 'Failed to update Docker isolation',
        message: res.error || 'Unknown error',
      })
      return false
    }
    isolationEnabled.value = enabled
    // Refresh the isolated count immediately so the toggle card reflects
    // the new state without waiting for the next full page refresh.
    isolatedServerCount.value = enabled ? stdioServerCount.value : 0
    systemStore.addToast({
      type: 'success',
      title: enabled ? 'Docker isolation enabled' : 'Docker isolation disabled',
      message: enabled
        ? 'New connections will isolate immediately. Existing connections will isolate after restart.'
        : 'Servers will run directly on the host.',
    })
    return true
  } catch (e: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Failed to update Docker isolation',
      message: e?.message || String(e),
    })
    return false
  } finally {
    togglingIsolation.value = false
  }
}

async function onIsolationToggle(event: Event) {
  const target = event.target as HTMLInputElement
  await setDockerIsolation(target.checked)
}

async function enableIsolationFromNudge() {
  const ok = await setDockerIsolation(true)
  if (ok) {
    // The nudge disappears automatically (isolationEnabled is now true),
    // but also set the dismissed flag so a later disable doesn't resurrect
    // the banner uninvited.
    dismissIsolationNudge()
  }
}

function dismissIsolationNudge() {
  nudgeDismissed.value = true
  try {
    window.localStorage?.setItem(NUDGE_DISMISSED_KEY, '1')
  } catch {
    // localStorage can throw in private-mode Safari — safe to ignore,
    // banner will simply reappear next session.
  }
}

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
    case 'installed': return 'badge-success'
    case 'pulling': return 'badge-info'
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
    case 'pulling':
      return 'pulling image…'
    case 'available':
      return 'disabled'
    case 'error':
      return 'failed'
    default:
      return status
  }
}

function scannerNeedsApiKey(scanner: any): boolean {
  if (scanner.status !== 'installed' && scanner.status !== 'configured') return false
  if (!scanner.required_env?.length) return false
  const configured = scanner.configured_env || {}
  return scanner.required_env.some((env: any) => !configured[env.key])
}

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const [scannersRes, overviewRes] = await Promise.all([
      api.listScanners(),
      api.getSecurityOverview(),
    ])
    if (scannersRes.success) {
      const list = (scannersRes.data || []) as any[]
      // Defensive sort: the backend already returns scanners alphabetically
      // by ID, but sorting here guarantees the UI stays stable even if a
      // caller hits an older backend or a proxy reorders the JSON.
      list.sort((a, b) => String(a.id).localeCompare(String(b.id)))
      scanners.value = list
    }
    if (overviewRes.success) {
      overview.value = overviewRes.data || {}
      overviewLoaded.value = true
    }
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function toggleScanner(scanner: any) {
  installing.value = scanner.id
  try {
    // 'available' → enable. Any other non-error status → disable.
    // 'error' is handled by the dedicated Retry button (retryScanner).
    if (scanner.status === 'available') {
      const res = await api.installScanner(scanner.id)
      if (!res.success) {
        error.value = `Failed to enable: ${res.error}`
      }
    } else if (scanner.status !== 'error') {
      await api.removeScanner(scanner.id)
    }
    await refresh()
    // Refresh shared scanner-status cache so other pages (Servers,
    // ServerDetail) update their scan-button visibility immediately.
    await refreshSecurityScannerStatus()
  } finally {
    installing.value = null
  }
}

// retryScanner re-runs the enable flow for a scanner that previously failed
// (typically because a Docker pull did not succeed). This is bound to the
// "Retry" button that shows up next to error-state scanners.
async function retryScanner(scanner: any) {
  installing.value = scanner.id
  try {
    const res = await api.installScanner(scanner.id)
    if (!res.success) {
      error.value = `Retry failed: ${res.error}`
    }
    await refresh()
    await refreshSecurityScannerStatus()
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
  configDockerImage.value = scanner.image_override || ''
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
  const hasEnv = Object.keys(toSend).length > 0
  const hasImage = configDockerImage.value && configDockerImage.value !== (configScanner.value?.image_override || '')
  if (hasEnv || hasImage) {
    await api.configureScanner(configScanner.value.id, toSend, configDockerImage.value || undefined)
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
  historyPage.value = 1
  loadHistory()
}

function sortIndicator(field: string): string {
  if (historySort.value !== field) return ''
  return historyOrder.value === 'desc' ? '\u25BC' : '\u25B2'
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
    const offset = (historyPage.value - 1) * HISTORY_PAGE_SIZE
    const res = await api.listScanHistory({
      sort: historySort.value,
      order: historyOrder.value,
      limit: HISTORY_PAGE_SIZE,
      offset,
    })
    if (res.success && res.data) {
      scanHistory.value = res.data.scans || []
      historyTotal.value = res.data.total || 0
    }
  } catch {
    // Ignore history load errors
  } finally {
    historyLoading.value = false
  }
}

watch(historyPage, () => loadHistory())

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

// Listener for live scanner status updates (background image pulls).
// The system store forwards security.scanner_changed SSE events as a
// CustomEvent on window so we don't have to wire an EventSource here.
function handleScannerChanged(e: Event) {
  const detail = (e as CustomEvent).detail
  if (!detail) return
  // Apply the update inline so the user sees an instant transition from
  // "pulling…" to "enabled" without waiting for a full refetch.
  const idx = scanners.value.findIndex((s: any) => s.id === detail.scanner_id)
  if (idx >= 0) {
    scanners.value[idx] = {
      ...scanners.value[idx],
      status: detail.status,
      error_message: detail.error || '',
    }
  }
  // Then do a full refresh for overview counters + shared caches.
  refresh()
  refreshSecurityScannerStatus()
}

onMounted(async () => {
  await Promise.all([refresh(), loadHistory(), loadIsolationState()])
  // Subscribe to live scanner updates.
  window.addEventListener('mcpproxy:scanner-changed', handleScannerChanged)
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
  window.removeEventListener('mcpproxy:scanner-changed', handleScannerChanged)
})
</script>
