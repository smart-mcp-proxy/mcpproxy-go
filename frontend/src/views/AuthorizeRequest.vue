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
      <span>{{ errorText }}</span>
      <button type="button" class="btn btn-sm" data-test="authorize-load-retry" @click="load()">Try again</button>
    </div>

    <div v-else-if="view" class="card bg-base-100 shadow">
      <div class="card-body space-y-4">
        <div class="space-y-1">
          <p data-test="authorize-reported">The app calls itself "<bdi class="font-mono bg-base-200 px-1 rounded">{{ view.reported_name }}</bdi>". MCPProxy cannot verify that.</p>
          <p class="text-sm opacity-70" data-test="authorize-returns-to">Returns to: <bdi class="font-mono bg-base-200 px-1 rounded">{{ view.redirect_uri }}</bdi></p>
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
            :disabled="busy"
            class="select select-bordered w-full"
            data-test="authorize-client-select"
          >
            <option v-for="c in existingClients" :key="c.id" :value="c.id">{{ c.display_name }} ({{ c.id }})</option>
            <option value="__new__">New client</option>
          </select>
          <p v-if="showSuggestion" class="text-sm" data-test="authorize-suggestion-note">Suggested because the app calls itself "<bdi class="font-mono bg-base-200 px-1 rounded">{{ view.reported_name }}</bdi>". MCPProxy cannot verify that.</p>
          <div v-if="selected === '__new__'" class="grid gap-2 sm:grid-cols-2">
            <label class="form-control">
              <span class="label-text text-sm">Client id</span>
              <input v-model.lazy="newId" :disabled="busy" class="input input-bordered" maxlength="56" data-test="authorize-new-id" />
            </label>
            <label class="form-control">
              <span class="label-text text-sm">Display name</span>
              <input v-model.lazy="newName" :disabled="busy" class="input input-bordered" maxlength="64" data-test="authorize-new-name" />
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
            :disabled="busy"
            class="select select-bordered w-full"
            data-test="authorize-profile-select"
          >
            <option v-if="view.profiles.length > 0" :value="UNSET" disabled>Choose a profile…</option>
            <option v-for="p in view.profiles" :key="p.name" :value="p.name">{{ p.name }}</option>
            <option :value="ALL">All servers</option>
          </select>
          <label v-if="profileSel !== UNSET && profileSel !== ALL" class="flex items-center gap-2 text-sm">
            <input v-model="lock" :disabled="busy" type="checkbox" class="checkbox checkbox-sm" data-test="authorize-lock" />
            Lock this client to the profile
          </label>
        </div>

        <p v-if="current?.access" class="text-sm" data-test="authorize-access"><template v-if="current.access.same_access_as_existing_client">Approving gives this app the same access as <bdi class="font-mono bg-base-200 px-1 rounded">{{ clientDisplayName }}</bdi>.</template><template v-else>Approving gives this app access to <bdi class="font-mono bg-base-200 px-1 rounded">{{ current.access.profile || 'All servers' }}</bdi> as <bdi class="font-mono bg-base-200 px-1 rounded">{{ clientDisplayName }}</bdi>.</template></p>

        <label v-if="showReplace" class="flex items-center gap-2 text-sm">
          <input v-model="replace" :disabled="busy" type="checkbox" class="checkbox checkbox-sm" data-test="authorize-replace" />
          Replace the previous browser login (approved {{ approvedOn }})
        </label>

        <GuardRefusal v-if="guard" :refusal="guard" />

        <div v-if="changedNote" class="alert alert-info text-sm" role="status" data-test="authorize-changed">
          The client's access changed while this page was open. It was reloaded: review it and approve again.
        </div>
        <div v-if="errorText" class="alert alert-error text-sm" role="alert" data-test="authorize-error">{{ errorText }}</div>
        <button v-if="previewFailed && !busy" type="button" class="btn btn-sm btn-outline self-start" data-test="authorize-retry" @click="checkAgain">Check again</button>

        <div class="card-actions justify-end">
          <button type="button" class="btn" :disabled="busy" data-test="authorize-deny" @click="deny">Deny</button>
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
//
// Three rules keep a decision tied to what is on screen:
//  - the decision goes to the request that was LOADED and is displayed; a new
//    `?request=` id clears the page at once and loads again;
//  - Approve needs a complete preview, with access, for the CURRENT choice, and
//    sends exactly the body that preview answered;
//  - one decision per page: after success the buttons stay disabled.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import api, { ApiError } from '@/services/api'
import { navigateTo } from '@/utils/navigate'
import GuardRefusal from '@/components/GuardRefusal.vue'
import type { OAuthApprovalBody, OAuthAuthorization, OAuthDecision, OAuthGuardVerdict, OAuthPreview } from '@/types/api'

const UNSET = '__unset__'
const ALL = '__all__'
const NEW = '__new__'

