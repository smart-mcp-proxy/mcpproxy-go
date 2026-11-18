<template>
  <section class="space-y-4 border border-base-300 rounded-lg p-4" data-test="skill-detail">
    <header class="space-y-1">
      <div class="flex flex-wrap items-center gap-2">
        <h3 class="font-semibold text-lg break-all" data-test="skill-name" v-text="skill.name || skill.path"></h3>
        <span class="badge badge-sm" :class="chipClass" data-test="skill-detail-chip" v-text="chip.label"></span>
      </div>
      <p class="text-xs text-base-content/60">from the server, not verified</p>
      <!-- v-text: every string below was written by the upstream server. -->
      <p class="whitespace-pre-wrap break-words text-sm" data-test="skill-description" v-text="skill.description"></p>
      <p v-if="skill.unusable_reason" class="text-sm text-warning" data-test="skill-unusable-reason">
        Unusable: <span v-text="skill.unusable_reason"></span>
      </p>
      <p v-if="skill.approved_at" class="text-xs text-base-content/60">
        Approved <span v-text="formatDateTime(skill.approved_at)"></span><span v-if="skill.approved_by"> by <span v-text="skill.approved_by"></span></span>
      </p>
    </header>

    <div class="flex flex-wrap gap-2" data-test="skill-actions">
      <button v-if="canApprove" class="btn btn-sm btn-primary" data-test="skill-approve" :disabled="busy || stale" @click="emit('approve')">
        {{ skill.status === 'changed' ? 'Approve changes' : 'Approve' }}
      </button>
      <button v-if="!skill.blocked" class="btn btn-sm btn-outline btn-error" data-test="skill-block" :disabled="busy" @click="emit('block')">Block</button>
      <button v-else class="btn btn-sm btn-outline" data-test="skill-unblock" :disabled="busy" @click="emit('unblock')">Unblock</button>
    </div>

    <div>
      <h4 class="font-medium text-sm mb-1">Files ({{ skill.files.length }}, {{ formatSkillSize(skill.total_size) }})</h4>
      <ul class="text-sm divide-y divide-base-200">
        <li v-for="f in skill.files" :key="f.path" :data-test="`skill-file-${f.path}`" class="flex flex-wrap items-center gap-2 py-1">
          <code class="break-all" v-text="f.path"></code>
          <span class="text-xs text-base-content/60">{{ formatSkillSize(f.size) }}</span>
          <span class="text-xs text-base-content/60 font-mono" :title="f.digest" v-text="shortDigest(f.digest)"></span>
          <span v-if="!f.text" class="badge badge-ghost badge-xs">binary, not shown</span>
          <button v-else class="btn btn-xs btn-ghost" :data-test="`skill-view-${f.path}`" @click="viewFile(f.path, 'current')">View</button>
        </li>
      </ul>
    </div>

    <div v-if="viewer" class="space-y-2" data-test="skill-viewer">
      <div class="flex flex-wrap items-center gap-2">
        <code class="text-sm break-all" v-text="viewer.file"></code>
        <!-- A copy is offered only when the listing names the file in it (an added file has no approved copy, a removed one no current copy). -->
        <div v-if="hasCopy(viewer.file, 'current') && hasCopy(viewer.file, 'approved')" class="join">
          <button class="btn btn-xs join-item" :class="viewer.copy === 'current' ? 'btn-active' : ''" data-test="skill-copy-current" @click="viewFile(viewer.file, 'current')">Current</button>
          <button class="btn btn-xs join-item" :class="viewer.copy === 'approved' ? 'btn-active' : ''" data-test="skill-copy-approved" @click="viewFile(viewer.file, 'approved')">Approved</button>
        </div>
        <span v-else class="badge badge-ghost badge-xs" data-test="skill-viewer-copy">{{ viewer.copy === 'approved' ? 'approved copy' : 'current copy' }}</span>
        <button class="btn btn-xs btn-ghost" data-test="skill-viewer-close" @click="viewer = null">Close</button>
      </div>
      <pre class="whitespace-pre-wrap break-words text-xs bg-base-200 rounded p-2 max-h-96 overflow-auto" data-test="skill-viewer-text" v-text="viewer.text"></pre>
      <div v-if="viewer.loading" class="loading loading-spinner loading-xs"></div>
      <p v-if="viewer.error" class="text-sm text-error" data-test="skill-viewer-error" v-text="viewer.error"></p>
      <button v-if="viewer.next !== undefined && !viewer.loading" class="btn btn-xs" data-test="skill-load-more" @click="loadMore">Load more</button>
    </div>

    <div v-if="diffRows" class="space-y-2" data-test="skill-diff">
      <h4 class="font-medium text-sm">Changes since the approved copy</h4>
      <ul class="text-sm divide-y divide-base-200">
        <li v-for="r in diffRows" :key="r.path" :data-test="`skill-diff-${r.path}`" :data-state="r.state" class="flex flex-wrap items-center gap-2 py-1">
          <span class="badge badge-xs" :class="diffBadge[r.state]">{{ r.state }}</span>
          <code class="break-all" v-text="r.path"></code>
          <button v-if="r.state === 'changed' && r.before?.text && r.after?.text" class="btn btn-xs btn-ghost" :data-test="`skill-line-diff-${r.path}`" @click="showLineDiff(r)">Show line diff</button>
          <!-- Whenever the approved copy is text, whatever the current file is (removed, or replaced by a binary file). -->
          <button v-if="r.state !== 'same' && r.before?.text" class="btn btn-xs btn-ghost" :data-test="`skill-view-approved-${r.path}`" @click="viewFile(r.path, 'approved')">View approved</button>
        </li>
      </ul>
      <div v-if="lineDiffState" class="space-y-1" data-test="skill-line-diff">
        <code class="text-xs break-all" v-text="lineDiffState.path"></code>
        <div v-if="lineDiffState.loading" class="loading loading-spinner loading-xs"></div>
        <p v-if="lineDiffState.note" class="text-sm text-base-content/70" data-test="skill-line-diff-note" v-text="lineDiffState.note"></p>
        <pre v-if="lineDiffState.ops" class="text-xs bg-base-200 rounded p-2 max-h-96 overflow-auto"><template v-for="(l, i) in lineDiffState.ops" :key="i"><span data-test="skill-diff-line" :data-op="l.op" class="block whitespace-pre-wrap break-words" :class="lineClass[l.op]" v-text="l.text"></span></template></pre>
      </div>
    </div>

    <div class="space-y-1" data-test="skill-scan">
      <h4 class="font-medium text-sm">Scan</h4>
      <!-- `scan` is guarded: a listing for a scoped caller carries none. -->
      <p class="text-sm">
        Verdict: <strong v-text="scan?.verdict || 'not scanned'"></strong>
        <span v-if="scan?.scanned_at" class="text-xs text-base-content/60"> at {{ formatDateTime(scan.scanned_at) }}</span>
      </p>
      <p v-if="scan?.verdict && !scan.complete" class="text-sm text-warning">
        Scan incomplete<span v-if="scan.incomplete">: <span v-text="scan.incomplete"></span></span>
      </p>
      <ul v-if="scan?.findings?.length" class="text-sm space-y-1">
        <li v-for="(f, i) in scan.findings" :key="i" data-test="skill-finding" class="border-l-2 border-warning pl-2">
          <code v-text="f.file"></code> @ {{ f.offset }}
          <span class="badge badge-xs ml-1" v-text="f.severity"></span>
          <code class="ml-1" v-text="f.rule"></code>
          <pre class="whitespace-pre-wrap break-words text-xs text-base-content/70" v-text="f.evidence"></pre>
        </li>
      </ul>
      <p v-if="scan?.findings_truncated" class="text-xs text-base-content/60" data-test="skill-findings-truncated">More findings exist than are listed.</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import api from '@/services/api'
