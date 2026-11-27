<!--
  Spec 124 PR-3 (FR-060, FR-061, FR-065): the Activity page's "Sessions" view.

  It lists WORK sessions, one row per agent run, from GET /api/v1/work-sessions:
  the filter ("Failed only", with or without policy blocks), the order and the
  paging are the door's own, so the list is exact however long the log is.
  With a session in the address (`?ws=<id>`) it shows that session's failure
  timeline (SessionTimeline.vue) in place of the list.

  The table of MCP connections this view used to be (GET /sessions) is kept one
  level down, in the collapsed "Client connections" section: SessionConnections.
  The parent still fetches that list and passes it in.
-->
<template>
  <SessionTimeline v-if="timelineId" :key="timelineId" />

  <div v-else class="space-y-4" data-test="work-sessions">
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body py-3 gap-3 max-sm:p-3">
        <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
          <label class="label cursor-pointer gap-2 p-0 max-sm:min-h-11">
            <input
              type="checkbox"
              class="toggle toggle-sm toggle-primary"
              data-test="work-sessions-failed-toggle"
              :checked="failedOnly"
              @change="setFailedOnly(($event.target as HTMLInputElement).checked)"
            />
            <span class="label-text text-sm">Failed only</span>
          </label>
          <label
            v-if="failedOnly"
            class="label cursor-pointer gap-2 p-0 max-sm:min-h-11"
            title="A call MCPProxy itself refused (quarantine, a profile, a rate limit) is a policy block. Off: only sessions with another kind of failure."
          >
            <input
              type="checkbox"
              class="checkbox checkbox-sm"
              data-test="work-sessions-include-blocked"
              :checked="includeBlocked"
              @change="setIncludeBlocked(($event.target as HTMLInputElement).checked)"
            />
            <span class="label-text text-sm">Include blocked</span>
          </label>

          <div class="flex-1"></div>

          <label class="flex items-center gap-2 text-sm">
            <span class="text-base-content/70">Sort</span>
            <select
              class="select select-bordered select-sm max-sm:min-h-11"
              aria-label="Sort sessions"
              data-test="work-sessions-sort"
              :value="sortBy"
              @change="setSort(($event.target as HTMLSelectElement).value)"
            >
              <option value="last_activity">Last activity</option>
              <option value="last_failure">Last failure</option>
            </select>
          </label>
          <button
            type="button"
            class="btn btn-sm btn-outline gap-2 max-sm:min-h-11 border-[var(--ds-border)]"
            data-test="work-sessions-refresh"
            :disabled="loading"
            @click="load()"
          >
            <UiIcon name="refresh" class="w-4 h-4" :class="{ 'animate-spin': loading }" />
            Refresh
          </button>
        </div>

        <p v-if="unattributedNote" class="text-xs text-base-content/70" data-test="work-sessions-unattributed-note">
          {{ unattributedNote }}
        </p>

        <p class="sr-only" role="status" aria-live="polite" aria-atomic="true">
          {{ loading ? 'Loading sessions' : `Showing ${sessions.length} of ${total} sessions` }}
        </p>

        <div v-if="error" class="alert alert-error" data-test="work-sessions-error">
          <UiIcon name="alert" class="w-5 h-5" />
          <span>{{ error }}</span>
          <button type="button" class="btn btn-sm btn-ghost max-sm:min-h-11" data-test="work-sessions-retry" @click="load()">Retry</button>
        </div>

        <div v-else-if="!loaded" class="flex justify-center py-12" data-test="work-sessions-loading">
          <span class="loading loading-spinner loading-lg"></span>
        </div>

        <div v-else-if="sessions.length === 0" class="text-center py-10 text-base-content/70" data-test="work-sessions-empty">
          <p class="text-lg">{{ emptyTitle }}</p>
          <p class="text-sm mt-1">{{ emptyHint }}</p>
        </div>

        <!-- While another filter, order or page is on its way the rows shown
             belong to the previous one: they are dimmed and cannot be opened. -->
        <div
          v-else
          class="overflow-x-auto transition-opacity"
          :class="stale ? 'opacity-50' : ''"
          :inert="stale || undefined"
          :aria-busy="stale ? 'true' : undefined"
          data-test="work-sessions-table-wrap"
        >
          <table class="table table-sm w-full table-fixed max-sm:[&_td]:px-1.5 max-sm:[&_th]:px-1.5" data-test="work-sessions-table">
            <caption class="sr-only">
              Work sessions: one row per agent run, with its calls, failures and the last failure.
            </caption>
            <!-- Nine facts in six cells of two lines, so that the reason of the
                 last failure has room at 1280 px and the table never scrolls
                 sideways (Spec 134 SC-006). Fixed widths go to the cells whose
                 content has a known size; the last-failure cell takes the rest. -->
            <thead>
              <tr>
                <th class="hidden sm:table-cell sm:w-[14%]">Agent token</th>
                <th class="sm:w-[19%]"><span class="sm:hidden">Agent · Client</span><span class="max-sm:hidden">Client · Project</span></th>
                <th class="hidden md:table-cell md:w-[10rem]">Started · Duration</th>
                <th class="w-[3.25rem] sm:w-[4rem] text-right">Calls</th>
                <th class="w-[4.75rem] sm:w-[5.5rem] text-right">Failures</th>
                <th class="hidden sm:table-cell">Last failure · Reason</th>
                <th class="w-[5.5rem] sm:w-[6rem]"><span class="sr-only">Open timeline</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="ws in sessions" :key="ws.id" data-test="work-sessions-row" :data-session="ws.id">
                <td class="hidden sm:table-cell" data-test="ws-token">
                  <router-link
                    v-if="sessionTokenName(ws)"
                    :to="scopeQuery.linkTo('tokens', { token: sessionTokenName(ws) })"
                    class="link link-hover text-sm break-words"
                  >{{ sessionTokenName(ws) }}</router-link>
                  <span v-else class="text-base-content/60" title="No agent token: the call used the API key or a client credential">—</span>
                </td>
                <td>
                  <!-- Below sm the token column folds into this cell. -->
                  <div v-if="sessionTokenName(ws)" class="sm:hidden text-sm break-words" data-test="ws-token-narrow">
                    <router-link :to="scopeQuery.linkTo('tokens', { token: sessionTokenName(ws) })" class="link link-hover">{{ sessionTokenName(ws) }}</router-link>
                  </div>
                  <div data-test="ws-client">
                    <router-link
                      v-if="ws.client_id"
                      :to="scopeQuery.linkTo('clients', { client: ws.client_id })"
                      class="link link-hover text-sm font-medium break-words"
                    >{{ clientLabel(ws.client_id) }}</router-link>
                    <span v-else class="text-sm font-medium break-words">{{ ws.client_name || ws.surface || '—' }}</span>
                    <div v-if="clientNote(ws)" class="text-xs text-base-content/70 truncate" :title="clientNote(ws)">{{ clientNote(ws) }}</div>
                  </div>
                  <!-- The project, when the client disclosed one. -->
                  <div v-if="ws.project_name" class="text-xs text-base-content/70 truncate" :title="`Project: ${ws.project_name}`">
                    project: <span data-test="ws-project">{{ ws.project_name }}</span>
                  </div>
                  <!-- Below sm the last-failure cell folds into this one. -->
                  <div v-if="ws.last_failure" class="sm:hidden text-xs mt-0.5" data-test="ws-reason-narrow">
                    <div class="truncate" :title="reasonText(ws)">{{ reasonText(ws) }}</div>
                    <div class="text-base-content/70 truncate">{{ formatDateTimeMillis(ws.last_failure.at) }}</div>
                  </div>
                  <!-- FR-061: in the cell that shows at every width. -->
                  <div v-if="ws.truncated_start_at" class="mt-0.5">
                    <StateTag
                      tone="review"
                      data-test="ws-truncated"
                      :title="`Rows before ${formatDateTime(ws.truncated_start_at)} were removed by retention`"
                    >Start trimmed</StateTag>
                  </div>
                </td>
                <td class="hidden md:table-cell">
                  <div class="text-sm whitespace-nowrap" data-test="ws-started">{{ formatDateTime(ws.started_at) }}</div>
                  <div class="text-xs text-base-content/70 whitespace-nowrap">
                    <span class="sr-only">Duration: </span><span data-test="ws-duration">{{ formatSessionDuration(ws.duration_ms) }}</span>
                    · {{ ws.status === 'active' ? 'active' : 'ended' }}
                  </div>
                </td>
                <!-- FR-065: the count is the link to the session's rows in the
                     Tool calls view, at every width. -->
                <td class="text-right">
                  <router-link
                    :to="scopeQuery.linkTo('activity', { view: 'calls', session: ws.id })"
                    class="link font-mono max-sm:inline-flex max-sm:min-h-11 max-sm:min-w-11 max-sm:items-center max-sm:justify-end"
                    :title="`Tool calls of this session`"
                    :aria-label="`${ws.attempts} calls: open the Tool calls of session ${sessionName(ws)}`"
                    :data-test="`ws-calls-link-${ws.id}`"
                  ><span data-test="ws-calls">{{ ws.attempts }}</span></router-link>
                </td>
                <td class="text-right" data-test="ws-failures">
                  <span class="font-mono" :class="failureClass(ws)">{{ ws.failures }}</span>
                  <div v-if="ws.policy_blocks > 0" class="text-xs text-base-content/70 whitespace-nowrap">{{ ws.policy_blocks }} blocked</div>
                </td>
                <td class="hidden sm:table-cell">
                  <template v-if="ws.last_failure">
                    <div class="text-sm truncate" data-test="ws-reason" :title="`${reasonText(ws)}${reasonWhere(ws) ? ` (${reasonWhere(ws)})` : ''}`">
                      {{ reasonText(ws) }}<span v-if="reasonWhere(ws)" class="text-base-content/70"> · {{ reasonWhere(ws) }}</span>
                    </div>
                    <div
                      class="text-xs text-base-content/70 truncate"
                      data-test="ws-last-failure"
                      :title="ws.first_failure_at ? `First failure: ${formatDateTimeMillis(ws.first_failure_at)}` : undefined"
                    >
                      {{ formatDateTimeMillis(ws.last_failure.at) }} · {{ formatRelative(ws.last_failure.at) }}
                    </div>
                  </template>
                  <span v-else class="text-base-content/60" data-test="ws-last-failure">—</span>
                </td>
                <td class="text-right">
                  <router-link
                    :to="timelineLink(ws)"
                    class="btn btn-xs btn-primary whitespace-nowrap max-sm:min-h-11"
                    :aria-label="`Open the timeline of session ${sessionName(ws)}`"
                    :data-test="`ws-open-${ws.id}`"
                  >Timeline</router-link>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="loaded && !error && total > 0" class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-sm text-base-content/70" data-test="work-sessions-range">{{ rangeText }}</span>
          <div class="join">
            <button
              type="button"
              class="btn btn-sm join-item max-sm:min-h-11"
              data-test="work-sessions-prev"
              :disabled="shownOffset === 0 || loading"
              @click="goTo(shownOffset - PAGE_SIZE)"
            >Previous</button>
            <button
              type="button"
              class="btn btn-sm join-item max-sm:min-h-11"
              data-test="work-sessions-next"
              :disabled="shownOffset + sessions.length >= total || loading"
              @click="goTo(shownOffset + PAGE_SIZE)"
            >Next</button>
          </div>
        </div>

        <p class="text-xs text-base-content/70">
          A work session is one agent's run of work: one credential, one client, one project, until it has been idle for 30 minutes.
          Counts are lifetime facts and stay exact after retention has removed rows.
        </p>
      </div>
    </div>

    <details class="card bg-base-100 shadow-sm" data-test="sessions-connections">
      <summary class="cursor-pointer px-4 py-3 text-sm font-medium max-sm:min-h-11">
        Client connections ({{ props.sessions.length }})
      </summary>
      <div class="px-4 pb-4">
        <SessionConnections :sessions="props.sessions" />
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useRoute, type RouteLocationRaw } from 'vue-router'
import api from '@/services/api'
import type { MCPSession, WorkSession, WorkSessionListParams } from '@/types/api'
import { useScopeQuery } from '@/composables/useScopeQuery'
import { useScopeLabels, type ScopeName } from '@/composables/useScopeLabels'
import { useAuthStore } from '@/stores/auth'
import { useSystemStore } from '@/stores/system'
import { formatDateTime, formatDateTimeMillis, formatRelative } from '@/utils/datetime'
import { formatSessionDuration, sessionTokenName } from '@/utils/workSessions'
import SessionConnections from '@/components/activity/SessionConnections.vue'
import SessionTimeline from '@/components/activity/SessionTimeline.vue'
import StateTag from '@/components/StateTag.vue'
import UiIcon from '@/components/UiIcon.vue'