type Phase = 'nokey' | 'loading' | 'ready' | 'gone' | 'decided' | 'error'

const route = useRoute()
const requestId = computed(() => (typeof route.query.request === 'string' ? route.query.request : ''))

const phase = ref<Phase>('loading')
const view = ref<OAuthAuthorization | null>(null)
const loadedId = ref('')
const pastedKey = ref('')
const selected = ref('')
const newId = ref('')
const newName = ref('')
const profileSel = ref<string>(UNSET)
const lock = ref(true)
const replace = ref(false)
const accepted = ref<{ key: string; preview: OAuthPreview } | null>(null)
const previewFailed = ref(false)
const guardOverride = ref<OAuthGuardVerdict | null>(null)
const errorText = ref('')
const changedNote = ref(false)
const submitting = ref(false)
const decided = ref(false)
let previewSeq = 0
let loadGen = 0
// A preview answering binding_changed reloads the request once on its own; a
// user-initiated change (or a new request) earns one more.
let reloadsLeft = 1
let applyingReload = false

const busy = computed(() => submitting.value || decided.value)
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

// A previous login is replaced only by signing in the client that owns it.
const previousForSelection = computed(() => {
  const owner = fixed.value?.id ?? selectedExisting.value?.id
  if (!owner || !view.value) return null
  const list = view.value.previous_logins ?? []
  return list.find((p) => p.client_id === owner) ?? (view.value.previous_login?.client_id === owner ? view.value.previous_login : null)
})
const showReplace = computed(() => !!previousForSelection.value)

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
  if (previousForSelection.value && replace.value) body.replace_grant = previousForSelection.value.grant_id
  return body
}

const previewKey = computed(() => (phase.value === 'ready' && complete.value ? JSON.stringify(buildBody()) : null))

// The accepted preview is current only for the exact body it answered.
const current = computed(() => (accepted.value && accepted.value.key === previewKey.value ? accepted.value.preview : null))

const guard = computed(() => {
  if (guardOverride.value) return refusalOf(guardOverride.value)
  if (current.value) return current.value.guard ? refusalOf(current.value.guard) : null
  return view.value?.guard ? refusalOf(view.value.guard) : null
})

function refusalOf(g: OAuthGuardVerdict) {
  return { bindings: g.bindings, fixes: g.fixes }
}

const approveReady = computed(
  () => !!current.value && current.value.complete === true && !!current.value.access && !guard.value && !previewFailed.value,
)
const approveDisabled = computed(() => !approveReady.value || busy.value)

const approvedOn = computed(() => {
  const at = previousForSelection.value?.approved_at
  if (!at) return ''
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
})

function resetPage(keepNote = false) {
  loadGen++
  previewSeq++
  view.value = null
  loadedId.value = ''
  accepted.value = null
  previewFailed.value = false
  guardOverride.value = null
  errorText.value = ''
  submitting.value = false
  decided.value = false
  if (!keepNote) changedNote.value = false
}

function resetChoice(v: OAuthAuthorization) {
  // With no profiles configured the select has no placeholder: All servers.
  profileSel.value = v.profiles.length === 0 ? ALL : UNSET
  lock.value = true
  replace.value = !!(v.previous_login || v.previous_logins?.length)
  // The new-client draft is always ready, whatever is selected first.
  newId.value = v.suggested_new_client?.id ?? ''
  newName.value = v.suggested_new_client?.display_name ?? ''
  if (v.address_kind === 'generic') selected.value = v.suggested_client ? v.suggested_client.id : NEW
  else selected.value = ''
}

async function load(keepNote = false): Promise<boolean> {
  // A review-again reload keeps what the user chose, when it still exists.
  const kept = keepNote
    ? {
        selected: selected.value, newId: newId.value, newName: newName.value, profileSel: profileSel.value,
        lock: lock.value, replace: replace.value, grant: previousForSelection.value?.grant_id ?? '',
      }
    : null
  resetPage(keepNote)
  if (!keepNote) reloadsLeft = 1
  const id = requestId.value
  if (!id) {
    phase.value = 'gone'
    return false
  }
  phase.value = 'loading'
  const gen = loadGen
  try {
    const v = await api.getOAuthAuthorization(id)
    if (gen !== loadGen) return false
    applyingReload = keepNote // the watcher must not treat the reload's own changes as user edits
    view.value = v
    loadedId.value = id
    resetChoice(v)
    if (kept && v.address_kind === 'generic' && (kept.selected === NEW || v.existing_clients?.some((c) => c.id === kept.selected))) {
      // Every kept value is checked against the reloaded data.
      selected.value = kept.selected
      newId.value = kept.newId
      newName.value = kept.newName
      const names = v.profiles.map((p) => p.name)
      if (names.length === 0) profileSel.value = ALL
      else if (kept.profileSel === ALL || names.includes(kept.profileSel)) profileSel.value = kept.profileSel
      else profileSel.value = UNSET
      lock.value = kept.lock
      const prev = (v.previous_logins ?? []).find((p) => p.client_id === kept.selected)
        ?? (v.previous_login?.client_id === kept.selected ? v.previous_login : undefined)
      replace.value = prev ? (prev.grant_id === kept.grant ? kept.replace : true) : false
    }
    phase.value = 'ready'
    applyingReload = false
    return true
  } catch (e) {
    applyingReload = false
    if (gen !== loadGen) return false
    if (e instanceof ApiError && e.status === 404) phase.value = 'gone'
    else {
      errorText.value = e instanceof Error ? e.message : 'Could not load the request.'
      phase.value = 'error'
    }
    return false
  }
}

