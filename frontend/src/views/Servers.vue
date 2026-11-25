<template>
  <div class="space-y-4">
    <!-- Page Header -->
    <div class="flex flex-wrap justify-between items-center gap-3">
      <div>
        <h1 class="text-3xl font-bold">Servers</h1>
        <p class="text-base-content/70 mt-1">Manage upstream MCP servers</p>
      </div>
      <div class="flex items-center space-x-2">
        <button
          @click="refreshServers"
          :disabled="serversStore.loading.loading"
          class="btn btn-outline min-h-11 md:min-h-8"
          data-test="servers-refresh"
        >
          <UiIcon name="refresh" class="w-5 h-5" />
          <span v-if="serversStore.loading.loading" class="loading loading-spinner loading-sm"></span>
          {{ serversStore.loading.loading ? 'Refreshing...' : 'Refresh' }}
        </button>
        <button
          v-if="hasEnabledScanners()"
          @click="scanAllServers"
          :disabled="scanAllRunning"
          class="btn btn-primary min-h-11 md:min-h-8"
          data-test="servers-scan-all"
        >
          <UiIcon name="shield" class="w-5 h-5" />
          <span v-if="scanAllRunning" class="loading loading-spinner loading-sm"></span>
          {{ scanAllRunning ? 'Scanning...' : 'Scan All' }}
        </button>
      </div>
    </div>

    <!-- Spec 134 FR-016/FR-023: one filter row. "View as" is the profile picker
         (maintainer decision 2026-11-25). Chips, More filters and search wait
         for the first server so a fresh install shows only the empty state. -->
    <div class="space-y-2" data-test="servers-scope-controls">
      <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
        <ViewAs />
        <template v-if="hasServers">
          <div class="flex flex-wrap gap-2" role="group" aria-label="Server filter" data-test="servers-chips">
            <button
              v-for="chip in chips"
              :key="chip.value"
              type="button"
              :class="['btn btn-sm min-h-11 md:min-h-8', filter === chip.value ? 'btn-primary' : 'btn-outline']"
              :aria-pressed="filter === chip.value"
              :data-test="chip.value === 'all' ? 'kpi-card-total' : `servers-chip-${chip.value}`"
              @click="selectChip(chip.value)"
            >{{ chip.label }} ({{ chip.count }})</button>
          </div>
          <details ref="moreFilters" class="dropdown" data-test="servers-more-filters">
            <summary class="btn btn-sm btn-outline min-h-11 md:min-h-8" data-test="servers-more-filters-toggle">
              More filters<span v-if="legacyActiveLabel"> ({{ legacyActiveLabel }})</span>
            </summary>
            <ul class="dropdown-content menu z-[var(--z-dropdown)] bg-base-100 rounded-box shadow-lg border border-base-300 w-64 p-2">
              <li v-for="item in legacyItems" :key="item.value">
                <button
                  type="button"
                  class="min-h-11 md:min-h-0"
                  :class="filter === item.value ? 'menu-active' : ''"
                  :aria-pressed="filter === item.value"
                  :data-test="`kpi-card-${item.value}`"
                  @click="selectLegacy(item.value)"
                >{{ item.label }} ({{ item.count }})</button>
              </li>
            </ul>
          </details>
          <input
            v-model="searchQuery"
            type="search"
            placeholder="Search servers..."
            aria-label="Search servers"
            data-test="servers-search"
            class="input input-bordered input-sm min-h-11 md:min-h-8 w-full sm:w-64"
          />
        </template>
      </div>
      <ScopeChips page="servers" :scope-query="scopeQuery" :disabled="disabledScope" :unavailable="scopeUnavailableNames" />
      <p v-if="hasServers" class="text-sm text-base-content/70" data-test="servers-totals">
        <span data-test="servers-total-count">{{ counts.total }} servers, {{ counts.enabled }} enabled</span>
        <span> · </span>
        <span data-test="servers-connected-denominator">{{ connectedSummary }}</span>
        <span> · </span>
        <span data-test="kpi-card-total-tools">{{ totalTools }} tools available</span>
      </p>
    </div>

    <!-- A refresh failed but we still hold a list. Say so without throwing the
         servers away: the failure is about freshness, not about the servers. -->
    <div
      v-if="hasServers && serversStore.loading.error"
      class="alert alert-warning"
      data-test="servers-refresh-error"
    >
      <UiIcon name="alert" class="w-6 h-6" />
      <div class="flex-1 text-sm">
        Couldn't refresh the server list — showing the last known state.
        <span class="opacity-70">{{ serversStore.loading.error }}</span>
      </div>
      <button @click="refreshServers" class="btn btn-sm min-h-11 md:min-h-8">Retry</button>
    </div>

    <!-- Loading State -->
    <div v-if="serversStore.loading.loading || scopedBusy" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading servers...</p>
    </div>

    <!-- Spec 109-l: the viewed profile does not exist (404) or the scoped list
         failed. An inline message with a way out, never a blanked page. -->
    <div v-else-if="profileActive && scopedNotFound" class="alert alert-warning" role="alert" data-test="servers-profile-not-found">
      <div class="flex-1 text-sm">Profile not found</div>
      <button type="button" class="btn btn-sm" data-test="servers-profile-clear" @click="clearProfile">Clear</button>
    </div>
    <div v-else-if="profileActive && scopedError" class="alert alert-error" role="alert" data-test="servers-profile-error">
      <div class="flex-1 text-sm">{{ scopedError }}</div>
      <button type="button" class="btn btn-sm" @click="loadScoped(false)">Try Again</button>
    </div>

    <!-- Error State: the load failed and we have nothing to fall back on.
         Audit F28: also suppressed while the Authentication Required modal is
         up — "Failed to load servers" is that modal's cause restated, and it
         has no fix of its own to offer. -->
    <div
      v-else-if="serversStore.loading.error && !hasServers && !systemStore.authRequired"
      class="alert alert-error"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load servers</h3>
        <div class="text-sm">{{ serversStore.loading.error }}</div>
      </div>
      <button @click="refreshServers" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- First-run empty state: no servers configured at all. This is the page a
         new user lands on after closing the setup wizard, so it has to offer
         the next action rather than restate that the list is empty. Kept
         distinct from the filter/search empty state below — "you have nothing
         yet" and "nothing matched" need different answers. -->
    <div v-else-if="isFirstRun" class="text-center py-12" data-test="servers-first-run-empty">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No servers yet</h3>
      <p class="text-base-content/70 mb-4 max-w-lg mx-auto">
        Upstream MCP servers are where your tools come from. Add one and MCPProxy
        indexes its tools, so your AI agent can find them without loading every
        schema into its context.
      </p>
      <div class="flex flex-wrap gap-2 justify-center">
        <button
          @click="router.push('/add-server')"
          class="btn btn-primary"
          data-test="servers-empty-add"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
          </svg>
          Add Server
        </button>
        <router-link
          to="/add-server?tab=catalog"
          class="btn btn-outline"
          data-test="servers-empty-registry"
        >
          Browse catalog
        </router-link>
      </div>
      <p class="mt-4 text-sm">
        <button
          type="button"
          class="link link-primary"
          data-test="servers-empty-import"
          @click="openImportWizard"
        >
          Import from your AI client configs
        </button>
      </p>
    </div>

    <!-- Nothing to show and no list has arrived. This view does not fetch on
         mount — App.vue and the Dashboard do — so on a direct load there is a
         window where `loading` is still false and `servers` is still empty,
         and neither empty state is true yet. Say "not yet" rather than pick
         one of them and be wrong. Servers we already hold are a list by
         definition, so this never gates the grid below. -->
    <div
      v-else-if="!hasServers && !serversStore.loaded"
      class="text-center py-12"
      data-test="servers-pending"
    >
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Filter/search empty state: servers exist, none match the current view. -->
    <div v-else-if="filteredServers.length === 0" class="text-center py-12" data-test="servers-filter-empty">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">{{ profileActive && !searchQuery && filter === 'all' ? 'No servers in this profile' : 'No servers found' }}</h3>
      <p class="text-base-content/70 mb-4">
        {{ searchQuery ? 'No servers match your search criteria' : emptyFilterText }}
      </p>
      <button v-if="profileActive && !searchQuery && filter === 'all'" type="button" class="btn btn-outline" data-test="servers-profile-clear" @click="clearProfile">
        Clear
      </button>
      <button v-else-if="searchQuery" @click="searchQuery = ''" class="btn btn-outline">
        Clear Search
      </button>
      <button v-else @click="selectChip('all')" class="btn btn-outline" data-test="servers-show-all">
        Show all servers
      </button>
    </div>

    <!-- Servers table: fixed row height, problems first (FR-024). -->
    <div
      v-else
      role="table"
      aria-label="Servers"
      class="servers-table bg-base-100 rounded-box shadow-sm"
      data-test="servers-table"
      :style="{ '--servers-cols': COLS }"
    >
      <div class="servers-table__head" role="row">
        <div role="columnheader">Server</div>
        <div role="columnheader">Status</div>
        <div role="columnheader">Reason</div>
        <div role="columnheader">Tools</div>
        <div role="columnheader">Last call</div>
        <div role="columnheader"><span class="sr-only">Action</span></div>
        <div role="columnheader"><span class="sr-only">More</span></div>
      </div>
      <div role="rowgroup">
        <ServerRow
          v-for="server in filteredServers"
          :key="server.name"
          :server="server"
          :activity-stats="perServerActivity[server.name]"
          :now-ms="nowMs"
          v-memo="[
            nowMs,
            server.connected,
            server.connecting,
            server.enabled,
            server.quarantined,
            server.tool_count,
            server.last_error,
            server.authenticated,
            server.title,
            server.diagnostic?.code,
            server.diagnostic?.user_message,
            server.health?.status,
            server.health?.summary,
            server.health?.detail,
            server.health?.admin_state,
            server.health?.level,
            server.health?.action,
            server.health?.actions?.join(','),
            perServerActivity[server.name]?.errors,
            perServerActivity[server.name]?.last_call_at
          ]"
        />
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="serversHints" />

  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { useProfilesStore, PROFILES_CHANGED_EVENT } from '@/stores/profiles'