import type { SkillCopy, SkillEntry } from '@/types/api'
import { formatDateTime } from '@/utils/datetime'
import {
  skillStateChip, skillFileDiff, lineDiff, readWholeSkillFile, skillFileDigest, skillPartMatches,
  SKILL_CONTENT_CHANGED, SKILL_CONTENT_CHANGED_MESSAGE, type FileDiffRow, type LineOp,
} from '@/utils/skills'

// `skill` is the version the reviewer opened (SkillsPanel keeps it fixed until
// the reviewer re-opens); `stale` says the listing has moved on since.
const props = defineProps<{ serverName: string; skill: SkillEntry; busy?: boolean; stale?: boolean }>()
const emit = defineEmits<{
  (e: 'approve'): void; (e: 'block'): void; (e: 'unblock'): void
  /** A part of another version came back: the listing on screen is out of date. */
  (e: 'content-changed'): void
}>()

/** Files larger than this (either copy) are not line-diffed in the browser. */
const LINE_DIFF_MAX_BYTES = 256 * 1024

const chip = computed(() => skillStateChip(props.skill))
const scan = computed(() => props.skill.scan as SkillEntry['scan'] | undefined)
const chipClass = computed(() => `badge-${({ error: 'error', warning: 'warning', info: 'info', success: 'success', neutral: 'ghost' })[chip.value.tone]}`)
const canApprove = computed(() => !props.skill.blocked && (props.skill.status === 'pending' || props.skill.status === 'changed'))
const diffRows = computed<FileDiffRow[] | null>(() => props.skill.approved_files ? skillFileDiff(props.skill.approved_files, props.skill.files) : null)
const diffBadge = { added: 'badge-success', removed: 'badge-error', changed: 'badge-warning', same: 'badge-ghost' } as const
const lineClass = { same: '', add: 'bg-success/20', del: 'bg-error/20' } as const

