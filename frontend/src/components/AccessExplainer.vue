<template>
  <BaseDialog :open="open" :title="title ?? `Explain access${subjectLabel ? ' — ' + subjectLabel : ''}`" wide test-id="access-explainer" @close="emit('close')">
    <p v-if="note" class="text-sm opacity-80" data-test="explain-note">{{ note }}</p>
    <form class="flex flex-wrap items-end gap-2" @submit.prevent="run">
      <div class="form-control flex-1 min-w-[14rem]">
        <label class="label" for="explain-tool"><span class="label-text font-medium">Tool</span></label>
        <input
          id="explain-tool"
          v-model.trim="toolInput"
          list="explain-tool-options"
          class="input input-bordered input-sm w-full font-mono"
          placeholder="server:tool"
          autocomplete="off"
          data-test="explain-tool-input"
        />
        <datalist id="explain-tool-options" data-test="explain-tool-options">
          <option v-for="name in toolNames" :key="name" :value="name" />
        </datalist>
      </div>
      <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !toolInput" data-test="explain-run">
        <span v-if="busy" class="loading loading-spinner loading-xs" />
        Explain
      </button>
    </form>

    <div v-if="busy && !result" class="space-y-2" aria-busy="true" data-test="explain-skeleton">
      <div v-for="n in 4" :key="n" class="skeleton h-5 w-full" />
    </div>
    <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="explain-error">{{ error }}</div>

    <div v-if="result" class="space-y-3" aria-live="polite" data-test="explain-result">
      <div class="alert text-sm" :class="verdictClass" data-test="explain-verdict">
        <span><strong>{{ verdictTitle }}</strong> &mdash; <code>{{ result.tool }}</code> for {{ resultSubject }}<template v-if="result.profile?.name"> on profile <strong>{{ profiles.titleFor(result.profile.name) }}</strong> ({{ result.profile.source }})</template>.</span>
      </div>
      <ol class="space-y-1" data-test="explain-steps">
        <li v-for="(step, index) in result.steps" :key="step.step + index" class="flex items-start gap-2 text-sm" :data-test="`explain-step-${step.step}`">
          <span class="badge badge-sm shrink-0" :class="statusClass(step.status)">
            <span aria-hidden="true">{{ statusIcon(step.status) }}</span>&nbsp;{{ statusText(step.status) }}
          </span>
          <span class="font-medium">{{ STEP_LABELS[step.step] ?? step.step }}</span>
          <span v-if="step.detail" class="opacity-70">{{ detailText(step.detail) }}</span>
        </li>
      </ol>
      <div v-if="result.fixes?.length" class="space-y-1" data-test="explain-fixes">
        <p class="text-sm font-medium">What you can do</p>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="(fix, index) in result.fixes"
            :key="fix.action + fix.target + index"
            type="button"
            class="btn btn-xs btn-outline"
            :data-test="`explain-fix-${fix.action}`"
            @click="follow(fix)"
          >{{ fix.label }}</button>
        </div>
      </div>
    </div>
    <template #actions>
      <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Close</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import BaseDialog from '@/components/BaseDialog.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { useScopeQuery } from '@/composables/useScopeQuery'
import { CONNECT_CLIENT_EVENT } from '@/navigation/navModel'
import { STEP_LABELS, describeError, reasonText } from '@/utils/profiles'
import type { AccessExplanation, ExplainSubjectQuery } from '@/types/api'

// Spec 108-i T099 / FR-046: "Why can't this client use this tool?" The steps are
// the canonical enforcement chain in order, each with an icon AND text; the
// fixes are the server's, one button each, in its order. Every fix navigates or
// opens the dialog that previews first; nothing here changes access.
const props = defineProps<{
  open: boolean
  subject: { kind: 'client' | 'token' | 'profile' | 'anonymous'; name?: string }
  tool?: string
  // Spec 108-j J8: an entry point (a blocked Activity row) can retitle the
  // dialog and say what the verdict is evaluated against; the explainer is live,
  // never a replay of the moment of the call.
  title?: string
  note?: string
}>()
const emit = defineEmits<{ (e: 'close'): void }>()
const router = useRouter()
const profiles = useProfilesStore()
const scope = useScopeQuery('clients')