import { useOnboardingStore } from '@/stores/onboarding'
import api from '@/services/api'
import type { Server } from '@/types'
import ServerRow from '@/components/ServerRow.vue'
import UiIcon from '@/components/UiIcon.vue'
import { countServers, matchesFilter, matchesSearch, resolveFilter, sortServers, type LegacyServerFilter, type ServerChip, type ServerFilter } from '@/utils/serversList'
import ScopeChips from '@/components/scope/ScopeChips.vue'
import ViewAs from '@/components/ViewAs.vue'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import { useSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'
import type { ActivityPerServer } from '@/types'

// Grid columns shared by the table header and every row (ServerRow.vue).
const COLS = 'minmax(160px, 1.1fr) 150px minmax(0, 2fr) 64px 250px 120px 44px'

const serversStore = useServersStore()
const systemStore = useSystemStore()
const profilesStore = useProfilesStore()
const onboardingStore = useOnboardingStore()
const route = useRoute()
const router = useRouter()
const scopeQuery = useScopeQuery('servers')
const searchQuery = ref('')
const scanAllRunning = ref(false)
const { hasEnabledScanners } = useSecurityScannerStatus()

// Spec 109 FR-013: the server card's stats line (last call, 24h errors)
// reads this ONE per-page-load summary rather than issuing a request per
// card. Keyed by server name; a server absent from the map had no call in
// the period.
const perServerActivity = ref<Record<string, ActivityPerServer>>({})

async function loadActivitySummary() {
  try {
    const res = await api.getActivitySummary('24h')
    if (res.success && res.data) {
      const byName: Record<string, ActivityPerServer> = {}
      for (const entry of res.data.per_server ?? []) {
        byName[entry.name] = entry
      }
      perServerActivity.value = byName
    }
  } catch {
    // Best-effort: the card falls back to "never" / 0 errors, which is a
    // safe (if stale) default — a failed summary fetch must not block the
    // rest of the Servers page from rendering.
  }
}

// Spec 109-k (activity-scope-filters), T119: Servers wired to the URL filter
// contract (url-filter-contract.md — `status` and `q` are client-side only
// here, `GET /servers` takes no query string). Read on mount and on every
// route change, one-directional (URL -> local state), matching Tools.vue's
// existing `q` pattern — this is what makes the `/review` -> `/servers?status
// =needs_review` redirect (router T026a) actually filter the list instead of
// silently landing on the unfiltered one.
function applyScopeQueryParams() {
  const q = route.query.q
  const next = typeof q === 'string' ? q : ''
  if (next !== searchQuery.value) searchQuery.value = next
}

// ---------------------------------------------------------------------------
// Spec 109-l P10c: the `profile` scope. A profile in the URL asks
// GET /servers?profile=<p> for the servers (and tool counts) that profile
// admits. The result is page-local: the shared servers store keeps the full
// list (the sidebar, header and Dashboard read it), so a scoped view never
// leaks into them.
// ---------------------------------------------------------------------------
const UNATTRIBUTED_NOTE = 'Unattributed applies to Activity and Usage only'

const profileScopeAvailable = computed(() => isScopeParamAvailable('profile'))
const profileValue = computed(() => scopeQuery.state.profile ?? '')
const profileChipVisible = computed(() => scopeQuery.chips.value.some(chip => chip.name === 'profile'))
const disabledScope = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  if (profileValue.value === '-' && profileScopeAvailable.value) out.profile = UNATTRIBUTED_NOTE
  return out
})

