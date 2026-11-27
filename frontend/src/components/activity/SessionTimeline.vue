<!--
  Spec 124 PR-3 (FR-061..FR-064): one page of a work session's failure
  timeline, from GET /api/v1/work-sessions/{id}/timeline.

  The address is the state: `?view=sessions&ws=<id>&at=<position>` names the
  session and the row the page is positioned at (`first_failure` when absent,
  `failure:<n>`, `row:<activity id>`, `end`), so a timeline can be shared and
  reloaded. Stepping to another failure rewrites `at`; loading earlier or later
  rows widens the page around the same position.

  Rows come without argument and response bodies. A row's bodies are fetched
  only when the row is expanded, through the same door (`at=row:<id>` with no
  context and `include_bodies`), so a scoped caller reads them under the same
  rules as the page.
-->
<template>
  <div class="session-timeline space-y-4" data-test="session-timeline">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
      <router-link
        :to="listLink"
        class="btn btn-sm btn-outline gap-1 max-sm:min-h-11 border-[var(--ds-border)]"
        data-test="timeline-back"
      >
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
        </svg>
        Sessions
      </router-link>
      <h2 class="text-lg font-semibold min-w-0 break-all outline-none" tabindex="-1" data-test="timeline-heading">Session timeline</h2>
      <code class="text-xs bg-base-200 px-2 py-1 rounded break-all" data-test="timeline-session-id">{{ sessionId }}</code>
    </div>

    <!-- A session that is not there, is not the caller's, or has nothing in
         the caller's reach: the door answers the same 404 for all three. -->
    <div v-if="notFound" class="card bg-base-100 shadow-sm" data-test="timeline-not-found">
      <div class="card-body items-center text-center py-10">
        <p class="text-lg">Session not found</p>
        <p class="text-sm text-base-content/70">
          It may have been removed by retention, or the link names a session you cannot see.
        </p>
        <router-link :to="listLink" class="btn btn-sm btn-primary mt-2 max-sm:min-h-11" data-test="timeline-back-to-list">
          Back to sessions
        </router-link>
      </div>
    </div>

    <div v-else-if="error" class="alert alert-error" data-test="timeline-error">
      <UiIcon name="alert" class="w-5 h-5" />
      <span>{{ error }}</span>
      <div class="flex flex-wrap gap-2">
        <button
          v-if="at !== TIMELINE_AT_DEFAULT"
          type="button"
          class="btn btn-sm btn-ghost max-sm:min-h-11"
          data-test="timeline-open-first-failure"
          @click="goTo(TIMELINE_AT_DEFAULT)"
        >Open at the first failure</button>
        <button type="button" class="btn btn-sm btn-ghost max-sm:min-h-11" data-test="timeline-retry" @click="load()">Retry</button>
      </div>
    </div>

    <div v-else-if="!page" class="flex justify-center py-12" data-test="timeline-loading">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <template v-else>
      <!-- What the list row shows, in full, plus what only this screen has. -->
      <div class="card bg-base-100 shadow-sm">
        <div class="card-body py-3 gap-3 max-sm:p-3">
          <dl class="grid grid-cols-2 sm:grid-cols-3 xl:grid-cols-6 gap-x-4 gap-y-2 text-sm" data-test="timeline-facts">
            <div v-for="fact in facts" :key="fact.label" class="min-w-0">
              <dt class="text-xs text-base-content/70">{{ fact.label }}</dt>
              <dd class="break-words">
                <router-link v-if="fact.to" :to="fact.to" class="link link-hover">{{ fact.value }}</router-link>
                <template v-else>{{ fact.value }}</template>
                <span v-if="fact.note" class="text-xs text-base-content/70"> · {{ fact.note }}</span>
              </dd>
            </div>
          </dl>

          <div class="flex flex-wrap items-center gap-2">
            <StateTag :tone="session.status === 'active' ? 'ok' : 'off'">{{ session.status === 'active' ? 'Active' : 'Ended' }}</StateTag>
            <StateTag v-if="session.rebuilt" tone="off" title="The summary of this session was rebuilt from the rows still in the log">Rebuilt from rows</StateTag>
            <StateTag v-if="session.ownerless" tone="off" title="Recorded before every row carried its credential; no single owner">No single owner</StateTag>
            <div class="flex-1"></div>
            <!-- FR-065: the session's rows in the Tool calls view. -->
            <router-link
              :to="scopeQuery.linkTo('activity', { view: 'calls', session: sessionId })"
              class="btn btn-sm btn-outline max-sm:min-h-11 border-[var(--ds-border)]"
              data-test="timeline-calls-link"
            >Tool calls of this session</router-link>
          </div>

          <!-- The page's "View as" and token chips narrow the list of sessions.
               A timeline is one session, whoever made it. -->
          <p v-if="scopeNote" class="text-xs text-base-content/70" data-test="timeline-scope-note">{{ scopeNote }}</p>
          <div
            v-if="session.truncated_start_at"
            class="soft-panel tone-review rounded px-3 py-2 text-sm"
            data-test="timeline-truncated-start"
          >
            The start of this session is no longer in the log: rows before
            {{ formatDateTimeMillis(session.truncated_start_at) }} were removed by retention.
          </div>
          <div
            v-if="page.earlier_failures_not_stored"
            class="soft-panel tone-review rounded px-3 py-2 text-sm"
            data-test="timeline-earlier-failures"
          >
            This session had {{ page.lifetime_failures }} {{ page.lifetime_failures === 1 ? 'failure' : 'failures' }} in all.
            {{ page.failure_total }} {{ page.failure_total === 1 ? 'is' : 'are' }} still in the log; the rows of the earlier ones were removed by retention.
          </div>
        </div>
      </div>

      <div class="card bg-base-100 shadow-sm">
        <div class="card-body py-3 gap-3 max-sm:p-3">
          <div class="flex flex-wrap items-center gap-2">
            <div class="join">
              <button
                type="button"
                class="btn btn-sm join-item max-sm:min-h-11"
                data-test="timeline-prev-failure"
                :disabled="!navigation.prev || loading"
                @click="navigation.prev && goTo(navigation.prev)"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
                </svg>
                Previous failure
              </button>
              <button
                type="button"
                class="btn btn-sm join-item max-sm:min-h-11"
                data-test="timeline-next-failure"
                :disabled="!navigation.next || loading"
                @click="navigation.next && goTo(navigation.next)"
              >
                Next failure
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
                </svg>
              </button>
            </div>
            <span class="text-sm font-medium" role="status" aria-live="polite" data-test="timeline-failure-label">{{ navigation.label }}</span>
            <span v-if="loading" class="loading loading-spinner loading-xs" aria-hidden="true"></span>
            <div class="flex-1"></div>
            <span class="text-xs text-base-content/70" data-test="timeline-range">{{ rangeText }}</span>
            <!-- An active session grows: read the same page again. -->
            <button
              type="button"
              class="btn btn-sm btn-outline gap-2 max-sm:min-h-11 border-[var(--ds-border)]"
              data-test="timeline-refresh"
              :disabled="loading"
              @click="load()"
            >
              <UiIcon name="refresh" class="w-4 h-4" :class="{ 'animate-spin': loading }" />
              Refresh
            </button>
            <button
              type="button"
              class="btn btn-sm btn-outline max-sm:min-h-11 border-[var(--ds-border)]"
              data-test="timeline-open-end"
              :disabled="at === 'end' || loading"
              @click="goTo('end')"
            >Latest rows</button>
          </div>

          <div v-if="page.has_before" class="flex justify-center">
            <button
              type="button"
              class="btn btn-sm btn-ghost max-sm:min-h-11"
              data-test="timeline-load-earlier"
              :disabled="loading"
              @click="loadEarlier"
            >{{ before >= TIMELINE_CONTEXT_MAX ? 'Continue from the earliest row shown' : 'Load earlier rows' }}</button>
          </div>

          <div class="overflow-x-auto">
            <table class="table table-sm w-full table-fixed max-sm:[&_td]:px-1.5 max-sm:[&_th]:px-1.5" data-test="timeline-table">
              <caption class="sr-only">
                Session timeline in time order: tool calls, upstream events and the start and end of the session.
              </caption>
              <thead>
                <tr>
                  <th class="w-[5.75rem] sm:w-[6.5rem]">Time</th>
                  <th>What happened</th>
                  <!-- Below sm the result is printed inside "What happened". -->
                  <th class="hidden sm:table-cell sm:w-28">Result</th>
                  <th class="hidden sm:table-cell sm:w-24">Duration</th>
                  <th class="w-12 max-sm:w-14"><span class="sr-only">Details</span></th>
                </tr>
              </thead>
              <tbody>
                <template v-for="row in page.rows" :key="row.index">
                  <tr
                    data-test="timeline-row"
                    :data-index="row.index"
                    :data-kind="row.kind"
                    :data-failed="row.failed ? 'true' : undefined"
                    :data-position="row.index === page.position ? 'true' : undefined"
                    :aria-current="row.index === page.position ? 'true' : undefined"
                    :class="rowClass(row)"
                  >
                    <td class="whitespace-nowrap font-mono text-xs align-top" :title="formatDateTimeMillis(row.at)">
                      <span data-test="timeline-time">{{ formatTimeMillis(row.at) }}</span>
                    </td>

                    <!-- Start and end of the session: derived rows, nothing stored. -->
                    <td v-if="isBoundary(row)" :colspan="columnCount - 1" class="text-sm text-base-content/70 break-words">
                      {{ row.kind === 'session_start' ? 'Session started' : 'Session ended' }}
                      <span class="text-xs">· {{ formatDateTimeMillis(row.at) }}</span>
                    </td>

                    <template v-else>
                      <td class="min-w-0 align-top">
                        <!-- An upstream event: why a call one row later could not work. -->
                        <div v-if="row.kind === 'upstream_event'" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                          <UiIcon name="plug" class="w-4 h-4 shrink-0" />
                          <span class="font-medium break-all">{{ row.activity?.server_name || 'upstream' }}</span>
                          <span>{{ upstreamEventLabel(row.activity) }}</span>
                          <!-- Repeats within a minute are one row at the time of the
                               first; the last one may be what a later call ran into. -->
                          <span v-if="eventCount(row) > 1" class="text-xs" data-test="timeline-event-repeats">
                            ×{{ eventCount(row) }}<template v-if="eventLastAt(row)">, last at {{ eventLastAt(row) }}</template>
                          </span>
                          <StateTag v-if="row.precedes_session" tone="off">before the session started</StateTag>
                        </div>
                        <div v-else class="flex flex-wrap items-center gap-x-2 gap-y-1 min-w-0">
                          <code
                            v-if="toolLabel(row)"
                            class="text-sm bg-base-200 px-2 py-0.5 rounded min-w-0 max-w-full break-all"
                          >{{ toolLabel(row) }}</code>
                          <span v-if="kindLabel(row)" class="text-xs text-base-content/70">{{ kindLabel(row) }}</span>
                        </div>
                        <div v-if="rowMessage(row) || eventCode(row)" class="text-xs mt-0.5 break-words" :class="row.failed ? '' : 'text-base-content/70'">
                          {{ rowMessage(row) }}
                          <code v-if="eventCode(row)" class="ml-1 break-all" data-test="timeline-code">{{ eventCode(row) }}</code>
                        </div>
                        <div class="sm:hidden mt-1" data-test="timeline-result-narrow">
                          <StateTag v-if="resultTone(row)" :tone="resultTone(row)!">{{ resultLabel(row) }}</StateTag>
                          <span v-else class="text-xs text-base-content/70">{{ resultLabel(row) }}</span>
                        </div>
                      </td>
                      <td class="hidden sm:table-cell align-top">
                        <StateTag v-if="resultTone(row)" :tone="resultTone(row)!">{{ resultLabel(row) }}</StateTag>
                        <span v-else class="text-sm text-base-content/70">{{ resultLabel(row) }}</span>
                      </td>
                      <td class="hidden sm:table-cell whitespace-nowrap text-sm align-top">
                        {{ durationText(row) }}
                      </td>
                      <td class="align-top">
                        <button
                          type="button"
                          class="btn btn-xs btn-ghost max-sm:min-h-11 max-sm:min-w-11"
                          :aria-expanded="isExpanded(row)"
                          :aria-controls="`timeline-detail-${row.index}`"
                          :aria-label="`${isExpanded(row) ? 'Hide' : 'Show'} details of the row at ${formatTimeMillis(row.at)}`"
                          :data-test="`timeline-expand-${row.index}`"
                          @click="toggle(row)"
                        >
                          <svg class="w-4 h-4 transition-transform" :class="isExpanded(row) ? 'rotate-90' : ''" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
                          </svg>
                        </button>
                      </td>
                    </template>
                  </tr>

                  <tr v-if="!isBoundary(row) && isExpanded(row)" class="timeline-row-detail">
                    <td :colspan="columnCount" :id="`timeline-detail-${row.index}`" :data-test="`timeline-detail-${row.index}`">
                      <dl class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-4 gap-y-1 text-xs mb-2">
                        <div v-for="item in detailFacts(row)" :key="item.label" class="min-w-0">
                          <dt class="inline text-base-content/70">{{ item.label }}: </dt>
                          <dd class="inline break-all">{{ item.value }}</dd>
                        </div>
                      </dl>
                      <p v-if="row.activity?.error_message && row.kind !== 'upstream_event'" class="text-sm break-words mb-2">{{ row.activity.error_message }}</p>

                      <div :data-test="`timeline-bodies-${row.index}`">
                        <p v-if="row.bodies_removed || bodyState(row)?.removed" class="text-sm text-base-content/70">
                          The arguments and the response of this row were removed by retention to keep the row itself.
                        </p>
                        <p v-else-if="row.kind === 'upstream_event'" class="text-sm text-base-content/70">
                          An upstream event has no arguments or response.
                        </p>
                        <p v-else-if="bodyState(row)?.loading" class="text-sm text-base-content/70">
                          <span class="loading loading-spinner loading-xs"></span> Loading arguments and response…
                        </p>
                        <div v-else-if="bodyState(row)?.error" class="text-sm">
                          {{ bodyState(row)?.error }}
                          <button type="button" class="btn btn-xs btn-ghost max-sm:min-h-11" @click="loadBodies(row)">Retry</button>
                        </div>
                        <template v-else-if="bodyState(row)?.record">
                          <div class="text-xs font-semibold mb-1">Arguments</div>
                          <pre v-if="hasArguments(row)" class="bg-base-200 rounded p-2 text-xs overflow-auto max-h-64 whitespace-pre-wrap break-all">{{ argumentsText(row) }}</pre>
                          <p v-else class="text-sm text-base-content/70">No arguments recorded.</p>
                          <div class="text-xs font-semibold mt-2 mb-1">
                            Response
                            <span v-if="bodyState(row)?.record?.response_truncated" class="font-normal text-base-content/70">(shortened when it was stored)</span>
                          </div>
                          <pre v-if="bodyState(row)?.record?.response" class="bg-base-200 rounded p-2 text-xs overflow-auto max-h-64 whitespace-pre-wrap break-all">{{ bodyState(row)?.record?.response }}</pre>
                          <p v-else class="text-sm text-base-content/70">No response recorded.</p>
                        </template>
                      </div>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>

          <div v-if="page.has_after" class="flex justify-center">
            <button
              type="button"
              class="btn btn-sm btn-ghost max-sm:min-h-11"
              data-test="timeline-load-later"
              :disabled="loading"
              @click="loadLater"
            >{{ after >= TIMELINE_CONTEXT_MAX ? 'Continue from the latest row shown' : 'Load later rows' }}</button>
          </div>
        </div>
      </div>

      <!-- FR-064: the same view from the command line. -->
      <div class="card bg-base-100 shadow-sm">
        <div class="card-body py-3 gap-2 max-sm:p-3">
          <div class="text-xs text-base-content/70">The same view from the command line</div>
          <div class="flex flex-wrap items-center gap-2">
            <code class="flex-1 min-w-0 bg-base-200 rounded px-2 py-1.5 text-xs break-all select-all" data-test="timeline-cli">{{ cliCommand }}</code>
            <button
              type="button"
              class="btn btn-sm btn-outline gap-1 max-sm:min-h-11 border-[var(--ds-border)]"
              data-test="timeline-copy-cli"
              @click="copyCli"
            >
              <UiIcon :name="copied === 'yes' ? 'check' : 'copy'" class="w-4 h-4" />
              {{ copied === 'yes' ? 'Copied' : copied === 'no' ? 'Not copied: select the text' : 'Copy as CLI command' }}
            </button>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, type RouteLocationRaw } from 'vue-router'
