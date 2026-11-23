<template>
  <div class="max-w-2xl mx-auto space-y-4" data-test="authorize-page">
    <h1 class="text-2xl font-bold">{{ title }}</h1>

    <!-- No admin key in this browser: nothing is requested until one is pasted. -->
    <div v-if="phase === 'nokey'" class="card bg-base-100 shadow" data-test="authorize-no-key">
      <div class="card-body space-y-3">
        <p>Open this request from the MCPProxy tray, or paste the API key.</p>
        <div class="flex gap-2">
          <input
            v-model="pastedKey"
            type="password"
            autocomplete="off"
            class="input input-bordered flex-1"
            aria-label="MCPProxy API key"
            data-test="authorize-key-input"
            @keyup.enter="submitKey"
          />
          <button type="button" class="btn btn-primary" :disabled="!pastedKey.trim()" data-test="authorize-key-submit" @click="submitKey">Continue</button>
        </div>
      </div>
    </div>

    <div v-else-if="phase === 'loading'" class="opacity-70" data-test="authorize-loading">Loading the request…</div>

    <div v-else-if="phase === 'gone'" class="alert alert-warning" role="alert" data-test="authorize-gone">
      This sign-in request has expired or was already handled. Start the sign-in again from the app.
    </div>

    <div v-else-if="phase === 'decided'" class="alert alert-info" role="alert" data-test="authorize-decided">
      This sign-in request was already decided.
    </div>

    <div v-else-if="phase === 'error'" class="alert alert-error" role="alert" data-test="authorize-load-error">
      {{ errorText }}
    </div>

    <div v-else-if="view" class="card bg-base-100 shadow">
      <div class="card-body space-y-4">
        <div class="space-y-1">
          <p data-test="authorize-reported">The app calls itself "{{ view.reported_name }}". MCPProxy cannot verify that.</p>
          <p class="text-sm opacity-70" data-test="authorize-returns-to">Returns to: {{ view.redirect_uri }}</p>
        </div>

        <!-- Per-client address: the client is fixed. -->
        <div v-if="fixed" class="space-y-1" data-test="authorize-client-fixed">
          <span class="font-medium">Client</span>
          <span class="ml-2">{{ fixed.display_name }}</span>
          <span v-if="fixed.has_api_key" class="ml-1 text-sm opacity-70">(already connected with an API key)</span>
        </div>

        <!-- Generic address: the user chooses. -->
        <div v-else class="space-y-2">
          <label class="font-medium" for="authorize-client-select">Client</label>
          <select
            id="authorize-client-select"
            v-model="selected"
            class="select select-bordered w-full"
            data-test="authorize-client-select"
          >
            <option v-for="c in existingClients" :key="c.id" :value="c.id">{{ c.display_name }}</option>
            <option value="__new__">New client</option>
          </select>
          <p v-if="showSuggestion" class="text-sm" data-test="authorize-suggestion-note">Suggested because the app calls itself "{{ view.reported_name }}". MCPProxy cannot verify that.</p>
          <div v-if="selected === '__new__'" class="grid gap-2 sm:grid-cols-2">
            <label class="form-control">
              <span class="label-text text-sm">Client id</span>
              <input v-model.lazy="newId" class="input input-bordered" maxlength="56" data-test="authorize-new-id" />
            </label>
            <label class="form-control">
              <span class="label-text text-sm">Display name</span>
              <input v-model.lazy="newName" class="input input-bordered" maxlength="64" data-test="authorize-new-name" />
            </label>
          </div>
        </div>

        <!-- Profile: shown when the client is bound, asked when it is not. -->
        <div v-if="currentBinding" class="space-y-1" data-test="authorize-binding">
          <span class="font-medium">Profile</span>
          <span class="ml-2">{{ currentBinding.profile || 'All servers' }} ({{ currentBinding.mode }})</span>
        </div>
        <div v-else-if="clientChosen" class="space-y-2" data-test="authorize-profile-ask">
          <label class="font-medium" for="authorize-profile-select">Profile</label>
          <select
            id="authorize-profile-select"
            v-model="profileSel"
            class="select select-bordered w-full"
            data-test="authorize-profile-select"
          >
            <option v-if="view.profiles.length > 0" :value="UNSET" disabled>Choose a profile…</option>
            <option v-for="p in view.profiles" :key="p.name" :value="p.name">{{ p.name }}</option>
            <option :value="ALL">All servers</option>
          </select>
          <label v-if="profileSel !== UNSET && profileSel !== ALL" class="flex items-center gap-2 text-sm">
            <input v-model="lock" type="checkbox" class="checkbox checkbox-sm" data-test="authorize-lock" />
            Lock this client to the profile
          </label>
        </div>

        <p v-if="preview?.access" class="text-sm" data-test="authorize-access">{{ accessSentence }}</p>

        <label v-if="view.previous_login" class="flex items-center gap-2 text-sm">
          <input v-model="replace" type="checkbox" class="checkbox checkbox-sm" data-test="authorize-replace" />
          Replace the previous browser login (approved {{ approvedOn }})
        </label>

        <GuardRefusal v-if="guard" :refusal="guard" />

        <div v-if="changedNote" class="alert alert-info text-sm" role="status" data-test="authorize-changed">
          The client's access changed while this page was open. It was reloaded: review it and approve again.
        </div>
        <div v-if="errorText" class="alert alert-error text-sm" role="alert" data-test="authorize-error">{{ errorText }}</div>

        <div class="card-actions justify-end">
          <button type="button" class="btn" :disabled="submitting" data-test="authorize-deny" @click="deny">Deny</button>
          <button type="button" class="btn btn-primary" :disabled="approveDisabled" data-test="authorize-approve" @click="approve">Approve</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// Spec 132 consent page for a browser sign-in request. Everything the app