// The profile the request will carry: `-` (unattributed) applies to Activity and
// Usage only, so it is a disabled chip here and never sent.
const appliedProfile = computed(() => {
  if (!profileScopeAvailable.value) return ''
  const value = scopeQuery.toRest()?.profile ?? ''
  return value === '-' ? '' : value
})
const profileActive = computed(() => appliedProfile.value !== '')

// Rule 1 (no flash): while the URL names a profile but /status has not yet said
// whether this build supports it, show the spinner, not the unfiltered list.
const scopeWaiting = ref(false)
const scopeWaitTimedOut = ref(false)
const scopeUnavailableNames = computed(() => {
  const value = route.query.profile
  return scopeWaitTimedOut.value && !profileScopeAvailable.value && typeof value === 'string' && value !== '' ? ['profile'] : []
})

const scopedServers = ref<Server[] | null>(null)
const scopedLoading = ref(false)
const scopedError = ref('')
const scopedNotFound = ref(false)
const scopedBusy = computed(() => scopeWaiting.value || (profileActive.value && scopedLoading.value && scopedServers.value === null))

let scopedSeq = 0
async function loadScoped(silent: boolean) {
  const profile = appliedProfile.value
  const seq = ++scopedSeq
  if (!profile) {
    scopedServers.value = null
    scopedLoading.value = false
    scopedError.value = ''
    scopedNotFound.value = false
    return
  }
  if (!silent) {
    scopedServers.value = null
    scopedLoading.value = true
  }
  scopedError.value = ''
  scopedNotFound.value = false
  try {
    const response = await api.getServers({ profile })
    if (seq !== scopedSeq) return
    if (response.success && response.data) {
      scopedServers.value = response.data.servers ?? []
    } else {
      scopedServers.value = []
      scopedError.value = response.error || 'Failed to load servers'
    }
  } catch (err) {
    if (seq !== scopedSeq) return
    scopedServers.value = []
    const status = (err as { status?: number } | null)?.status
    if (status === 404) scopedNotFound.value = true
    else scopedError.value = err instanceof Error ? err.message : 'Failed to load servers'
  } finally {
    if (seq === scopedSeq) scopedLoading.value = false
  }
}