const props = withDefaults(defineProps<{
  /** MCP connections (GET /sessions), fetched by the parent. */
  sessions: MCPSession[]
  /** The page's Auto-refresh switch: the list is read again every 30 s. */
  autoRefresh?: boolean
}>(), { autoRefresh: false })

const PAGE_SIZE = 25
const REFRESH_MS = 30_000

const route = useRoute()
const scopeQuery = useScopeQuery('activity')
const { valueLabel, chipLabel } = useScopeLabels()
const authStore = useAuthStore()
const systemStore = useSystemStore()

// ---- address ---------------------------------------------------------------

/** The session whose timeline the address names, if any. */
const timelineId = computed(() => scopeQuery.state.ws || '')
const failedOnly = computed(() => scopeQuery.state.failed === 'true')
/** The door's default is to count policy blocks as failures. */
const includeBlocked = computed(() => scopeQuery.state.include_blocked !== 'false')
type SortKey = 'last_activity' | 'last_failure'
/** "Failed only" reads newest failure first (User Story 1); the whole list, newest activity first. */
const defaultSort = computed<SortKey>(() => (failedOnly.value ? 'last_failure' : 'last_activity'))
/** The address names the order only when it is not the default of the current filter. */
const sortBy = computed<SortKey>(() => {
  const value = scopeQuery.state.sort
  return value === 'last_failure' || value === 'last_activity' ? value : defaultSort.value
})

