<template>
  <section class="space-y-4" data-test="skills-panel">
    <div v-if="loading && !listing" class="text-center py-8"><span class="loading loading-spinner"></span></div>

    <div v-else-if="error && !listing" class="alert alert-error" data-test="skills-error">
      <span v-text="error"></span>
      <button class="btn btn-sm" data-test="skills-retry" @click="load">Retry</button>
    </div>

    <p v-else-if="listing && !listing.enabled" class="text-base-content/70" data-test="skills-empty-disabled">
      Skills over MCP is turned off. Set <code>enable_skills</code> to true in the configuration to read and review skills from upstream servers.
    </p>

    <template v-else-if="listing">
      <div class="flex flex-wrap items-center gap-3 text-sm" data-test="skills-header">
        <button class="btn btn-sm" data-test="skills-refresh" :disabled="refreshing || !listing.supported" @click="refresh">
          <span v-if="refreshing" class="loading loading-spinner loading-xs"></span>
          Refresh
        </button>
        <span class="text-base-content/60">
          <template v-if="listing.last_refresh">Last refresh {{ formatDateTime(listing.last_refresh) }}</template>
          <template v-else>Not refreshed yet</template>
        </span>
        <span v-if="listing.expose_skills === false" class="badge badge-ghost badge-sm" data-test="skills-excluded">Excluded from agents (expose_skills: false)</span>
        <span v-if="listing.truncated" class="badge badge-warning badge-sm" data-test="skills-truncated">List truncated</span>
      </div>
      <p v-if="listing.error" class="text-sm text-error" data-test="skills-last-error" v-text="listing.error"></p>
      <!-- A reload that failed while a listing is on screen: the listing stays, the error is shown. -->
      <p v-if="error" class="text-sm text-error" data-test="skills-load-error" v-text="error"></p>
    </template>

    <div v-if="notice" class="alert alert-info text-sm" data-test="skills-notice" role="status">
      <span v-text="notice"></span>
      <button class="btn btn-xs btn-ghost" @click="notice = ''">Dismiss</button>
    </div>

    <template v-if="listing && listing.enabled">
      <p v-if="!listing.supported && listing.skills.length === 0" class="text-base-content/70" data-test="skills-empty-unsupported">
        This server does not announce the skills extension.
      </p>
      <p v-else-if="listing.skills.length === 0" class="text-base-content/70" data-test="skills-empty-none">
        This server lists no skills.
      </p>
      <ul v-else class="divide-y divide-base-200 border border-base-300 rounded-lg" data-test="skills-list">
        <li v-for="s in listing.skills" :key="s.path">
          <button
            type="button"
            class="w-full text-left px-3 py-2 flex flex-wrap items-center gap-2 hover:bg-base-200"
            :class="s.path === selectedPath ? 'bg-base-200' : ''"
            :data-test="`skill-row-${s.path}`"
            @click="open(s)"
          >
            <span class="font-medium break-all" v-text="s.name || s.path"></span>
            <span class="badge badge-sm" :class="chipClass(s)" :data-test="`skill-chip-${s.path}`" v-text="skillStateChip(s).label"></span>
            <span class="text-xs text-base-content/60">{{ s.available ? 'Offered to agents' : 'Not offered' }}</span>
            <span class="text-xs text-base-content/60">{{ s.files?.length ?? 0 }} file{{ (s.files?.length ?? 0) === 1 ? '' : 's' }}</span>
            <span v-if="s.scan?.verdict" class="text-xs text-base-content/60">scan: <span v-text="s.scan.verdict"></span></span>
          </button>
        </li>
      </ul>

      <div v-if="review && review.stale" class="alert alert-warning text-sm" data-test="skill-stale" role="status">
        <span>This skill changed while you were reviewing it. Approve is off until you open the new version.</span>
        <button v-if="liveSelected" class="btn btn-xs" data-test="skill-reopen" @click="open(liveSelected)">Open the new version</button>
      </div>
      <div v-else-if="review && awaitingReload" class="alert alert-info text-sm" data-test="skill-checking" role="status">
        <span>The server's skills changed. Approve is off until the list is reloaded.</span>
        <button class="btn btn-xs" data-test="skill-checking-reload" :disabled="loading" @click="load">Reload</button>
      </div>

      <!-- Keyed by the review: re-opening resets the viewer and the diff. -->
      <SkillDetail
        v-if="review"
        :key="review.token"
        :server-name="serverName"
        :skill="review.entry"
        :busy="busy"
        :stale="review.stale || awaitingReload"
        @approve="approve()"
        @block="setBlocked(true)"
        @unblock="setBlocked(false)"
        @content-changed="onContentChanged"
      />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import api from '@/services/api'