function clearProfile() {
  scopeQuery.clear(['profile'])
}

// The applied profile changed (the select, a chip, a link, or the feature list
// arriving after mount): refetch under it.
watch(appliedProfile, () => { void loadScoped(false) })

// The shared list refreshes on SSE; keep a scoped view live (debounced).
let scopedRefreshTimer: ReturnType<typeof setTimeout> | null = null
watch(
  () => serversStore.servers.map(s => `${s.name}|${s.connected}|${s.enabled}|${s.quarantined}|${s.tool_count}|${s.health?.status ?? ''}|${s.security_scan?.status ?? ''}|${s.security_scan?.finding_counts?.warning ?? ''}`).join(','),
  () => {
    if (!profileActive.value) return
    if (scopedRefreshTimer) clearTimeout(scopedRefreshTimer)
    scopedRefreshTimer = setTimeout(() => { void loadScoped(true) }, 300)
  },
)
onBeforeUnmount(() => { if (scopedRefreshTimer) clearTimeout(scopedRefreshTimer) })

// A profile edited elsewhere (the editor, the Profiles page) changes which
// servers a scoped view lists; the server list itself did not change, so the
// fingerprint above would never notice.
function onProfilesChanged() {
  if (profileActive.value) void loadScoped(true)
}
onMounted(() => window.addEventListener(PROFILES_CHANGED_EVENT, onProfilesChanged))
onBeforeUnmount(() => window.removeEventListener(PROFILES_CHANGED_EVENT, onProfilesChanged))

