<template>
  <div class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow server-card" data-test="server-card">
    <div class="card-body p-4 flex flex-col">
      <!-- Title row: name, transport chip, trust-mode shield, ⋯ menu (Spec 109 FR-013 D15) -->
      <div class="flex items-center gap-1.5 mb-2">
        <h3
          class="card-title text-base truncate flex-1 min-w-0"
          :title="server.name"
          data-test="server-card-title"
        >{{ displayName }}</h3>

        <span class="badge badge-sm badge-ghost shrink-0" data-test="server-card-protocol">{{ server.protocol }}</span>

        <!-- Trust mode at a glance (spec 088 FR-007 / Spec 109 FR-013): a
             shield icon, never a text badge — the tooltip carries the label.
             Always the EFFECTIVE mode; an unrecognized configured value
             renders the fail-closed mode with a subtle marker and the raw
             value in the tooltip instead of being hidden or rewritten.

             Review round 1 (109-e medium finding): the CSS `data-tip`
             tooltip conveys nothing to a screen reader, and the SVG was
             `aria-hidden` with no accessible name outside the invalid-value
             edge case — every other card's trust mode was unannounced. The
             wrapper now carries `role="img"` + `aria-label` unconditionally,
             from the same `trustBadgeTitle` text the tooltip already shows
             (it already includes the "not recognized" detail for the
             invalid case), so the old invalid-only sr-only span is
             redundant and removed. -->
        <div
          :class="['tooltip tooltip-left shrink-0', trustBadgeClass]"
          :data-tip="trustBadgeTitle"
          :data-trust-invalid="trustModeState.isInvalid ? 'true' : undefined"
          role="img"
          :aria-label="trustBadgeTitle"
          data-test="server-trust-mode"
        >
          <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M12 2L3.5 6.5V11c0 5.55 3.84 10.74 8.5 12 4.66-1.26 8.5-6.45 8.5-12V6.5L12 2zm0 2.18l6.5 3.35V11c0 4.52-3.15 8.76-6.5 9.93C8.65 19.76 5.5 15.52 5.5 11V7.53L12 4.18z"/>
          </svg>
        </div>

        <!-- ⋯ menu: every secondary action lives here, never on the card face
             (Spec 109 FR-013). Delete asks for confirmation and is reachable
             only from this menu. -->
        <div class="dropdown dropdown-end shrink-0">
          <button
            tabindex="0"
            type="button"
            class="btn btn-ghost btn-xs px-1.5"
            aria-label="More actions"
            aria-haspopup="true"
            data-test="server-card-menu-trigger"
          >
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
              <circle cx="5" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="19" cy="12" r="2"/>
            </svg>
          </button>
          <ul
            tabindex="0"
            class="dropdown-content menu menu-sm z-10 bg-base-100 rounded-box shadow-lg border border-base-300 w-52 p-1"
            data-test="server-card-menu"
          >
            <!-- Review round 1 (109-e high finding): gated directly on
                 `server.quarantined`, independent of whichever action is
                 primary (mirrors macOS ServersView.swift's
                 contextMenuActions). A quarantined server that ALSO needs
                 OAuth sign-in reports actions=[login, approve] (FR-010);
                 the primary button surfaces only "Sign in", so this is the
                 menu's only path to review when that happens. -->
            <li v-if="server.quarantined">
              <router-link
                :to="serverDetailPath(server.name, 'tools')"
                data-test="server-card-menu-review"
              >Review</router-link>
            </li>
            <li>
              <button type="button" @click="toggleEnabled" :disabled="loading" data-test="server-card-menu-toggle">
                {{ server.enabled ? 'Disable' : 'Enable' }}
              </button>
            </li>
            <li v-if="hasEnabledScanners()">
              <router-link
                v-if="server.enabled"
                :to="serverDetailPath(server.name, 'security')"
                data-test="server-card-menu-scan"
              >Scan</router-link>
              <span
                v-else
                class="disabled opacity-50 tooltip tooltip-right"
                :data-tip="scanDisabledReason"
                :title="scanDisabledReason"
                data-test="server-card-scan-disabled"
              >Scan</span>
            </li>
            <li>
              <button type="button" @click="restart" :disabled="loading" data-test="server-card-menu-restart">Restart</button>
            </li>
            <li>
              <router-link :to="serverDetailPath(server.name, 'logs')" data-test="server-card-menu-logs">Logs</router-link>
            </li>
            <li>
              <router-link :to="serverDetailPath(server.name, 'config')" data-test="server-card-menu-edit">Edit</router-link>
            </li>
            <li>
              <router-link
                :to="serverDetailPath(server.name, 'config')"
                data-test="server-card-menu-trust"
              >Trust mode: {{ trustBadgeLabel }}</router-link>
            </li>
            <li v-if="canLogout">
              <button type="button" @click="triggerLogout" :disabled="loading" data-test="server-card-menu-logout">Logout</button>
            </li>
            <li>
              <button
                type="button"
                class="text-error"
                @click="showDeleteConfirmation = true"
                :disabled="loading"
                data-test="server-card-menu-delete"
              >Delete</button>
            </li>
          </ul>
        </div>
      </div>

      <!-- Status line: ONE line, label + detail, ellipsis with tooltip
           (Spec 109 FR-013/FR-011 — never health.level as text). Folds what
           used to be a separate full-width error alert / quarantine banner
           into this single line; the raw technical chain and the review
           narrative both move to the tooltip and to the detail page. -->
      <div
        class="text-sm mb-2 truncate tooltip tooltip-bottom max-w-full"
        :data-tip="statusTooltip || undefined"
        data-test="server-card-status-line"
      >
        <span :class="['badge badge-sm', statusBadgeClass]" data-test="server-status-chip">{{ statusText }}</span>
        <span v-if="statusDetail" class="text-base-content/60 ml-1"> — {{ statusDetail }}</span>
      </div>

      <!-- Stats line: tools · scan verdict · last call · errors (24h), the
           last two linking to Activity (Spec 109 FR-013). Always rendered so
           every card reserves the same height. -->
      <router-link
        :to="activityLink"
        class="text-xs text-base-content/70 hover:text-base-content mb-2 flex flex-wrap items-center gap-x-1.5 link link-hover"
        data-test="server-card-stats-line"
      >
        <span data-test="server-card-tool-count">{{ server.tool_count }} tools</span>
        <span v-if="securityLine" data-test="server-card-security-line">· <span :class="server.quarantined ? 'text-warning' : securityBadgeColor" :data-test="server.quarantined ? 'server-card-quarantine-scan-note' : 'security-scan-badge'">{{ securityLine }}</span></span>
        <span v-if="!server.quarantined && toolQuarantineSummary" class="text-warning" data-test="server-card-tool-quarantine-note">· {{ toolQuarantineSummary }}</span>
        <span>· last call {{ lastCallText }}</span>
        <span :class="errors24h > 0 ? 'text-error' : ''">· {{ errors24h }} error{{ errors24h === 1 ? '' : 's' }} (24h)</span>
      </router-link>

      <!-- Primary action row: reserves its height even when empty
           (Spec 109 FR-013 — at most ONE primary button, actions[0], none
           for the normal `ready` case). -->
      <div class="card-actions justify-end items-center mt-auto pt-1 min-h-[2.25rem]" data-test="server-card-primary-row">
        <button
          v-if="primaryAction && primaryKind === 'execute'"
          type="button"
          @click="runPrimaryAction"
          :disabled="loading"
          class="btn btn-sm btn-primary"
          data-test="server-card-primary-action"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          {{ primaryLabel }}
        </button>
        <router-link
          v-else-if="primaryAction && primaryKind === 'navigate'"
          :to="primaryHref"
          class="btn btn-sm btn-primary"
          data-test="server-card-primary-action"
        >
          {{ primaryLabel }}
        </router-link>

        <router-link
          :to="serverDetailPath(server.name)"
          class="btn btn-sm btn-outline"
          data-test="server-detail-link"
        >
          Details
        </router-link>
      </div>
    </div>

    <!-- Delete Confirmation Modal — the only place Delete is reachable from
         (Spec 109 FR-013: "Delete only in ⋯ with a confirmation naming the
         server"). -->
    <div v-if="showDeleteConfirmation" class="modal modal-open">
      <div class="modal-box">
        <h3 class="font-bold text-lg mb-4">Delete Server</h3>
        <p class="mb-4">
          Are you sure you want to delete the server <strong>{{ server.name }}</strong>?
        </p>
        <p class="text-sm text-base-content/70 mb-6">
          This action cannot be undone. The server will be removed from your configuration.
        </p>
        <div class="modal-action">
          <button
            @click="showDeleteConfirmation = false"
            :disabled="loading"
            class="btn btn-outline"
          >
            Cancel
          </button>
          <button
            @click="confirmDelete"
            :disabled="loading"
            class="btn btn-error"
            data-test="server-card-delete-confirm"
          >
            <span v-if="loading" class="loading loading-spinner loading-xs"></span>
            Delete Server
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import type { Server, ActivityPerServer } from '@/types'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { useSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'
import { serverDetailPath, serverDisplayName } from '@/utils/serverRoute'
import { oauthSignInState, healthStatusText, healthActionLabel } from '@/utils/health'
import { deriveTrustModeState, TRUST_MODES } from '@/utils/trustMode'

interface Props {
  server: Server
  // One server's slice of the ONE `GET /activity/summary?period=24h` response
  // the parent view fetches per page load (Spec 109 FR-013) — undefined for a
  // server with no call in the period.
  activityStats?: ActivityPerServer
}

const props = defineProps<Props>()

// MCP-1112: title-preferring display label. The '/'-safe detail links call
// serverDetailPath() directly in the template.
const displayName = computed(() => serverDisplayName(props.server))

const serversStore = useServersStore()
const systemStore = useSystemStore()
const { hasEnabledScanners } = useSecurityScannerStatus()
const loading = ref(false)
const showDeleteConfirmation = ref(false)

const isHttpProtocol = computed(() => {
  return props.server.protocol === 'http' || props.server.protocol === 'streamable-http'
})

// MCP-1821 — OAuth sign-in state (null when no sign-in is required). When set,
// the status line reads a calm amber "Sign-in required" instead of red
// "Disconnected"/"Unhealthy", matching the ServerDetail Sign-in CTA.
const signInState = computed(() => oauthSignInState(props.server))

// Trust-mode badge (spec 088 FR-007 / FR-001). Display only — the mode is
// changed from the server detail Configuration tab.
const trustModeState = computed(() => deriveTrustModeState(props.server.trust_mode))

const trustModeMeta = computed(
  () => TRUST_MODES.find(m => m.mode === trustModeState.value.effective) ?? TRUST_MODES[TRUST_MODES.length - 1]
)

const trustBadgeLabel = computed(() => trustModeMeta.value.label)

// Auto is the least-safe mode (unscanned tool changes) and reads amber; scan
// reads informational; manual — the secure default — stays neutral.
const trustBadgeClass = computed(() => {
  if (trustModeState.value.isInvalid) return 'text-warning'
  switch (trustModeState.value.effective) {
    case 'auto':
      return 'text-warning'
    case 'scan':
      return 'text-info'
    default:
      return 'text-base-content/50'
  }
})

const trustBadgeTitle = computed(() => {
  const meta = trustModeMeta.value
  if (trustModeState.value.isInvalid) {
    return `Trust mode: configured value "${trustModeState.value.raw}" is not recognized — using ${meta.label} (fail closed). ${meta.description}`
  }
  const prefix = trustModeState.value.isDefault
    ? `Trust mode: ${meta.label} (default)`
    : `Trust mode: ${meta.label}`
  return `${prefix} — ${meta.description}`
})

// Status chip color (unchanged from the pre-109-e badge coloring).
const statusBadgeClass = computed(() => {
  const health = props.server.health
  if (health) {
    switch (health.admin_state) {
      case 'disabled':
        return 'badge-neutral'
      case 'quarantined':
        // MCP-1821 — a quarantined server can ALSO be login-required; the
        // actionable amber "Sign-in required" chip takes precedence over the
        // purple quarantine chip so the user sees the next action.
        if (signInState.value) return 'badge-warning'
        return 'badge-secondary'
      default:
        if (signInState.value) return 'badge-warning'
        switch (health.level) {
          case 'healthy': return 'badge-success'
          case 'degraded': return 'badge-warning'
          case 'unhealthy': return 'badge-error'
          default: return 'badge-ghost'
        }
    }
  }
  if (signInState.value) return 'badge-warning'
  if (props.server.connected) return 'badge-success'
  if (props.server.connecting) return 'badge-warning'
  return 'badge-error'
})

const statusText = computed(() => {
  const health = props.server.health
  if (health) {
    // MCP-1821 — surface an actionable "Sign-in required" for OAuth login
    // states, including a quarantined-and-login-required server.
    if (signInState.value && health.admin_state !== 'disabled') return 'Sign-in required'
    // FR-011: no surface may render `level` as text — fall back to the one
    // status label table, never the raw severity value.
    return healthStatusText(health, props.server.connected)
  }
  if (signInState.value) return 'Sign-in required'
  if (props.server.connected) return 'Connected'
  if (props.server.connecting) return 'Connecting'
  return 'Disconnected'
})

// Audit F12's plain-language half, now the status line's DETAIL segment
// rather than a full-width error block. health.summary is already the
// mapped phrase ("Host not found") while the server is administratively
// enabled; for a disabled/quarantined server the calculator's summary
// describes the admin state instead, which the status TEXT above already
// says — so the detail falls through to the structured diagnostic, then to
// the raw chain's last segment (the root cause), never the whole chain.
const statusDetail = computed(() => {
  const health = props.server.health
  if (health?.summary && health.admin_state === 'enabled' && health.summary !== statusText.value) {
    return health.summary
  }
  const diagnosticMessage = props.server.diagnostic?.user_message
  if (diagnosticMessage) return diagnosticMessage
  const raw = props.server.last_error ?? ''
  if (!raw) return ''
  const segments = raw.split(': ')
  return segments[segments.length - 1] || raw
})

// The full technical detail lives in the tooltip — never printed on the card
// face (audit F12: no full-width raw-error dump).
const statusTooltip = computed(() => {
  const health = props.server.health
  if (health?.detail) return health.detail
  return props.server.last_error ?? ''
})

// ONE primary action, from the SAME actions[0] value and label table every
// other surface reads (Spec 109 FR-013/FR-014, utils/health.ts
// healthActionLabel — internal/health.ActionLabels on the Go side). `actions`
// can carry a proactive nudge even on a `ready`/usable server (e.g. a
// token expiring soon), so this does not gate on `status`.
const primaryAction = computed<string>(() => {
  const health = props.server.health
  let action = ''
  if (health) {
    if (health.actions && health.actions.length > 0) action = health.actions[0]
    else if (health.action) action = health.action
  } else if (signInState.value) {
    // Old-core / diagnostic-only fallback: no health object at all.
    action = 'login'
  }
  // Race guard: disableServer()'s optimistic update flips top-level `enabled`
  // to false immediately, but `health` (still reporting a pre-disable
  // 'login') is only replaced once the SSE-triggered refresh lands. `enabled`
  // itself always updates immediately, so it is the one field safe to gate
  // on to stop a stale Sign-in CTA outliving the disable it just requested.
  if (action === 'login' && !props.server.enabled) return 'enable'
  return action
})

const primaryLabel = computed(() => healthActionLabel(primaryAction.value))

// login/restart/enable run in place; every other action opens the screen
// that performs it — never a one-click approve (FR-005/FR-014). The approve
// action is labelled "Review" and links straight to the server's Tools tab
// (Spec 109 FR-013's interim review surface, until the dedicated review
// screen ships); it never approves from here.
//
// Review round 1 (109-e high finding): this used to link to `/review/<name>`
// on the theory that a redirect to `?tab=tools` would land elsewhere
// (109-a T026a). That redirect route does not exist on this branch (and
// 109-a is not merged), so the link 404'd. Pointing straight at the Tools
// tab needs no redirect to exist at all.
const primaryKind = computed<'execute' | 'navigate' | ''>(() => {
  switch (primaryAction.value) {
    case 'login':
    case 'restart':
    case 'enable':
      return 'execute'
    case 'approve':
    case 'set_secret':
    case 'configure':
    case 'edit_url':
    case 'view_logs':
      return 'navigate'
    default:
      return ''
  }
})

const primaryHref = computed(() => {
  switch (primaryAction.value) {
    case 'approve':
      return serverDetailPath(props.server.name, 'tools')
    case 'set_secret':
      return '/secrets'
    case 'configure':
      return serverDetailPath(props.server.name, 'config')
    case 'edit_url':
      // Audit F11: a name that does not resolve is an address problem, not a
      // restartable outage — land on the endpoint field itself.
      return `${serverDetailPath(props.server.name, 'config')}&focus=endpoint`
    case 'view_logs':
      return serverDetailPath(props.server.name, 'logs')
    default:
      return serverDetailPath(props.server.name)
  }
})

async function runPrimaryAction() {
  switch (primaryAction.value) {
    case 'login':
      await triggerOAuth()
      break
    case 'restart':
      await restart()
      break
    case 'enable':
      await enableServer()
      break
    default:
      break
  }
}

// Audit F7: a disabled control must say why it is disabled.
const scanDisabledReason = computed(() =>
  props.server.quarantined && !props.server.enabled
    ? 'Enable the server to scan it — approving it from Review enables it too'
    : 'Enable the server first — a scan inspects a running server'
)

// Tool-level quarantine count (pending + changed) — folded into the stats
// line as plain text (Spec 109 FR-013): reaching the review is the primary
// action's job, not a second button on the card.
const quarantineToolCount = computed(() => {
  const q = props.server.quarantine
  if (!q) return 0
  return (q.pending_count ?? 0) + (q.changed_count ?? 0)
})

const toolQuarantineSummary = computed(() => {
  const q = props.server.quarantine
  if (!q) return ''
  const pending = q.pending_count ?? 0
  const changed = q.changed_count ?? 0
  const total = pending + changed
  if (total === 0) return ''
  const noun = (n: number) => (n === 1 ? 'tool' : 'tools')
  if (changed > 0 && pending > 0) {
    return `${pending} pending, ${changed} changed`
  }
  if (changed > 0) {
    return `${changed} changed ${noun(changed)} — re-review needed`
  }
  return `${pending} ${noun(pending)} pending approval`
})

// Security scan verdict (Spec 039), folded into the stats line.
const hasHeldTools = computed(() => quarantineToolCount.value > 0)

const securityBadgeText = computed(() => {
  const scan = props.server.security_scan
  if (!scan) return ''
  if (scan.status === 'clean' && hasHeldTools.value) {
    const n = quarantineToolCount.value
    return `Clean scan · ${n} tool${n !== 1 ? 's' : ''} held`
  }
  switch (scan.status) {
    case 'clean': return 'Clean'
    case 'warnings': {
      const count = scan.finding_counts?.warning ?? 0
      return `${count} warning${count !== 1 ? 's' : ''}`
    }
    case 'dangerous': return 'Dangerous'
    case 'failed': return 'Scan failed'
    case 'not_scanned': return ''
    case 'scanning': return 'Scanning…'
    default: return ''
  }
})

// Issue #1065: a clean CONTENT verdict and a REVIEW-STATE requirement are
// both true at once and must never read as contradictory peers ("Clean"
// beside "needs review"). While the server is quarantined the verdict is
// always subordinated to the review ask, never shown standalone.
const quarantineScanNote = computed(() => {
  const scan = props.server.security_scan
  const status = scan?.status
  if (!scan || !status || status === 'not_scanned') return ''
  switch (status) {
    case 'scanning':
      return 'Scanning… — still needs review'
    case 'failed':
      return 'Last scan could not complete — still needs review'
    case 'clean':
      return 'Last scan: clean — still needs review'
    case 'warnings': {
      const n = scan.finding_counts?.warning ?? 0
      return `Last scan: ${n} warning${n !== 1 ? 's' : ''} — needs review`
    }
    case 'dangerous':
      return 'Last scan: dangerous findings — needs review'
    default:
      return ''
  }
})

const securityLine = computed(() => (props.server.quarantined ? quarantineScanNote.value : securityBadgeText.value))

// GH #938: a clean FULL-SERVER scan verdict must not read as "all clear" when
// tools are still held — the color, not just the wording, has to demote.
const securityBadgeColor = computed(() => {
  if (securityBadgeText.value === '') return ''
  const scan = props.server.security_scan
  if (scan?.status === 'clean' && hasHeldTools.value) return 'text-warning'
  switch (scan?.status) {
    case 'clean': return 'text-success'
    case 'warnings': return 'text-warning'
    case 'dangerous': return 'text-error'
    case 'failed': return 'text-error'
    default: return 'text-base-content/60'
  }
})

// Activity stats line (Spec 109 FR-013): last call time + 24h errors, both
// from the ONE per-page-load `GET /activity/summary` response the parent
// view fetches and slices per server — never a per-card request.
const errors24h = computed(() => props.activityStats?.errors ?? 0)

// Review round 2 (109-e medium finding): `lastCallText` read `Date.now()`
// directly, but its only reactive dependency was `activityStats.last_call_at`
// — Vue's computed cache never re-evaluates on the passage of time alone, so
// an idle server's "Xm ago" froze at whatever it read on first render and
// stayed wrong for as long as the tab stayed open. `nowMs` is a ticking ref
// the computed actually depends on, refreshed on an interval and torn down
// with the component.
const nowMs = ref(Date.now())
let nowTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  nowTimer = setInterval(() => { nowMs.value = Date.now() }, 30_000)
})
onUnmounted(() => {
  if (nowTimer !== undefined) clearInterval(nowTimer)
})