import api from '@/services/api'
import type {
  ActivityRecord,
  WorkSession,
  WorkSessionTimelineResponse,
  WorkSessionTimelineRow,
} from '@/types/api'
import { useScopeQuery } from '@/composables/useScopeQuery'
import { useScopeLabels } from '@/composables/useScopeLabels'
import { useProfilesStore } from '@/stores/profiles'
import { profileEditorLink } from '@/utils/profileRoute'
import { profileSourceLabel } from '@/utils/profiles'
import { formatType, statusPresentation } from '@/utils/activity'
import { formatDateTimeMillis, formatTimeMillis } from '@/utils/datetime'
import type { StatusTone } from '@/utils/healthTone'
import {
  TIMELINE_AT_DEFAULT,
  TIMELINE_CONTEXT_DEFAULT,
  TIMELINE_CONTEXT_MAX,
  TIMELINE_CONTEXT_STEP,
  failureNavigation,
  formatSessionDuration,
  isRecoveryEvent,
  normalizeTimelineAt,
  sessionTokenName,
  timelineCliCommand,
  upstreamEventLabel,
} from '@/utils/workSessions'
import StateTag from '@/components/StateTag.vue'
import UiIcon from '@/components/UiIcon.vue'

const route = useRoute()
const scopeQuery = useScopeQuery('activity')
const profiles = useProfilesStore()
const { valueLabel } = useScopeLabels()