// A 404 that names the choice (not the request) is shown on the choice.
function isChoiceError(e: ApiError): boolean {
  return e.code === 'profile_not_found' || e.code === 'client_not_found'
}

async function runPreview(id: string, key: string) {
  const seq = ++previewSeq
  errorText.value = ''
  try {
    const p = await api.previewOAuthAuthorization(id, JSON.parse(key))
    if (seq !== previewSeq || busy.value) return
    accepted.value = { key, preview: p }
    previewFailed.value = false
    guardOverride.value = null
  } catch (e) {
    if (seq !== previewSeq || busy.value) return
    accepted.value = null
    if (e instanceof ApiError && e.status === 404 && !isChoiceError(e)) {
      phase.value = 'gone'
      return
    }
    if (e instanceof ApiError && e.code === 'binding_changed') {
      if (reloadsLeft > 0) {
        reloadsLeft--
        if (await load(true)) changedNote.value = true
        return
      }
      // Reloaded once already: stop here. Deny stays available; Check again is the user's call.
      previewFailed.value = true
      changedNote.value = true
      errorText.value = "The client's access keeps changing. Check again when it is stable."
      return
    }
    previewFailed.value = true
    errorText.value = e instanceof Error ? e.message : 'Could not check this choice.'
  }
}

// Sync: the accepted preview is dropped in the same tick the choice changes.
watch(
  previewKey,
  (key) => {
    if (key !== null && !applyingReload) reloadsLeft = 1
    previewSeq++
    accepted.value = null
    guardOverride.value = null
    previewFailed.value = false
    if (key !== null && loadedId.value) void runPreview(loadedId.value, key)
  },
  { flush: 'sync' },
)

watch(requestId, () => {
  if (phase.value === 'nokey') return
  void load()
})

async function decide(call: (id: string) => Promise<OAuthDecision>) {
  if (busy.value || !loadedId.value) return
  submitting.value = true
  errorText.value = ''
  // An answer for a request that was left (new id, unmount) is obsolete: it
  // must not navigate, hide, disable or release anything on the page now shown.
  const gen = loadGen
  previewSeq++ // a preview still in flight is obsolete once a decision starts
  try {
    const res = await call(loadedId.value)
    if (gen !== loadGen) return
    decided.value = true // terminal: stays disabled while the browser leaves
    navigateTo(res.redirect_url)
  } catch (e) {
    if (gen !== loadGen) return
    submitting.value = false
    await handleDecisionError(e)
    // The preview that was cut off when the decision started is run again.
    if (gen === loadGen && phase.value === 'ready' && !current.value) retryPreview()
  }
}

function checkAgain() {
  reloadsLeft = 1
  retryPreview()
}

// Runs the preview again for the current request and choice (also the
// "Check again" button after a failed preview).
function retryPreview() {
  const key = previewKey.value
  if (key !== null && loadedId.value && !busy.value) void runPreview(loadedId.value, key)
}

function approve() {
  const a = accepted.value
  if (!approveReady.value || !a) return
  void decide((id) => api.approveOAuthAuthorization(id, JSON.parse(a.key)))
}

function deny() {
  void decide((id) => api.denyOAuthAuthorization(id))
}

async function handleDecisionError(e: unknown) {
  if (!(e instanceof ApiError)) {
    errorText.value = e instanceof Error ? e.message : 'The request failed.'
    return
  }
  if (e.code === 'already_decided') phase.value = 'decided'
  else if (e.code === 'binding_changed') {
    if (await load(true)) changedNote.value = true
  } else if (e.code === 'binding_bypassable_without_auth') {
    guardOverride.value = { code: e.code, bindings: e.bindings ?? [], fixes: e.fixes ?? [] }
  } else if (e.status === 404 && !isChoiceError(e)) phase.value = 'gone'
  else errorText.value = e.message
}

function submitKey() {
  const key = pastedKey.value.trim()
  if (!key) return
  api.setAPIKey(key)
  pastedKey.value = ''
  void load()
}

onBeforeUnmount(() => {
  loadGen++
  previewSeq++
})

onMounted(() => {
  if (!api.hasAPIKey()) {
    phase.value = 'nokey'
    return
  }
  void load()
})
</script>