const lastCallText = computed(() => {
  const iso = props.activityStats?.last_call_at
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return 'never'
  const diffMs = nowMs.value - then
  const minutes = Math.floor(diffMs / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
})

// Links to Activity scoped to this server, last 24h, and to errors only when
// there are any — the same query-param contract 109-k teaches Activity to
// read (url-filter-contract.md).
const activityLink = computed(() => {
  const base = `/activity?server=${encodeURIComponent(props.server.name)}&from=-24h`
  return errors24h.value > 0 ? `${base}&status=error` : base
})

const canLogout = computed(() => {
  if (!props.server.enabled) return false
  if (props.server.user_logged_out) return false
  if (!isHttpProtocol.value) return false

  const hasToken = props.server.authenticated === true
  if (!hasToken) return false
  if (props.server.connecting) return false
  if (props.server.connected) return true

  if (props.server.last_error) {
    if (props.server.oauth_status === 'expired') return false
    const isOAuthRequired = props.server.last_error.includes('OAuth authentication required') ||
      props.server.last_error.includes('authorization') ||
      props.server.last_error.includes('401') ||
      props.server.last_error.includes('invalid_token')
    if (isOAuthRequired) return false
    return true
  }

  if (props.server.oauth_status === 'authenticated') return true
  return false
})

async function toggleEnabled() {
  loading.value = true
  try {
    if (props.server.enabled) {
      await serversStore.disableServer(props.server.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Disabled',
        message: `${props.server.name} has been disabled`,
      })
    } else {
      await serversStore.enableServer(props.server.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Enabled',
        message: `${props.server.name} has been enabled`,
      })
    }
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Operation Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function enableServer() {
  loading.value = true
  try {
    await serversStore.enableServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Enabled',
      message: `${props.server.name} has been enabled`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Enable Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function restart() {
  loading.value = true
  try {
    await serversStore.restartServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Restarted',
      message: `${props.server.name} is restarting`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Restart Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function triggerOAuth() {
  loading.value = true
  try {
    await serversStore.triggerOAuthLogin(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Login Triggered',
      message: `Check your browser for ${props.server.name} login`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'OAuth Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function triggerLogout() {
  loading.value = true
  try {
    await serversStore.triggerOAuthLogout(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Logout Successful',
      message: `${props.server.name} has been logged out`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Logout Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function confirmDelete() {
  loading.value = true
  try {
    await serversStore.deleteServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Deleted',
      message: `${props.server.name} has been deleted successfully`,
    })
    showDeleteConfirmation.value = false
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Delete Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
/* Spec 109 FR-013 / T068: every card in the grid has the same height,
   whatever state it is in. The primary-action row already reserves its
   height empty; this floors the card body itself so a short one-line status
   (e.g. a healthy "ready" server) does not sit shorter than a card carrying
   a status detail + tool-quarantine note. */
.server-card {
  min-height: 232px;
}
.server-card :deep(.card-body) {
  height: 100%;
}
</style>