// ---- address ---------------------------------------------------------------

const sessionId = computed(() => scopeQuery.state.ws || '')
/** A malformed position in a hand-edited address reads as the default. */
const at = computed(() => normalizeTimelineAt(scopeQuery.state.at))

/** The Sessions list this timeline was opened from: the same address without it. */
const listLink = computed<RouteLocationRaw>(() => {
  const query: Record<string, string | string[] | null> = {}
  for (const [key, value] of Object.entries(route.query)) {
    if (value === undefined || key === 'ws' || key === 'at') continue
    query[key] = value as string | string[] | null
  }
  query.view = 'sessions'
  return { path: route.path, query }
})

function goTo(position: string): void {
  scopeQuery.set({ at: position === TIMELINE_AT_DEFAULT ? undefined : position })
}

// ---- page ------------------------------------------------------------------

const before = ref(TIMELINE_CONTEXT_DEFAULT)
const after = ref(TIMELINE_CONTEXT_DEFAULT)
const page = ref<WorkSessionTimelineResponse | null>(null)
const loading = ref(false)
const error = ref('')
const notFound = ref(false)
let loadSeq = 0
/** Scroll to the positioned row after a load that moved the position. */
let scrollToPosition = true

const statusOf = (err: unknown): number | undefined =>
  typeof (err as { status?: unknown })?.status === 'number' ? (err as { status: number }).status : undefined

