<template>
  <!-- Spec 134 FR-020/021: one fixed-height table row. No inline expansion and no
       "Details" link: the server name opens server detail. Every secondary
       action of the old card lives in the more menu, behaving as before. -->
  <div class="server-row" role="row" data-test="server-row" :data-server="server.name">
    <div class="server-row__name" role="cell">
      <router-link
        :to="serverDetailPath(server.name)"
        class="link link-hover font-medium truncate block"
        :title="server.name"
        data-test="server-detail-link"
      ><span data-test="server-card-title">{{ displayName }}</span></router-link>
      <span class="text-xs text-base-content/60 block truncate" data-test="server-card-protocol">{{ server.protocol }}</span>
    </div>

    <div class="server-row__status" role="cell" :title="server.health?.status || undefined" data-test="server-card-status-line">
      <StatusPill v-if="hasStatus" :status="server.health?.status" data-test="server-status-chip" />
      <span v-else :class="['badge badge-sm', legacyChipClass]" data-test="server-status-chip">{{ legacyChipText }}</span>
    </div>

    <div class="server-row__reason text-sm text-base-content/70" role="cell" :title="reasonTitle" data-test="server-row-reason">{{ reason }}</div>

    <div class="server-row__tools text-sm" role="cell" data-test="server-card-tool-count">{{ server.tool_count }}<span class="server-row__label"> tools</span></div>

    <div class="server-row__last text-sm" role="cell">
      <router-link
        :to="activityLink"
        class="link link-hover text-base-content/70 hover:text-base-content whitespace-nowrap"
        data-test="server-card-stats-line"
      >
        <span>last call {{ lastCall }}</span>
        <span :class="errors24h > 0 ? 'text-error' : ''"> · {{ errors24h }} error{{ errors24h === 1 ? '' : 's' }} (24h)</span>
      </router-link>
    </div>

    <div class="server-row__action" role="cell" data-test="server-card-primary-row">
      <button
        v-if="primaryAction && primaryKind === 'execute'"
        type="button"
        @click="runPrimaryAction"
        :disabled="loading"
        class="btn btn-sm btn-primary server-row__btn"
        data-test="server-card-primary-action"
      >
        <span v-if="loading" class="loading loading-spinner loading-xs"></span>
        {{ primaryLabel }}
      </button>
      <router-link
        v-else-if="primaryAction && primaryKind === 'navigate'"
        :to="primaryHref"
        class="btn btn-sm btn-primary server-row__btn"
        data-test="server-card-primary-action"
      >
        {{ primaryLabel }}
      </router-link>
    </div>

    <div class="server-row__more dropdown dropdown-end" role="cell">
      <button
        tabindex="0"
        type="button"
        class="btn btn-ghost btn-sm server-row__btn server-row__btn--icon"
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
            class="dropdown-content menu menu-sm z-[var(--z-dropdown)] bg-base-100 rounded-box shadow-lg border border-base-300 w-52 p-1 server-row__menu"
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
                :to="reviewPath(server.name)"
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
                aria-disabled="true"
                data-test="server-card-scan-disabled"
              >Scan<span class="block text-xs font-normal whitespace-normal" data-test="server-card-scan-disabled-reason">{{ scanDisabledReason }}</span></span>
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
            <li v-if="canSignIn">
              <button type="button" @click="triggerOAuth" :disabled="loading" data-test="server-card-menu-login">{{ server.authenticated || server.connected ? 'Sign in again' : 'Sign in' }}</button>
            </li>
            <li>
              <button
                type="button"
                class="text-error"
                @click="openDelete($event)"
                :disabled="loading"
                data-test="server-card-menu-delete"
              >Delete</button>
            </li>
          </ul>
    </div>

    <!-- Delete Confirmation Modal — the only place Delete is reachable from
         (Spec 109 FR-013: "Delete only in ⋯ with a confirmation naming the
         server"). -->
    <div v-if="showDeleteConfirmation" class="modal modal-open" role="dialog" aria-modal="true" aria-label="Delete server" @keydown.esc="closeDelete" @keydown.tab="trapDeleteTab">
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
            @click="closeDelete"
            :disabled="loading"
            class="btn btn-outline min-h-11 md:min-h-8"
            ref="deleteCancel"
            data-test="server-card-delete-cancel"
          >
            Cancel
          </button>
          <button
            @click="confirmDelete"
            :disabled="loading"
            class="btn btn-error min-h-11 md:min-h-8"
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
import { ref, computed, nextTick, watch } from 'vue'
import type { Server, ActivityPerServer } from '@/types'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { useSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'
import { serverDetailPath, serverDisplayName, reviewPath } from '@/utils/serverRoute'
import { oauthSignInState, healthStatusText, healthActionLabel } from '@/utils/health'
import { canStartOAuthLogin } from '@/utils/oauthActions'
import { serverReason } from '@/utils/serverReason'
import { lastCallText as formatLastCall, serverActivityLink, trustBadge } from '@/utils/serverFacts'
import StatusPill from '@/components/StatusPill.vue'

interface Props {
  server: Server
  // One server's slice of the ONE `GET /activity/summary?period=24h` response
  // the parent view fetches per page load (Spec 109 FR-013) — undefined for a
  // server with no call in the period.
  activityStats?: ActivityPerServer
  // Shared clock from the page so 300 rows do not each run a timer.
  nowMs?: number
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
// The dialog takes focus (Cancel, the safe choice) so a keyboard user is inside it.
const deleteCancel = ref<HTMLButtonElement | null>(null)
const deleteTrigger = ref<HTMLElement | null>(null)
function closeDelete() {
  showDeleteConfirmation.value = false
  // Back to the control that opened it (the more button), not to <body>.
  void nextTick(() => deleteTrigger.value?.focus())
}
// Keep Tab inside the open dialog: Cancel and Delete Server only.
function trapDeleteTab(event: KeyboardEvent) {
  const dialog = (event.currentTarget as HTMLElement | null)
  const buttons = Array.from(dialog?.querySelectorAll<HTMLButtonElement>('button:not([disabled])') ?? [])
  if (buttons.length === 0) return
  const first = buttons[0]
  const last = buttons[buttons.length - 1]
  const active = document.activeElement
  if (event.shiftKey && active === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && active === last) { event.preventDefault(); first.focus() }
}
function openDelete(event: Event) {
  // The more button holds focus while its menu is open; remember it for the return.
  deleteTrigger.value = (event.currentTarget as HTMLElement | null)?.closest('.dropdown')?.querySelector<HTMLElement>('[data-test="server-card-menu-trigger"]') ?? null
  showDeleteConfirmation.value = true
}
watch(showDeleteConfirmation, open => { if (open) void nextTick(() => deleteCancel.value?.focus()) })

const isHttpProtocol = computed(() => {
  return props.server.protocol === 'http' || props.server.protocol === 'streamable-http'
})

// MCP-1821 — OAuth sign-in state (null when no sign-in is required). When set,
// the status line reads a calm amber "Sign-in required" instead of red
// "Disconnected"/"Unhealthy", matching the ServerDetail Sign-in CTA.
const signInState = computed(() => oauthSignInState(props.server))
const canSignIn = computed(() => canStartOAuthLogin(props.server))

// Trust mode label for the menu entry; the shield itself lives on server detail.
const trustBadgeLabel = computed(() => trustBadge(props.server).label)

// Spec 134 FR-003: a server with `health.status` prints the label through the
// one StatusPill. Without it (old core, or a payload with only a summary) the
// present legacy chip and words stay.
const hasStatus = computed(() => !!props.server.health?.status)
const legacyChipClass = computed(() => {
  const health = props.server.health
  if (health?.admin_state === 'disabled') return 'badge-neutral'
  if (signInState.value) return 'badge-warning'
  if (health) {
    if (health.admin_state === 'quarantined') return 'badge-secondary'
    switch (health.level) {
      case 'healthy': return 'badge-success'
      case 'degraded': return 'badge-warning'
      case 'unhealthy': return 'badge-error'
      default: return 'badge-ghost'
    }
  }
  if (props.server.connected) return 'badge-success'
  if (props.server.connecting) return 'badge-warning'
  return 'badge-error'
})
const legacyChipText = computed(() => {
  const health = props.server.health
  if (signInState.value && health?.admin_state !== 'disabled') return 'Sign-in required'
  if (health) return healthStatusText(health, props.server.connected)
  if (props.server.connected) return 'Connected'
  if (props.server.connecting) return 'Connecting'
  return 'Disconnected'
})

// FR-022: the one-line reason; the whole diagnostic is on server detail.
const reason = computed(() => serverReason(props.server))
const reasonTitle = computed(() => props.server.health?.detail || props.server.last_error || reason.value)

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
// action is labelled "Review" and opens the dedicated review screen. It
// never approves from this card.
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
      return reviewPath(props.server.name)
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

// Activity stats line (Spec 109 FR-013): last call time + 24h errors, both
// from the ONE per-page-load `GET /activity/summary` response the parent
// view fetches and slices per server — never a per-card request.
const errors24h = computed(() => props.activityStats?.errors ?? 0)

const lastCall = computed(() => formatLastCall(props.activityStats?.last_call_at, props.nowMs ?? Date.now()))

const activityLink = computed(() => serverActivityLink(props.server.name, errors24h.value))

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
    const result = await serversStore.triggerOAuthLogin(props.server.name)
    systemStore.addToast({
      type: result.browser_opened ? 'info' : 'warning',
      title: result.browser_opened ? 'Sign-in opened' : 'Open the sign-in page',
      message: result.browser_opened ? `Finish signing in to ${props.server.name} in your browser.` : 'Your browser blocked the new tab. Use the link below to continue.',
      link: result.auth_url ? { url: result.auth_url, label: `Sign in to ${props.server.name}` } : undefined,
      duration: result.browser_opened ? 10000 : 0,
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
    // The dialog stays open on failure: keep the keyboard inside it.
    void nextTick(() => deleteCancel.value?.focus())
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
/* Fixed row height at desktop widths; the table header (Servers.vue) uses the
   same grid. Reason and last call never wrap (FR-020). The desktop columns need
   about 900 px of table; below a 1200 px viewport (sidebar included) the row
   stacks: name + status, reason, tools + last call, action + more, with 44 px
   targets. A container query would be neater, but layout containment would make
   the table the containing block of the fixed delete dialog. */
.server-row {
  display: grid;
  grid-template-columns: var(--servers-cols);
  align-items: center;
  column-gap: 12px;
  height: 40px;
  padding: 0 12px;
  border-bottom: 1px solid var(--color-base-300, oklch(var(--b3)));
}
.server-row > * { min-width: 0; }
.server-row__reason,
.server-row__last,
.server-row__tools { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.server-row__label { display: none; }
/* A long forward-compatible status word is clipped inside its own cell. */
.server-row__status { overflow: hidden; }
.server-row__status :deep(.status-pill) { max-width: 100%; overflow: hidden; }
.server-row__action { display: flex; justify-content: flex-end; }
.server-row__more { justify-self: end; }
@media (max-width: 1199px) {
  .server-row {
    grid-template-columns: minmax(0, 1fr) auto;
    grid-template-areas: 'name status' 'reason reason' 'tools last' 'action more';
    height: auto;
    row-gap: 8px;
    padding: 12px;
  }
  .server-row__name { grid-area: name; }
  .server-row__status { grid-area: status; }
  .server-row__reason { grid-area: reason; }
  .server-row__tools { grid-area: tools; }
  .server-row__last { grid-area: last; text-align: right; }
  .server-row__action { grid-area: action; justify-content: flex-start; min-height: 44px; }
  .server-row__more { grid-area: more; }
  .server-row__label { display: inline; }
  .server-row__btn { min-height: 44px; }
  .server-row__btn--icon { min-width: 44px; }
  .server-row__menu :where(li > *) { min-height: 44px; display: flex; align-items: center; }
}
</style>