// What the page displays: the profile's list when one is applied, otherwise the
// shared store. Every tile, pill and card below reads these, never the store.
const rows = computed<Server[]>(() => (profileActive.value ? (scopedServers.value ?? []) : serversStore.servers))
const counts = computed(() => countServers(rows.value))
// Only tools that are actually available count: enabled, not quarantined (#285, #1064).
const totalTools = computed(() =>
  rows.value.filter(s => s.enabled && !s.quarantined).reduce((sum, server) => sum + server.tool_count, 0))

onMounted(async () => {
  void loadActivitySummary()
  applyScopeQueryParams()
  if (profileScopeAvailable.value && !profilesStore.loaded && !profilesStore.loading) void profilesStore.fetchProfiles()
  const urlHasProfile = typeof route.query.profile === 'string' && route.query.profile !== ''
  if (urlHasProfile && !profileScopeAvailable.value) {
    scopeWaiting.value = true
    await systemStore.waitForScopeFeatures()
    scopeWaitTimedOut.value = true
    scopeWaiting.value = false
  }
  if (profileActive.value) await loadScoped(false)
})
watch(profileScopeAvailable, available => {
  if (available && !profilesStore.loaded && !profilesStore.loading) void profilesStore.fetchProfiles()
})
watch(() => route.query.q, applyScopeQueryParams)

// The filter lives in the URL (`filter`, legacy `status`) and is derived below;
// only the search box writes back (FR-023: a chip click keeps `q` and `profile`).
watch(searchQuery, value => {
  if ((typeof route.query.q === 'string' ? route.query.q : '') === value) return
  scopeQuery.set({ q: value || undefined })
})

// The page chrome (stat tiles, filter pills, search) only describes a list that
// exists. `servers.length` — not `loaded` — is the right gate: it is also false
// while the very first fetch is in flight, so a fresh install never flashes a
// row of zeroes before the empty state resolves.
const hasServers = computed(() => rows.value.length > 0)