async function load(): Promise<void> {
  const id = sessionId.value
  if (!id) return
  const seq = ++loadSeq
  loading.value = true
  try {
    const response = await api.getWorkSessionTimeline(id, { at: at.value, before: before.value, after: after.value })
    if (seq !== loadSeq) return
    // Spec 124 US1 scenario 6: a session that never failed opens at its most
    // recent row. The door's default is the first failure, and with none it
    // answers with the earliest row, so an address that names no position
    // moves to the end.
    if (!scopeQuery.state.at && response.session.failures === 0 && response.total_rows > 0) {
      goTo('end')
      return
    }
    page.value = response
    error.value = ''
    notFound.value = false
    if (scrollToPosition) {
      scrollToPosition = false
      void nextTick(revealPosition)
    }
  } catch (err) {
    if (seq !== loadSeq) return
    page.value = null
    if (statusOf(err) === 404) {
      notFound.value = true
      error.value = ''
    } else {
      notFound.value = false
      error.value = err instanceof Error && err.message ? err.message : 'Failed to load the timeline'
    }
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function revealPosition(): void {
  const el = document.querySelector('[data-test="timeline-row"][data-position="true"]')
  if (el && typeof (el as HTMLElement).scrollIntoView === 'function') {
    ;(el as HTMLElement).scrollIntoView({ block: 'center' })
  }
}

// A new position keeps the amount of context the page has (US1 scenario 4):
// after "load earlier rows", the next failure opens just as wide.
watch([sessionId, at], () => {
  expanded.clear()
  scrollToPosition = true
  void load()
})

/** The first or the last stored row of the page: where a page past the door's
 * limit continues from. */
function edgeRowAt(edge: 'first' | 'last'): string | undefined {
  const rows = (page.value?.rows ?? []).filter(row => row.activity?.id)
  const row = edge === 'first' ? rows[0] : rows[rows.length - 1]
  return row?.activity?.id ? `row:${row.activity.id}` : undefined
}

function loadEarlier(): void {
  if (before.value >= TIMELINE_CONTEXT_MAX) {
    // The door's limit: continue from the earliest row shown, looking back.
    const anchor = edgeRowAt('first')
    if (!anchor) return
    before.value = TIMELINE_CONTEXT_STEP
    after.value = TIMELINE_CONTEXT_DEFAULT
    goTo(anchor)
    return
  }
  before.value = Math.min(before.value + TIMELINE_CONTEXT_STEP, TIMELINE_CONTEXT_MAX)
  void load()
}

function loadLater(): void {
  if (after.value >= TIMELINE_CONTEXT_MAX) {
    const anchor = edgeRowAt('last')
    if (!anchor) return
    before.value = TIMELINE_CONTEXT_DEFAULT
    after.value = TIMELINE_CONTEXT_STEP
    goTo(anchor)
    return
  }
  after.value = Math.min(after.value + TIMELINE_CONTEXT_STEP, TIMELINE_CONTEXT_MAX)
  void load()
}

const session = computed<WorkSession>(() => page.value!.session)

const navigation = computed(() =>
  page.value ? failureNavigation(page.value) : { label: '', prev: undefined, next: undefined })

const rangeText = computed(() => {
  const rows = page.value?.rows ?? []
  if (rows.length === 0) return `0 of ${page.value?.total_rows ?? 0} rows`
  return `rows ${rows[0].index + 1}–${rows[rows.length - 1].index + 1} of ${page.value!.total_rows}`
})

// ---- session facts ---------------------------------------------------------

interface Fact { label: string; value: string; note?: string; to?: RouteLocationRaw }

const facts = computed<Fact[]>(() => {
  const s = session.value
  const out: Fact[] = []
  const token = sessionTokenName(s)
  out.push(token
    ? { label: 'Agent token', value: token, to: scopeQuery.linkTo('tokens', { token }) }
    : { label: 'Agent token', value: '—' })
  if (s.client_id) {
    out.push({
      label: 'Client',
      value: valueLabel('client', s.client_id),
      note: s.client_name ? `~${s.client_name} (reported)` : undefined,
      to: scopeQuery.linkTo('clients', { client: s.client_id }),
    })
  } else {
    out.push({ label: 'Client', value: s.client_name || s.surface || '—' })
  }
  out.push({ label: 'Project', value: s.project_name || '—' })
  if (s.run_name) out.push({ label: 'Run', value: s.run_name })
  if (s.surface) out.push({ label: 'Surface', value: s.surface })
  // Administrators only: absent for a scoped caller.
  if (s.profile) {
    const source = profileSourceLabel(s.profile_source)
    out.push({ label: 'Profile', value: profiles.titleFor(s.profile), note: source || undefined, to: profileEditorLink(s.profile) })
  }
  out.push({ label: 'Started', value: formatDateTimeMillis(s.started_at) })
  out.push(s.ended_at
    ? { label: 'Ended', value: formatDateTimeMillis(s.ended_at) }
    : { label: 'Last activity', value: formatDateTimeMillis(s.last_activity_at) })
  out.push({
    label: 'Duration',
    value: formatSessionDuration(s.duration_ms),
    note: s.connection_count ? `${s.connection_count} ${s.connection_count === 1 ? 'connection' : 'connections'}` : undefined,
  })
  out.push({ label: 'Calls', value: String(s.attempts) })
  out.push({
    label: 'Failures',
    value: String(s.failures),
    note: s.policy_blocks > 0 ? `${s.policy_blocks} blocked` : undefined,
  })
  // Lifetime facts: they stay when retention has removed the failed rows.
  if (s.first_failure_at) out.push({ label: 'First failure', value: formatDateTimeMillis(s.first_failure_at) })
  if (s.last_failure) {
    const f = s.last_failure
    const where = f.server && f.tool ? `${f.server}:${f.tool}` : (f.tool || f.server || '')
    const why = f.reason || (f.policy_block ? 'Blocked by MCPProxy' : '')
    out.push({
      label: 'Last failure',
      value: formatDateTimeMillis(f.at),
      note: [why, f.error_class, f.error_code, where].filter(Boolean).join(' · ') || undefined,
    })
  }
  return out
})

const scopeNote = computed(() => {
  const applied = scopeQuery.chips.value.some(chip => chip.name === 'profile' || chip.name === 'client' || chip.name === 'token')
  return applied ? 'The profile, client and token filters of the page narrow the list of sessions. They do not change this timeline.' : ''
})

// ---- rows ------------------------------------------------------------------

// Result and Duration fold away below `sm` (640 px). A cell that spans the row
// must span exactly the columns that are shown: a wider colspan adds phantom
// columns, and a fixed-layout table then gives them the subject's width.
const SM_QUERY = '(min-width: 640px)'
const media = typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(SM_QUERY) : null
const wide = ref(media ? media.matches : true)
const onMedia = (event: MediaQueryListEvent): void => { wide.value = event.matches }
media?.addEventListener?.('change', onMedia)
const columnCount = computed(() => (wide.value ? 5 : 3))

const isBoundary = (row: WorkSessionTimelineRow): boolean =>
  row.kind === 'session_start' || row.kind === 'session_end'

function rowClass(row: WorkSessionTimelineRow): string[] {
  const classes: string[] = []
  if (isBoundary(row)) classes.push('timeline-row-boundary')
  else if (row.kind === 'upstream_event') classes.push('timeline-row-event', isRecoveryEvent(row.activity) ? 'timeline-row-recovery' : '')
  if (row.failed) classes.push(row.policy_block ? 'timeline-row-blocked' : 'timeline-row-failed')
  if (page.value && row.index === page.value.position) classes.push('timeline-row-position')
  return classes.filter(Boolean)
}

function toolLabel(row: WorkSessionTimelineRow): string {
  const a = row.activity
  if (!a) return ''
  // A built-in tool (call_tool_read, retrieve_tools...) is MCPProxy's own: the
  // server on its row is the one it was asked about, not where it ran.
  if (row.kind === 'internal_call' && a.tool_name) return a.tool_name
  if (a.server_name && a.tool_name) return `${a.server_name}:${a.tool_name}`
  return a.tool_name || a.server_name || ''
}

/** A word for the rows that are not a plain tool call. */
function kindLabel(row: WorkSessionTimelineRow): string {
  switch (row.kind) {
    case 'call': return ''
    case 'internal_call': return row.activity?.server_name ? `built-in tool · ${row.activity.server_name}` : 'built-in tool'
    case 'policy_decision': return 'policy decision'
    case 'prompt': return 'prompt'
    default: return row.activity ? formatType(row.activity.type) : ''
  }
}

const eventCount = (row: WorkSessionTimelineRow): number => {
  const count = Number(row.activity?.metadata?.count)
  return Number.isFinite(count) ? count : 0
}
const eventLastAt = (row: WorkSessionTimelineRow): string => {
  const last = row.activity?.metadata?.last_at
  return typeof last === 'string' && last ? formatTimeMillis(last, '') : ''
}
/** The diagnostic code of a row: an event keeps it as `code`, a failed call as `error_code`. */
const eventCode = (row: WorkSessionTimelineRow): string => {
  const meta = row.activity?.metadata
  const code = row.kind === 'upstream_event' ? meta?.code : meta?.error_code
  return typeof code === 'string' ? code : ''
}

/** The line under the row's subject: the fixed message of an event, the error of a failed call. */
function rowMessage(row: WorkSessionTimelineRow): string {
  const a = row.activity
  if (!a) return ''
  // A problem carries its fixed text as the row's error; a recovery, which is
  // no error, carries it in the metadata.
  if (row.kind === 'upstream_event') return a.error_message || (typeof a.metadata?.message === 'string' ? a.metadata.message : '')
  if (row.failed || a.status === 'error' || a.status === 'blocked') return a.error_message || ''
  return ''
}

function resultLabel(row: WorkSessionTimelineRow): string {
  if (row.kind === 'upstream_event') return isRecoveryEvent(row.activity) ? 'Recovered' : 'Event'
  if (row.policy_block) return 'Blocked'
  return row.activity ? statusPresentation(row.activity.status).label : ''
}

/** Colour is spent on what needs attention: a success is plain text. */
function resultTone(row: WorkSessionTimelineRow): StatusTone | undefined {
  if (row.kind === 'upstream_event') return isRecoveryEvent(row.activity) ? 'ok' : 'review'
  if (row.policy_block) return 'review'
  const tone = row.activity ? statusPresentation(row.activity.status).tone : 'muted'
  if (tone === 'error') return 'error'
  if (tone === 'warning') return 'review'
  if (tone === 'neutral') return 'off'
  return row.failed ? 'error' : undefined
}

function durationText(row: WorkSessionTimelineRow): string {
  const ms = row.activity?.duration_ms
  if (typeof ms !== 'number' || row.kind === 'upstream_event') return ''
  return ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(2)}s`
}

function detailFacts(row: WorkSessionTimelineRow): { label: string; value: string }[] {
  const a = row.activity
  if (!a) return []
  const out: { label: string; value: string }[] = [
    { label: 'Time', value: formatDateTimeMillis(row.at) },
    { label: 'Type', value: formatType(a.type) },
    { label: 'Status', value: statusPresentation(a.status).label },
  ]
  if (a.error_class) out.push({ label: 'Error class', value: a.error_class })
  if (row.kind !== 'upstream_event' && eventCode(row)) out.push({ label: 'Error code', value: eventCode(row) })
  // The Duration column folds away below sm; the details always have it.
  if (durationText(row)) out.push({ label: 'Duration', value: durationText(row) })
  if (a.fault_domain) out.push({ label: 'Fault', value: a.fault_domain })
  if (a.upstream_http_status) out.push({ label: 'Upstream HTTP status', value: String(a.upstream_http_status) })
  if (a.block_reason) out.push({ label: 'Block reason', value: a.block_reason })
  if (row.kind === 'upstream_event') {
    if (typeof a.metadata?.kind === 'string') out.push({ label: 'Event', value: a.metadata.kind })
    if (eventCode(row)) out.push({ label: 'Code', value: eventCode(row) })
    if (eventCount(row) > 1) {
      out.push({ label: 'Repeats', value: String(eventCount(row)) })
      if (a.metadata?.first_at) out.push({ label: 'First at', value: formatDateTimeMillis(a.metadata.first_at) })
      if (a.metadata?.last_at) out.push({ label: 'Last at', value: formatDateTimeMillis(a.metadata.last_at) })
    }
  }
  if (a.request_id) out.push({ label: 'Request id', value: a.request_id })
  out.push({ label: 'Row id', value: a.id })
  return out
}

// ---- bodies, on request ----------------------------------------------------

interface BodyState { loading: boolean; error?: string; record?: ActivityRecord; removed?: boolean }
const expanded = reactive(new Set<string>())
const bodies = reactive(new Map<string, BodyState>())

const rowKey = (row: WorkSessionTimelineRow): string => row.activity?.id ?? `i${row.index}`
const isExpanded = (row: WorkSessionTimelineRow): boolean => expanded.has(rowKey(row))
const bodyState = (row: WorkSessionTimelineRow): BodyState | undefined => bodies.get(rowKey(row))
/** True once the bodies of any row were shown: the CLI needs --include-bodies for the same view. */
const bodiesShown = computed(() => [...expanded].some(key => !!bodies.get(key)?.record))

function toggle(row: WorkSessionTimelineRow): void {
  const key = rowKey(row)
  if (expanded.has(key)) {
    expanded.delete(key)
    return
  }
  expanded.add(key)
  if (!bodies.has(key)) void loadBodies(row)
}

async function loadBodies(row: WorkSessionTimelineRow): Promise<void> {
  const id = row.activity?.id
  // Retention kept the row without its bodies, and an event has none: no request.
  if (!id || row.bodies_removed || row.kind === 'upstream_event') return
  const key = rowKey(row)
  const session = sessionId.value
  bodies.set(key, { loading: true })
  try {
    const response = await api.getWorkSessionTimeline(session, { at: `row:${id}`, before: 0, after: 0, include_bodies: true })
    if (session !== sessionId.value) return
    const fresh = response?.rows?.find(r => r.activity?.id === id)
    if (!fresh?.activity) bodies.set(key, { loading: false, error: 'This row is no longer in the log.' })
    // Retention may have removed the bodies since the page was read.
    else if (fresh.bodies_removed) bodies.set(key, { loading: false, removed: true })
    else bodies.set(key, { loading: false, record: fresh.activity })
  } catch (err) {
    if (session !== sessionId.value) return
    bodies.set(key, {
      loading: false,
      error: statusOf(err) === 400 || statusOf(err) === 404
        ? 'This row is no longer in the log.'
        : (err instanceof Error && err.message ? err.message : 'Failed to load the arguments and response'),
    })
  }
}

const hasArguments = (row: WorkSessionTimelineRow): boolean => {
  const args = bodyState(row)?.record?.arguments
  return !!args && Object.keys(args).length > 0
}
const argumentsText = (row: WorkSessionTimelineRow): string =>
  JSON.stringify(bodyState(row)?.record?.arguments ?? {}, null, 2)

// ---- the same view from the command line (FR-064) --------------------------

const cliCommand = computed(() => timelineCliCommand({
  id: sessionId.value,
  at: at.value,
  before: before.value,
  after: after.value,
  includeBodies: bodiesShown.value,
}))

/** '' until the button is used; then whether the browser took the text. */
const copied = ref<'' | 'yes' | 'no'>('')
let copiedTimer: ReturnType<typeof setTimeout> | null = null
async function copyCli(): Promise<void> {
  try {
    await navigator.clipboard.writeText(cliCommand.value)
    copied.value = 'yes'
  } catch {
    // The browser refused (no permission, not a secure context): the command
    // is selectable next to the button, and the button says so.
    copied.value = 'no'
  }
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => { copied.value = '' }, copied.value === 'yes' ? 1500 : 4000)
}

onUnmounted(() => {
  loadSeq++
  if (copiedTimer) clearTimeout(copiedTimer)
  media?.removeEventListener?.('change', onMedia)
})

void load()
</script>

<style scoped>
/* Rows are told apart by more than colour: a failed row has a solid bar on its
   leading edge, the positioned row an outline, an event its icon and wording.
   The --status-* tokens exist for the two default themes only; in the other
   themes the row takes a tint of the theme's own semantic colour instead. */
.session-timeline {
  --tl-error: var(--status-error, var(--tone-error));
  --tl-error-soft: var(--status-error-soft, color-mix(in oklab, var(--color-error) 16%, var(--color-base-100)));
  --tl-review: var(--status-review, var(--tone-warning));
  --tl-review-soft: var(--status-review-soft, color-mix(in oklab, var(--color-warning) 18%, var(--color-base-100)));
  --tl-ok: var(--status-ok, var(--tone-success));
  --tl-ok-soft: var(--status-ok-soft, color-mix(in oklab, var(--color-success) 16%, var(--color-base-100)));
}
.timeline-row-failed > td { background: var(--tl-error-soft); }
.timeline-row-failed > td:first-child { box-shadow: inset 3px 0 0 var(--tl-error); }
.timeline-row-blocked > td { background: var(--tl-review-soft); }
.timeline-row-blocked > td:first-child { box-shadow: inset 3px 0 0 var(--tl-review); }
.timeline-row-event > td { background: var(--tl-review-soft); }
.timeline-row-event > td:first-child { box-shadow: inset 3px 0 0 var(--tl-review); }
.timeline-row-event.timeline-row-recovery > td { background: var(--tl-ok-soft); }
.timeline-row-event.timeline-row-recovery > td:first-child { box-shadow: inset 3px 0 0 var(--tl-ok); }
.timeline-row-boundary > td { background: var(--color-base-200); border-top: 1px solid var(--ds-border, var(--color-base-300)); border-bottom: 1px solid var(--ds-border, var(--color-base-300)); }
.timeline-row-position { outline: 2px solid var(--color-primary); outline-offset: -2px; }
.timeline-row-detail > td { background: var(--color-base-200); }
</style>