function setFailedOnly(on: boolean): void {
  // "Include blocked" belongs to "Failed only": it leaves the address with it.
  scopeQuery.set({ failed: on ? 'true' : undefined, include_blocked: on ? scopeQuery.state.include_blocked : undefined })
}
function setIncludeBlocked(on: boolean): void {
  scopeQuery.set({ include_blocked: on ? undefined : 'false' })
}
function setSort(value: string): void {
  const key: SortKey = value === 'last_failure' ? 'last_failure' : 'last_activity'
  scopeQuery.set({ sort: key === defaultSort.value ? undefined : key })
}

/**
 * The same address with this session's timeline open: at its first failure
 * (the door's default), or at its most recent row when it never failed.
 */
function timelineLink(ws: WorkSession): RouteLocationRaw {
  const query: Record<string, string | string[] | null> = {}
  for (const [key, value] of Object.entries(route.query)) {
    if (value === undefined || key === 'at') continue
    query[key] = value as string | string[] | null
  }
  query.view = 'sessions'
  query.ws = ws.id
  if (ws.failures === 0) query.at = 'end'
  return { path: route.path, query }
}

// ---- scope (profile / client / token) --------------------------------------

/** Only the filters the build advertises: `chips` already applies that rule. */
const scopeChips = computed(() =>
  scopeQuery.chips.value.filter(chip => chip.name === 'profile' || chip.name === 'client' || chip.name === 'token'))