// Telling the user "you have no servers" is a claim, and only a list that
// actually arrived can support it. `loaded` (set once any successful list has
// been applied) is what distinguishes "none configured" from "we don't know
// yet" — `servers` starts empty either way.
const isFirstRun = computed(() => serversStore.loaded && serversStore.servers.length === 0)

// The setup wizard is mounted by Dashboard.vue, so opening it from here means
// navigating there first; the store carries the tab request across the hop.
function openImportWizard() {
  onboardingStore.openWizard('servers')
  void router.push('/')
}
/**
 * The Connected tile's sub-line: "2 of 3 enabled online" (F27, #1046). The
 * denominator is the servers that are meant to be up — a disabled server is not
 * an outage — and it is stated rather than left to be inferred from a
 * percentage. With nothing enabled there is no ratio to report, only a fact.
 */
const connectedSummary = computed(() => {
  const { connected, enabled } = counts.value
  if (enabled === 0) return 'no servers enabled'
  return `${connected} of ${enabled} enabled online`
})

// FR-023: the filter in force, from the URL (`status` legacy, `filter` chip) or
// the responsive default of a bare visit.
const wide = ref(true)
let mq: MediaQueryList | null = null
const onMq = () => { if (mq) wide.value = mq.matches }
onMounted(() => {
  if (typeof window === 'undefined' || !window.matchMedia) return
  mq = window.matchMedia('(min-width: 768px)')
  wide.value = mq.matches
  mq.addEventListener?.('change', onMq)
})
onBeforeUnmount(() => mq?.removeEventListener?.('change', onMq))

const filter = computed<ServerFilter>(() => resolveFilter({
  filter: route.query.filter,
  status: route.query.status,
  hasQ: route.query.q !== undefined,
  hasProfile: route.query.profile !== undefined,
  wide: wide.value,
  notOnlineCount: counts.value.notOnline,
}))

const chips = computed<{ value: ServerChip; label: string; count: number }[]>(() => [
  { value: 'not-online', label: 'Not online', count: counts.value.notOnline },
  { value: 'all', label: 'All', count: counts.value.total },
  { value: 'online', label: 'Online', count: counts.value.online },
  { value: 'disabled', label: 'Disabled', count: counts.value.disabled },
])
const legacyItems = computed<{ value: LegacyServerFilter; label: string; count: number }[]>(() => [
  { value: 'connected', label: 'Connected', count: counts.value.connected },
  { value: 'enabled', label: 'Enabled', count: counts.value.enabled },
  { value: 'quarantined', label: 'Quarantined', count: counts.value.quarantined },
  { value: 'needs_review', label: 'Needs review', count: counts.value.needsReview },
])
const legacyActiveLabel = computed(() => legacyItems.value.find(i => i.value === filter.value)?.label ?? '')

const moreFilters = ref<HTMLDetailsElement | null>(null)
// A chip click clears `status` and keeps `q` and `profile`; All is written
// explicitly so it is not mistaken for the phone default.
function selectChip(value: ServerChip) {
  scopeQuery.set({ filter: value, status: undefined })
  if (moreFilters.value) moreFilters.value.open = false
}
function selectLegacy(value: LegacyServerFilter) {
  if (filter.value === value) { selectChip('all'); return }
  scopeQuery.set({ status: value, filter: undefined })
  if (moreFilters.value) moreFilters.value.open = false
}

const EMPTY_TEXT: Record<ServerFilter, string> = {
  all: 'No servers available',
  'not-online': 'No enabled servers are offline',
  online: 'No servers are online',
  disabled: 'No servers are disabled',
  connected: 'No connected servers available',
  enabled: 'No enabled servers available',
  quarantined: 'No quarantined servers available',
  needs_review: 'No servers need review',
}
const emptyFilterText = computed(() => EMPTY_TEXT[filter.value])