// reports (name, redirect URI) is untrusted and is rendered as text only. The
// reported name never decides anything: the page sends the client the user
// sees selected, and the server never applies a suggestion.
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import api, { ApiError } from '@/services/api'
import { navigateTo } from '@/utils/navigate'
import GuardRefusal from '@/components/GuardRefusal.vue'
import type { OAuthApprovalBody, OAuthAuthorization, OAuthGuardVerdict, OAuthPreview } from '@/types/api'

const UNSET = '__unset__'
const ALL = '__all__'
const NEW = '__new__'

type Phase = 'nokey' | 'loading' | 'ready' | 'gone' | 'decided' | 'error'

const route = useRoute()
const requestId = computed(() => (typeof route.query.request === 'string' ? route.query.request : ''))

const phase = ref<Phase>('loading')
const view = ref<OAuthAuthorization | null>(null)
const pastedKey = ref('')
const selected = ref('')
const newId = ref('')
const newName = ref('')
const profileSel = ref<string>(UNSET)
const lock = ref(true)
const replace = ref(false)
const preview = ref<OAuthPreview | null>(null)
const previewing = ref(false)
const guardOverride = ref<OAuthGuardVerdict | null>(null)
const errorText = ref('')
const changedNote = ref(false)
const submitting = ref(false)
let previewSeq = 0

const fixed = computed(() => (view.value?.address_kind === 'client' ? view.value.client ?? null : null))
const existingClients = computed(() => view.value?.existing_clients ?? [])
const selectedExisting = computed(() =>
  view.value?.address_kind === 'generic' && selected.value && selected.value !== NEW
    ? existingClients.value.find((c) => c.id === selected.value) ?? null
    : null,
)
const currentBinding = computed(() => (fixed.value ? fixed.value.binding ?? null : selectedExisting.value?.binding ?? null))
const isNewGeneric = computed(() => view.value?.address_kind === 'generic' && selected.value === NEW)
const clientChosen = computed(() => !!fixed.value || !!selected.value)
const showSuggestion = computed(
  () => !!view.value?.suggested_client && selected.value === view.value.suggested_client.id,
)

const title = computed(() => {
  const name = fixed.value?.display_name
  return name ? `Sign-in request for ${name}` : 'Sign-in request'
})

const clientDisplayName = computed(() => {
  if (fixed.value) return fixed.value.display_name
  if (selectedExisting.value) return selectedExisting.value.display_name
  return newName.value.trim() || newId.value.trim()
})

// undefined = not chosen yet; '' = All servers.
const profileValue = computed<string | undefined>(() => {
  if (currentBinding.value) return undefined // kept, never asked
  if (profileSel.value === ALL) return ''
  if (profileSel.value !== UNSET) return profileSel.value
  return (view.value?.profiles.length ?? 0) > 0 ? undefined : ''
})

const complete = computed(() => {
  if (!view.value || !clientChosen.value) return false
  if (isNewGeneric.value && !newId.value.trim()) return false
  return !!currentBinding.value || profileValue.value !== undefined
})

function buildBody(): OAuthApprovalBody {
  const body: OAuthApprovalBody = {}
  if (fixed.value) body.client = { id: fixed.value.id }
  else if (isNewGeneric.value) {
    const n: { id: string; display_name?: string } = { id: newId.value.trim() }
    if (newName.value.trim()) n.display_name = newName.value.trim()
    body.client = { new: n }
  } else if (selected.value) body.client = { id: selected.value }
  if (currentBinding.value) body.binding_revision = currentBinding.value.revision
  else if (profileValue.value !== undefined) {
    body.profile = profileValue.value
    if (profileValue.value !== '') body.mode = lock.value ? 'locked' : 'switchable'
  }
  if (view.value?.previous_login && replace.value && !isNewGeneric.value) body.replace_grant = view.value.previous_login.grant_id
  return body
}