/**
 * A filter the work-session door cannot apply for this caller is not sent:
 * `-` (unattributed) has no meaning there, and the profile is not part of what
 * a signed-in user of the server edition may read, so the door would answer a
 * profile filter with nothing at all.
 */
function notApplied(chip: { name: string; value: string }): boolean {
  return chip.value === '-' || (chip.name === 'profile' && authStore.principalKind === 'tenant')
}
const scopeParams = computed(() => {
  const out: Pick<WorkSessionListParams, 'profile' | 'client' | 'token'> = {}
  for (const chip of scopeChips.value) {
    if (!notApplied(chip)) out[chip.name as ScopeName] = chip.value
  }
  return out
})
const unattributedNote = computed(() => {
  const skipped = scopeChips.value.filter(notApplied).map(chip => chipLabel(chip.name as ScopeName, chip.value))
  return skipped.length > 0
    ? `Not applied to work sessions: ${skipped.join(', ')}. The list below is not narrowed by ${skipped.length === 1 ? 'it' : 'them'}.`
    : ''
})
const scopeSummary = computed(() =>
  scopeChips.value
    .filter(chip => !notApplied(chip))
    .map(chip => chipLabel(chip.name as ScopeName, chip.value))
    .join(', '))

// ---- list ------------------------------------------------------------------