import type { SkillEntry, SkillsListResponse } from '@/types/api'
import { formatDateTime } from '@/utils/datetime'
import { skillStateChip, skillExpectedHashes, skillErrorCode, skillErrorNotice, skillApprovedNotice, type SkillAction } from '@/utils/skills'
import SkillDetail from './SkillDetail.vue'

const props = defineProps<{ serverName: string }>()

const listing = ref<SkillsListResponse | null>(null)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const refreshing = ref(false)
const busy = ref(false)
// A live skills.changed for this server: the records moved, so the version on
// screen may be out of date. Approve is off (for the open review and for one
// opened from the listing still on screen) until a reload for this server
// applies; apply() then clears it, or marks the review stale if its hash moved.
const awaitingReload = ref(false)

// The review in progress: the entry and the incarnation as they were when the
// reviewer opened the skill. A reload never swaps in a new version of the
// content under the reviewer: if the open skill's current_hash (or the
// server's incarnation) moved, the review is marked stale and Approve stays
// off until the reviewer opens the new version. A reload that moved neither
// (a block, a scan result) updates the entry in place.
interface Review { path: string; entry: SkillEntry; incarnation: number; token: number; stale: boolean }
const review = ref<Review | null>(null)
let reviewToken = 0
const selectedPath = computed(() => review.value?.path ?? null)
const liveSelected = computed(() => listing.value?.skills.find(s => s.path === selectedPath.value) ?? null)

// Opening (or re-opening) a skill starts a new review: a notice that answered
// the previous one (e.g. "Approved.") is not carried over to it.
function open(s: SkillEntry) {
  notice.value = ''
  review.value = { path: s.path, entry: s, incarnation: listing.value?.incarnation ?? 0, token: ++reviewToken, stale: false }
}

// The latch: once set, only open() (the reviewer's explicit re-open) clears it;
// apply() never does.
const STALE_CODES = new Set(['review_out_of_date', 'server_replaced'])
function markStale(token: number | undefined) {
  if (review.value && review.value.token === token) review.value.stale = true
}

// SkillDetail read a part of another version: the open review is out of date.
function onContentChanged() {
  markStale(review.value?.token)
  void load()
}

function chipClass(s: SkillEntry) {
  return `badge-${({ error: 'error', warning: 'warning', info: 'info', success: 'success', neutral: 'ghost' })[skillStateChip(s).tone]}`
}

// A response is applied only if it answers the newest request for the server
// still on screen: a late reply for the previous server, or an older reload of
// this one, is dropped.
let visit = 0
let requestSeq = 0
const isCurrent = (server: string, v: number, seq?: number) =>
  server === props.serverName && v === visit && (seq === undefined || seq === requestSeq)

function apply(data: SkillsListResponse) {
  listing.value = data
  error.value = ''
  awaitingReload.value = false
  const r = review.value
  if (!r) return
  const live = data.skills.find(s => s.path === r.path)
  if (!live) review.value = null
  else if (r.stale) return
  else if (live.current_hash === r.entry.current_hash && data.incarnation === r.incarnation) r.entry = live
  else r.stale = true
}

// A thrown request (a network error) is an answer like any other failure: it
// is turned into {success:false} so the spinner and the buttons are reset and
// the error is shown, but only while the request is still the current one.
type Answer<T> = { success: boolean; data?: T; error?: string; errorCode?: string }
async function settle<T>(run: () => Promise<Answer<T>>): Promise<Answer<T>> {
  try {
    return await run()
  } catch (e) {
    return { success: false, error: e instanceof Error && e.message ? e.message : 'The request failed' }
  }
}

// Returns the listing it applied, or null (a failure, or a superseded answer).
async function load(): Promise<SkillsListResponse | null> {
  const server = props.serverName, v = visit, seq = ++requestSeq
  loading.value = true
  const res = await settle(() => api.getServerSkills(server))
  if (!isCurrent(server, v, seq)) return null
  loading.value = false
  if (res.success && res.data) { apply(res.data); return res.data }
  error.value = res.error || 'Could not load skills'
  return null
}