const previewKey = computed(() => (phase.value === 'ready' && complete.value ? JSON.stringify(buildBody()) : null))

const guard = computed(() => {
  if (guardOverride.value) return refusalOf(guardOverride.value)
  if (preview.value) return preview.value.guard ? refusalOf(preview.value.guard) : null
  return view.value?.guard ? refusalOf(view.value.guard) : null
})

function refusalOf(g: OAuthGuardVerdict) {
  return { bindings: g.bindings, fixes: g.fixes }
}

const approveDisabled = computed(
  () => !complete.value || previewing.value || submitting.value || !!guard.value || !!previewFailed.value,
)
const previewFailed = ref(false)

const accessSentence = computed(() => {
  const a = preview.value?.access
  if (!a) return ''
  if (a.same_access_as_existing_client) return `Approving gives this app the same access as ${clientDisplayName.value}.`
  return `Approving gives this app access to ${a.profile || 'All servers'} as ${clientDisplayName.value}.`
})

const approvedOn = computed(() => {
  const at = view.value?.previous_login?.approved_at
  if (!at) return ''
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
})

function resetChoice(v: OAuthAuthorization) {
  guardOverride.value = null
  preview.value = null
  previewFailed.value = false
  profileSel.value = UNSET
  replace.value = !!v.previous_login
  if (v.address_kind === 'generic') {
    if (v.suggested_client) {
      selected.value = v.suggested_client.id
    } else {
      selected.value = NEW
      newId.value = v.suggested_new_client?.id ?? ''
      newName.value = v.suggested_new_client?.display_name ?? ''
    }
  } else {
    selected.value = ''
  }
}

async function load(keepNote = false) {
  if (!requestId.value) {
    phase.value = 'gone'
    return
  }
  phase.value = 'loading'
  errorText.value = ''
  if (!keepNote) changedNote.value = false
  try {
    const v = await api.getOAuthAuthorization(requestId.value)
    view.value = v
    resetChoice(v)
    phase.value = 'ready'
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) phase.value = 'gone'
    else {
      errorText.value = e instanceof Error ? e.message : 'Could not load the request.'
      phase.value = 'error'
    }
  }
}

async function runPreview(key: string) {
  const seq = ++previewSeq
  previewing.value = true
  errorText.value = ''
  try {
    const p = await api.previewOAuthAuthorization(requestId.value, JSON.parse(key))
    if (seq !== previewSeq) return
    preview.value = p
    previewFailed.value = false
    guardOverride.value = null
  } catch (e) {
    if (seq !== previewSeq) return
    preview.value = null
    if (e instanceof ApiError && e.status === 404 && e.code !== 'profile_not_found') {
      phase.value = 'gone'
      return
    }
    previewFailed.value = true
    errorText.value = e instanceof Error ? e.message : 'Could not check this choice.'
  } finally {
    if (seq === previewSeq) previewing.value = false
  }
}

watch(previewKey, (key) => {
  if (key === null) {
    previewSeq++
    previewing.value = false
    preview.value = null
    previewFailed.value = false
    return
  }
  void runPreview(key)
})

async function approve() {
  if (approveDisabled.value) return
  submitting.value = true
  errorText.value = ''
  try {
    const res = await api.approveOAuthAuthorization(requestId.value, buildBody())
    navigateTo(res.redirect_url)
  } catch (e) {
    await handleDecisionError(e)
  } finally {
    submitting.value = false
  }
}

async function handleDecisionError(e: unknown) {
  if (!(e instanceof ApiError)) {
    errorText.value = e instanceof Error ? e.message : 'The request failed.'
    return
  }
  if (e.code === 'already_decided') phase.value = 'decided'
  else if (e.code === 'binding_changed') {
    await load(true)
    changedNote.value = true
  } else if (e.code === 'binding_bypassable_without_auth') {
    guardOverride.value = { code: e.code, bindings: e.bindings ?? [], fixes: e.fixes ?? [] }
  } else if (e.status === 404 && e.code !== 'profile_not_found') phase.value = 'gone'
  else errorText.value = e.message
}

async function deny() {
  submitting.value = true
  errorText.value = ''
  try {
    const res = await api.denyOAuthAuthorization(requestId.value)
    navigateTo(res.redirect_url)
  } catch (e) {
    await handleDecisionError(e)
  } finally {
    submitting.value = false
  }
}

function submitKey() {
  const key = pastedKey.value.trim()
  if (!key) return
  api.setAPIKey(key)
  pastedKey.value = ''
  void load()
}

onMounted(() => {
  if (!api.hasAPIKey()) {
    phase.value = 'nokey'
    return
  }
  void load()
})
</script>