const sessions = ref<WorkSession[]>([])
const total = ref(0)
/** The page asked for, and the page the rows on screen belong to. */
const offset = ref(0)
const shownOffset = ref(0)
const shownKey = ref('')
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
let loadSeq = 0

// url-filter-contract.md rule 1: with profile, client or token in the address,
// the first request waits until the build has said which of them it accepts.
// Otherwise it would go out unfiltered and list every session.
let scopeReady = false
let waitingForScope = false
const urlNamesScope = (): boolean =>
  ['profile', 'client', 'token'].some(name => typeof route.query[name] === 'string' && route.query[name] !== '')

async function load(): Promise<void> {
  if (timelineId.value) return
  if (!scopeReady) {
    // One wait: a call that arrives meanwhile (the filters becoming known
    // changes what is asked) is served by the request this one sends.
    if (waitingForScope) return
    waitingForScope = true
    if (urlNamesScope()) await systemStore.waitForScopeFeatures()
    waitingForScope = false
    scopeReady = true
    if (timelineId.value) return
  }
  const seq = ++loadSeq
  const key = listKey.value
  const params: WorkSessionListParams = { limit: PAGE_SIZE, offset: offset.value, sort: sortBy.value, ...scopeParams.value }
  if (failedOnly.value) {
    params.failed = true
    if (!includeBlocked.value) params.include_blocked = false
  }
  loading.value = true
  try {
    const response = await api.getWorkSessions(params)
    if (seq !== loadSeq) return
    const rows = response?.sessions ?? []
    const count = response?.total ?? rows.length
    // The page is past the end (sessions were removed since it was opened):
    // show the last page that exists instead of an empty one.
    if (rows.length === 0 && count > 0 && offset.value > 0) {
      offset.value = Math.floor((count - 1) / PAGE_SIZE) * PAGE_SIZE
      void load()
      return
    }
    sessions.value = rows
    total.value = count
    // The door clamps an offset past the end; follow it so the range is true.
    offset.value = response?.offset ?? offset.value
    shownOffset.value = offset.value
    shownKey.value = key
    error.value = ''
    loaded.value = true
  } catch (err) {
    if (seq !== loadSeq) return
    error.value = err instanceof Error && err.message ? err.message : 'Failed to load sessions'
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function goTo(next: number): void {
  offset.value = Math.max(0, next)
  void load()
}

/** Everything that selects WHICH sessions are listed: a change starts at page one. */
const listKey = computed(() => JSON.stringify([
  failedOnly.value, includeBlocked.value, sortBy.value,
  scopeParams.value.profile ?? '', scopeParams.value.client ?? '', scopeParams.value.token ?? '',
]))
watch(listKey, () => {
  offset.value = 0
  void load()
}, { immediate: true })
/** Another filter, order or page was asked for and has not arrived. */
const stale = computed(() => loading.value && (shownKey.value !== listKey.value || shownOffset.value !== offset.value))

// Opening a timeline replaces the list, and with it the link that had the
// focus. Focus follows: to the timeline's heading, and on the way back to the
// row the timeline was opened from (or to the first control of the list).
let lastOpened = ''
function focusFirst(selectors: string[]): void {
  for (const selector of selectors) {
    const el = document.querySelector<HTMLElement>(selector)
    if (el) {
      el.focus()
      return
    }
  }
}
watch(timelineId, async (id, before) => {
  if (id) {
    loadSeq++ // a list answer still on its way belongs to a view that is gone
    lastOpened = id
    await nextTick()
    focusFirst(['[data-test="timeline-heading"]'])
    return
  }
  if (!before) return
  // Back from a timeline: the same page of the list, read again.
  await load()
  await nextTick()
  focusFirst([`[data-test="ws-open-${lastOpened}"]`, '[data-test="work-sessions-failed-toggle"]'])
})

let timer: ReturnType<typeof setInterval> | null = null
watch(() => props.autoRefresh, on => {
  if (timer) clearInterval(timer)
  timer = on ? setInterval(() => { if (!loading.value) void load() }, REFRESH_MS) : null
}, { immediate: true })
onUnmounted(() => {
  if (timer) clearInterval(timer)
  loadSeq++
})

const rangeText = computed(() => {
  if (sessions.value.length === 0) return `0 of ${total.value}`
  return `${shownOffset.value + 1}–${shownOffset.value + sessions.value.length} of ${total.value}`
})

const emptyTitle = computed(() => {
  if (scopeSummary.value) return `No sessions for ${scopeSummary.value}`
  return failedOnly.value ? 'No failed sessions' : 'No work sessions yet'
})
const emptyHint = computed(() => {
  if (scopeSummary.value) return 'Remove a filter chip above to see more.'
  if (failedOnly.value) {
    return includeBlocked.value
      ? 'No session in the log has a failure. Switch off "Failed only" to see every session.'
      : 'No session has a failure other than a policy block. Switch on "Include blocked" to count those.'
  }
  return 'A session appears here when an agent or a script calls a tool.'
})

// ---- row text --------------------------------------------------------------

const clientLabel = (id: string): string => valueLabel('client', id)

/** The second line of the client cell: what the client reported, and the run name. */
function clientNote(ws: WorkSession): string {
  const parts: string[] = []
  if (ws.client_id && ws.client_name) parts.push(`~${ws.client_name} (reported)`)
  if (ws.run_name) parts.push(`run: ${ws.run_name}`)
  return parts.join(' · ')
}

function sessionName(ws: WorkSession): string {
  return ws.run_name || sessionTokenName(ws) || (ws.client_id ? clientLabel(ws.client_id) : '') || ws.client_name || ws.surface || ws.id
}

/** Red for a failure that is not a policy block, amber when blocks are all there is. */
function failureClass(ws: WorkSession): string {
  if (ws.failures - ws.policy_blocks > 0) return 'ws-failures-real font-semibold'
  return ws.failures > 0 ? 'ws-failures-blocked font-semibold' : 'text-base-content/60'
}

function reasonText(ws: WorkSession): string {
  const failure = ws.last_failure
  if (!failure) return ''
  if (failure.reason) return failure.reason
  if (failure.policy_block) return 'Blocked by MCPProxy'
  return failure.error_class || failure.error_code || 'Failed'
}

function reasonWhere(ws: WorkSession): string {
  const failure = ws.last_failure
  if (!failure) return ''
  const tool = failure.server && failure.tool ? `${failure.server}:${failure.tool}` : (failure.tool || failure.server || '')
  return [failure.error_class, tool].filter(Boolean).join(' · ')
}
</script>

<style scoped>
/* The --status-* tokens exist for the two default themes only. */
.ws-failures-real { color: var(--status-error, var(--tone-error)); }
.ws-failures-blocked { color: var(--status-review, var(--tone-warning)); }
</style>