// One refresh in flight per visit: a late answer of an earlier one never
// resets the button of a newer one. A refresh takes its place in the request
// order when it starts (like load()): its listing is applied only while it is
// still the newest request; a load that started later and answered first has
// newer records, so a superseded refresh reloads instead of overwriting them.
let refreshSeq = 0
async function refresh() {
  const server = props.serverName, v = visit, mine = ++refreshSeq, seq = ++requestSeq
  refreshing.value = true
  const res = await settle(() => api.refreshServerSkills(server))
  if (!isCurrent(server, v) || mine !== refreshSeq) return
  refreshing.value = false
  if (res.success && res.data) {
    if (!isCurrent(server, v, seq)) { await load(); return }
    loading.value = false
    apply(res.data)
    return
  }
  // A failed refresh (refresh_superseded, skills_not_supported, skills_disabled,
  // server_replaced, a 500) reloads the listing: the notice says it was reloaded,
  // and the stored records may have moved even though this refresh did not apply.
  notice.value = skillErrorNotice(res.errorCode, res.error, 'refresh')
  await load()
}

// Spec 117 style binding: the approval names the hash and the incarnation of
// the listing on screen. A 409 reloads and tells the operator; it never retries.
// The answer is shown only while the same review is still open: a late answer
// for a skill the reviewer has left is not shown on the next one (the listing is
// still reloaded). A success notice given as a function is worded from the
// listing reloaded after the write (null when that reload did not apply).
// Every write takes an operation number when it starts; only the newest write
// publishes a notice, so a slow reload after an earlier write never replaces
// the notice of a later one (approve, then block: "Blocked." stays).
type DoneNotice = string | ((reloaded: SkillsListResponse | null) => string)
let writeSeq = 0
async function write(action: SkillAction, run: () => Promise<{ success: boolean; error?: string; errorCode?: string }>, done: DoneNotice) {
  const server = props.serverName, v = visit, token = review.value?.token, op = ++writeSeq
  const mayPublish = () => op === writeSeq && isCurrent(server, v)
  busy.value = true
  const res = await settle(run)
  if (!isCurrent(server, v)) return
  busy.value = false
  if (!res.success || typeof done === 'string') {
    if (review.value?.token === token) {
      // The approval routes answer 409 server_replaced with no error_code: the
      // code is read from the message.
      const code = res.success ? undefined : skillErrorCode(res.errorCode, res.error)
      // The server says the open version is out of date: latch it before the
      // reload, so a reload that is pending or fails never re-enables Approve.
      if (code && STALE_CODES.has(code)) markStale(token)
      if (mayPublish()) notice.value = res.success ? done as string : skillErrorNotice(code, res.error, action)
    }
    await load()
    return
  }
  const reloaded = await load()
  // Still the newest write and the same review (or none: the reload dropped a
  // skill that vanished).
  if (mayPublish() && (!review.value || review.value.token === token)) notice.value = done(reloaded)
}

// Approve sends exactly the hash and the incarnation the reviewer opened.
function approve() {
  const r = review.value
  if (!r || r.stale || awaitingReload.value || busy.value) return
  return write(
    'approve',
    () => api.approveSkills(props.serverName, [r.path], skillExpectedHashes([r.entry]), r.incarnation),
    reloaded => skillApprovedNotice(reloaded, r.path),
  )
}

function setBlocked(block: boolean) {
  const r = review.value
  if (!r || busy.value) return
  return write(
    block ? 'block' : 'unblock',
    // Called as methods: the API client's methods use `this`.
    () => (block
      ? api.blockSkills(props.serverName, [r.path], r.incarnation)
      : api.unblockSkills(props.serverName, [r.path], r.incarnation)),
    block ? 'Blocked.' : 'Unblocked.',
  )
}

// The store re-dispatches the core's frame whole ({payload: {server, counts},
// timestamp}), like every other runtime event.
function onSkillsChanged(e: Event) {
  const detail = (e as CustomEvent).detail as { payload?: { server?: string } } | null
  if (detail?.payload?.server !== props.serverName) return
  awaitingReload.value = true
  void load()
}

watch(() => props.serverName, () => {
  visit++
  listing.value = null
  review.value = null
  error.value = ''
  notice.value = ''
  refreshing.value = false
  busy.value = false
  awaitingReload.value = false
  void load()
})

onMounted(() => {
  window.addEventListener('mcpproxy:skills-changed', onSkillsChanged)
  void load()
})
onUnmounted(() => window.removeEventListener('mcpproxy:skills-changed', onSkillsChanged))
</script>