function shortDigest(d: string) { return d.replace(/^sha256:/, '').slice(0, 12) }
function formatSkillSize(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}

// digest: the one the listing on screen names for this file and copy. Every
// part must carry it (and answer the offset asked for), or the text is dropped.
interface Viewer { file: string; copy: SkillCopy; digest: string; text: string; next?: number; loading: boolean; error: string }
const viewer = ref<Viewer | null>(null)
interface LineDiffState { path: string; loading: boolean; note: string; ops?: LineOp[] }
const lineDiffState = ref<LineDiffState | null>(null)

// A reload that moved the skill's content, or a different skill, drops every
// fetched text: it described a copy that is no longer the one on screen.
let generation = 0
watch(() => [props.serverName, props.skill.path, props.skill.current_hash, props.skill.approved_hash], () => {
  generation++
  viewer.value = null
  lineDiffState.value = null
})

async function fetchPart(file: string, copy: SkillCopy, offset: number) {
  const gen = generation
  const res = await api.getSkillContent(props.serverName, { skill: props.skill.path, file, copy, offset })
  return { res, stale: gen !== generation }
}

function hasCopy(file: string, copy: SkillCopy) { return skillFileDigest(props.skill, file, copy) !== undefined }

async function viewFile(file: string, copy: SkillCopy) {
  // reactive() so the identity checks below compare the proxy the template sees.
  const v = reactive<Viewer>({ file, copy, digest: skillFileDigest(props.skill, file, copy) ?? '', text: '', loading: true, error: '' })
  viewer.value = v
  if (!v.digest) { v.loading = false; v.error = 'This copy of the file is not listed.'; return }
  await loadInto(v, 0)
}

async function loadMore() {
  const v = viewer.value
  if (v && v.next !== undefined) await loadInto(v, v.next)
}

async function loadInto(v: Viewer, offset: number) {
  v.loading = true
  v.error = ''
  const { res, stale } = await fetchPart(v.file, v.copy, offset)
  if (stale || viewer.value !== v) return
  v.loading = false
  if (!res.success || !res.data) { v.error = res.error || 'Could not read the file'; return }
  if (!skillPartMatches(res.data, v.digest, offset)) {
    // Never join parts of two versions, and never show a copy other than the one listed.
    v.text = ''
    v.next = undefined
    v.error = SKILL_CONTENT_CHANGED_MESSAGE
    emit('content-changed')
    return
  }
  v.text += res.data.text
  v.next = res.data.next_offset ?? undefined
}

async function showLineDiff(r: FileDiffRow) {
  const gen = generation
  const state = reactive<LineDiffState>({ path: r.path, loading: true, note: '' })
  lineDiffState.value = state
  if ((r.before?.size ?? 0) > LINE_DIFF_MAX_BYTES || (r.after?.size ?? 0) > LINE_DIFF_MAX_BYTES) {
    state.loading = false
    state.note = 'This file is too large to compare in the browser. Open the approved and current copies in the viewer instead.'
    return
  }
  const read = (copy: SkillCopy, digest: string) => readWholeSkillFile(offset =>
    api.getSkillContent(props.serverName, { skill: props.skill.path, file: r.path, copy, offset }), digest)
  const [before, after] = await Promise.all([read('approved', r.before?.digest ?? ''), read('current', r.after?.digest ?? '')])
  if (gen !== generation || lineDiffState.value !== state) return
  state.loading = false
  if (!before.success || !after.success) {
    state.note = (!before.success ? before.error : !after.success ? after.error : '') || 'Could not read the file'
    if ((!before.success && before.errorCode === SKILL_CONTENT_CHANGED) || (!after.success && after.errorCode === SKILL_CONTENT_CHANGED)) emit('content-changed')
    return
  }
  if (before.capped || after.capped) { state.note = 'This file is too large to compare in the browser. Open the approved and current copies in the viewer instead.'; return }
  const result = lineDiff(before.text, after.text)
  if (result.tooLarge) { state.note = 'This file is too large to compare in the browser. Open the approved and current copies in the viewer instead.'; return }
  state.ops = result.ops
}
</script>