// One shared clock for "last call" ages (a per-row timer would be 300 timers).
const nowMs = ref(Date.now())
let nowTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => { nowTimer = setInterval(() => { nowMs.value = Math.floor(Date.now() / 30_000) * 30_000 }, 30_000) })
onBeforeUnmount(() => { if (nowTimer !== undefined) clearInterval(nowTimer) })

const filteredServers = computed(() =>
  sortServers(rows.value.filter(s => matchesFilter(s, filter.value) && matchesSearch(s, searchQuery.value))))

async function refreshServers() {
  await serversStore.fetchServers()
  if (profileActive.value) await loadScoped(true)
  void loadActivitySummary()
}

async function scanAllServers() {
  scanAllRunning.value = true
  try {
    const res = await api.scanAll()
    if (res.success) {
      systemStore.addToast({
        type: 'success',
        title: 'Batch Scan Started',
        message: 'Scanning all servers. Check the Security page for progress.',
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Scan Failed',
        message: res.error || 'Failed to start batch scan',
      })
    }
  } catch (e: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Scan Failed',
      message: e.message || 'Failed to start batch scan',
    })
  } finally {
    scanAllRunning.value = false
  }
}

// Servers hints
const serversHints = computed<Hint[]>(() => {
  return [
    {
      icon: 'plus',
      title: 'Add New MCP Servers',
      description: 'Multiple ways to add servers to MCPProxy',
      sections: [
        {
          title: 'Add HTTP/HTTPS server',
          codeBlock: {
            language: 'bash',
            code: `# Add a remote MCP server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"my-server","url":"https://api.example.com/mcp","protocol":"http","enabled":true}'`
          }
        },
        {
          title: 'Add stdio server (npx)',
          codeBlock: {
            language: 'bash',
            code: `# Add an npm-based MCP server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"filesystem","command":"npx","args_json":"[\\"@modelcontextprotocol/server-filesystem\\"]","protocol":"stdio","enabled":true}'`
          }
        },
        {
          title: 'Add stdio server (uvx)',
          codeBlock: {
            language: 'bash',
            code: `# Add a Python-based MCP server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"python-server","command":"uvx","args_json":"[\\"mcp-server-package\\"]","protocol":"stdio","enabled":true}'`
          }
        }
      ]
    },
    {
      icon: 'wrench',
      title: 'Manage Servers via CLI',
      description: 'Common server management operations',
      sections: [
        {
          title: 'List all servers',
          codeBlock: {
            language: 'bash',
            code: `# View all upstream servers\nmcpproxy upstream list`
          }
        },
        {
          title: 'Enable/disable server',
          codeBlock: {
            language: 'bash',
            code: `# Disable a server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"server-name","enabled":false}'\n\n# Enable a server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"server-name","enabled":true}'`
          }
        },
        {
          title: 'Remove server',
          codeBlock: {
            language: 'bash',
            code: `# Delete a server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"delete","name":"server-name"}'`
          }
        }
      ]
    },
    {
      icon: 'bot',
      title: 'Use LLM Agents to Manage Servers',
      description: 'Let AI agents help you configure MCPProxy',
      sections: [
        {
          title: 'Example LLM prompts',
          list: [
            'Add the GitHub MCP server from @modelcontextprotocol/server-github to my configuration',
            'Show me all quarantined servers and help me review them',
            'Disable all servers that haven\'t been used in the last 24 hours',
            'Find and add MCP servers for working with Slack'
          ]
        }
      ]
    }
  ]
})
</script>

<style scoped>
.servers-table__head {
  display: grid;
  grid-template-columns: var(--servers-cols);
  column-gap: 12px;
  padding: 8px 12px;
  font-size: 12px;
  line-height: 16px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: color-mix(in oklab, currentColor 60%, transparent);
  border-bottom: 1px solid var(--color-base-300, oklch(var(--b3)));
}
@media (max-width: 767px) {
  .servers-table__head { display: none; }
}
</style>