const toolInput = ref('')
const toolNames = ref<string[]>([])
const busy = ref(false)
const error = ref('')
const result = ref<AccessExplanation | null>(null)

const subjectLabel = computed(() => (props.subject.kind === 'anonymous' ? 'anonymous callers' : props.subject.name ?? ''))
const resultSubject = computed(() => {
  const s = result.value?.subject
  if (!s) return subjectLabel.value
  return s.kind === 'anonymous' ? 'anonymous callers' : `${s.kind} ${s.name ?? ''}`.trim()
})

// Every explain takes a ticket and only the latest applies: closing and
// reopening the still-mounted dialog during a pending request must not let the
// old answer repopulate the reset dialog (#1446 F5.2).
let runTicket = 0

watch(() => props.open, open => {
  runTicket++
  busy.value = false
  if (!open) return
  toolInput.value = props.tool ?? ''
  result.value = null
  error.value = ''
  void loadTools()
  if (props.tool) void run()
})

async function loadTools() {
  try {
    const response = await api.getGlobalTools()
    const tools = response.success && response.data ? response.data.tools ?? [] : []
    toolNames.value = tools.map(tool => `${tool.server_name}:${tool.name}`).sort()
  } catch {
    toolNames.value = []
  }
}

async function run() {
  if (!toolInput.value) return
  const ticket = ++runTicket
  busy.value = true
  error.value = ''
  const query: ExplainSubjectQuery = { tool: toolInput.value }
  if (props.subject.kind === 'client') query.client = props.subject.name
  else if (props.subject.kind === 'token') query.token = props.subject.name
  else if (props.subject.kind === 'profile') query.profile = props.subject.name
  else query.anonymous = true
  try {
    const explained = await api.explainAccess(query)
    if (ticket !== runTicket) return
    result.value = explained
  } catch (err) {
    if (ticket !== runTicket) return
    result.value = null
    error.value = describeError(err)
  } finally {
    if (ticket === runTicket) busy.value = false
  }
}

const verdictTitle = computed(() => {
  switch (result.value?.verdict) {
    case 'allowed': return 'Allowed'
    case 'blocked': return 'Blocked'
    default: return 'Hidden'
  }
})
const verdictClass = computed(() => (result.value?.verdict === 'allowed' ? 'alert-success' : 'alert-warning'))

function statusIcon(status: string): string { return status === 'pass' ? '✓' : status === 'fail' ? '✗' : '–' }
function statusText(status: string): string { return status === 'pass' ? 'Pass' : status === 'fail' ? 'Fail' : 'Skipped' }
function statusClass(status: string): string { return status === 'pass' ? 'badge-success' : status === 'fail' ? 'badge-error' : 'badge-ghost' }
function detailText(detail: string): string { return /^[a-z_]+$/.test(detail) ? reasonText(detail) : detail }

function follow(fix: { action: string; target: string }) {
  const tool = result.value?.tool ?? toolInput.value
  const server = tool.includes(':') ? tool.slice(0, tool.indexOf(':')) : tool
  switch (fix.action) {
    case 'allow_in_profile':
    case 'classify_in_profile':
    case 'add_server_to_profile':
      void router.push(scope.linkTo('profile-editor', { focus: tool }, { name: fix.target }))
      break
    case 'move_client':
      // Deliberately unscoped: a sticky profile/client filter could hide the
      // very client row this fix is about to move.
      void router.push({ name: 'clients', query: { focus: fix.target, move: '1' } })
      break
    case 'edit_token':
      void router.push(scope.linkTo('tokens', { token: fix.target }))
      break
    case 'enable_server':
      void router.push(scope.linkTo('server-detail', {}, { serverName: fix.target || server }))
      break
    case 'approve_tool':
      void router.push(scope.linkTo('review', { server: fix.target || server }))
      break
    case 'change_setting':
      void router.push(scope.linkTo('settings', { tab: 'security', focus: fix.target }))
      break
    case 'reconnect_client':
      window.dispatchEvent(new CustomEvent(CONNECT_CLIENT_EVENT, { detail: { client: fix.target } }))
      break
  }
  emit('close')
}
</script>
