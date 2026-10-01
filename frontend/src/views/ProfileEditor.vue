<template>
  <div class="space-y-4" data-test="profile-editor">
    <router-link class="link text-sm" :to="{ name: 'profiles' }" data-test="profile-editor-back">&larr; All profiles</router-link>

    <div v-if="loading && !saved" class="space-y-3" aria-busy="true" data-test="profile-editor-skeleton">
      <div class="skeleton h-8 w-64" /><div class="skeleton h-48 w-full" />
    </div>

    <div v-else-if="notFound" role="alert" class="alert alert-warning" data-test="profile-editor-not-found">
      <span>Profile not found</span>
      <router-link class="btn btn-sm" :to="{ name: 'profiles' }">Back to profiles</router-link>
    </div>

    <div v-else-if="loadError" role="alert" class="alert alert-error" data-test="profile-editor-error">
      <span>{{ loadError }}</span>
      <button type="button" class="btn btn-sm" @click="load">Retry</button>
    </div>

    <template v-else-if="saved">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h1 class="text-3xl font-bold truncate">{{ saved.title || saved.name }}</h1>
          <p class="text-xs font-mono opacity-70">{{ saved.name }}</p>
        </div>
        <div v-if="canEdit" class="flex gap-2">
          <button type="button" class="btn btn-sm btn-outline" data-test="profile-rename" @click="renameOpen = true">Rename&hellip;</button>
          <button type="button" class="btn btn-sm btn-outline btn-error" data-test="profile-delete" @click="deleteOpen = true">Delete&hellip;</button>
        </div>
      </div>

      <div v-if="changedElsewhere" role="status" class="alert alert-info text-sm" data-test="profile-changed-elsewhere">
        <span>This profile changed elsewhere</span>
        <button type="button" class="btn btn-sm" data-test="profile-reload" @click="reloadFromServer">Reload</button>
      </div>
      <div v-if="focusServerMissing" role="status" class="alert alert-warning text-sm" data-test="profile-focus-add-server">
        <span>{{ focusServerMissing }} is not in this profile, so {{ route.query.focus }} is hidden.</span>
        <button v-if="canEdit" type="button" class="btn btn-sm" @click="addServer(focusServerMissing)">Add {{ focusServerMissing }} to this profile</button>
      </div>
      <p class="sr-only" aria-live="polite" data-test="profile-focus-announce">{{ announcement }}</p>
      <div v-for="warning in warnings" :key="warning" role="status" class="alert alert-warning text-sm" data-test="profile-warning">{{ warning }}</div>

      <div class="grid grid-cols-1 min-[1280px]:grid-cols-2 gap-6 items-start">
        <!-- Left: the form. -->
        <form class="space-y-5" data-test="profile-form" @submit.prevent="save">
          <fieldset :disabled="!canEdit" class="space-y-5">
            <div class="form-control">
              <label class="label" for="profile-title"><span class="label-text font-medium">Title</span><span class="label-text-alt text-base-content/80">{{ draft.title.length }} / 80</span></label>
              <input id="profile-title" v-model="draft.title" maxlength="80" class="input input-bordered input-sm w-full" :class="fieldErrors.title && 'input-error'" data-test="profile-title" />
              <span v-if="fieldErrors.title" class="text-error text-xs mt-1">{{ fieldErrors.title }}</span>
            </div>
            <div class="form-control">
              <label class="label" for="profile-description"><span class="label-text font-medium">Description</span><span class="label-text-alt text-base-content/80">{{ draft.description.length }} / 500</span></label>
              <textarea id="profile-description" v-model="draft.description" maxlength="500" rows="2" class="textarea textarea-bordered w-full" :class="fieldErrors.description && 'textarea-error'" data-test="profile-description" />
              <span v-if="fieldErrors.description" class="text-error text-xs mt-1">{{ fieldErrors.description }}</span>
            </div>

            <fieldset class="form-control" :class="fieldErrors.servers && 'border border-error rounded p-2'" data-test="profile-servers">
              <legend class="label-text font-medium mb-1">Servers</legend>
              <div class="max-h-44 overflow-y-auto space-y-1 border border-base-300 rounded-lg p-2">
                <p v-if="!serverChoices.length" class="text-sm opacity-70 text-center py-1">No servers configured</p>
                <label v-for="choice in serverChoices" :key="choice.name" class="flex items-center gap-2 cursor-pointer px-1">
                  <input v-model="draft.servers" type="checkbox" class="checkbox checkbox-sm" :value="choice.name" :data-test="`profile-server-${choice.name}`" />
                  <span class="text-sm">{{ choice.name }}</span>
                  <span v-if="choice.unknown" class="badge badge-warning badge-xs" :data-test="`profile-server-unknown-${choice.name}`">not a configured server</span>
                </label>
              </div>
              <span v-if="fieldErrors.servers" class="text-error text-xs mt-1">{{ fieldErrors.servers }}</span>
            </fieldset>

            <fieldset class="form-control" data-test="profile-max-tier">
              <legend class="label-text font-medium mb-1">Max tool tier</legend>
              <div class="join flex-wrap" role="radiogroup" aria-label="Max tool tier">
                <label v-for="option in MAX_TIER_OPTIONS" :key="option.value || 'none'" class="join-item btn btn-sm" :class="draft.max_tier === option.value ? 'btn-primary' : 'btn-outline'">
                  <input v-model="draft.max_tier" type="radio" class="sr-only" :value="option.value" name="max-tier" :data-test="`profile-tier-${option.value || 'none'}`" />
                  {{ option.label }}
                </label>
              </div>
              <span v-if="fieldErrors.max_tier" class="text-error text-xs mt-1">{{ fieldErrors.max_tier }}</span>
            </fieldset>

            <fieldset class="form-control" data-test="profile-unannotated">
              <legend class="label-text font-medium mb-1">Unannotated tools</legend>
              <div class="flex flex-wrap gap-3">
                <label v-for="option in unannotatedChoices" :key="option.value || 'default'" class="label cursor-pointer gap-2 py-0">
                  <input v-model="draft.unannotated" type="radio" class="radio radio-sm" :value="option.value" name="unannotated" :data-test="`profile-unannotated-${option.value || 'default'}`" />
                  <span class="label-text">{{ option.label }}</span>
                </label>
              </div>
              <span v-if="fieldErrors.unannotated" class="text-error text-xs mt-1">{{ fieldErrors.unannotated }}</span>
            </fieldset>

            <fieldset class="form-control" data-test="profile-code-execution">
              <legend class="label-text font-medium mb-1">Code execution</legend>
              <div class="flex flex-wrap gap-3">
                <label v-for="option in tristate('code execution', saved.effective_code_execution, saved.code_execution)" :key="option.value" class="label cursor-pointer gap-2 py-0">
                  <input v-model="draft.code_execution" type="radio" class="radio radio-sm" :value="option.value" name="code-execution" :data-test="`profile-code-execution-${option.value}`" />
                  <span class="label-text">{{ option.label }}</span>
                </label>
              </div>
              <span v-if="fieldErrors.code_execution" class="text-error text-xs mt-1">{{ fieldErrors.code_execution }}</span>
            </fieldset>

            <fieldset class="form-control" data-test="profile-management-tools">
              <legend class="label-text font-medium mb-1">Management tools</legend>
              <div class="flex flex-wrap gap-3">
                <label v-for="option in tristate('management tools')" :key="option.value" class="label cursor-pointer gap-2 py-0">
                  <input v-model="draft.management_tools" type="radio" class="radio radio-sm" :value="option.value" name="management-tools" :data-test="`profile-management-tools-${option.value}`" />
                  <span class="label-text">{{ option.label }}</span>
                </label>
              </div>
              <span class="text-xs opacity-70 mt-1">Add, change and restart servers from an MCP client.</span>
              <span v-if="fieldErrors.management_tools" class="text-error text-xs mt-1">{{ fieldErrors.management_tools }}</span>
            </fieldset>

            <fieldset class="form-control" data-test="profile-switchable">
              <legend class="label-text font-medium mb-1">Agent may switch to</legend>
              <div class="flex flex-wrap gap-3 mb-1">
                <label class="label cursor-pointer gap-2 py-0"><input v-model="draft.switch_mode" type="radio" class="radio radio-sm" value="unset" name="switch-mode" data-test="profile-switch-unset" /><span class="label-text">Not set (legacy)</span></label>
                <label class="label cursor-pointer gap-2 py-0"><input v-model="draft.switch_mode" type="radio" class="radio radio-sm" value="none" name="switch-mode" data-test="profile-switch-none" /><span class="label-text">None</span></label>
                <label class="label cursor-pointer gap-2 py-0"><input v-model="draft.switch_mode" type="radio" class="radio radio-sm" value="list" name="switch-mode" data-test="profile-switch-list" /><span class="label-text">Only these</span></label>
              </div>
              <div v-if="draft.switch_mode === 'list'" class="space-y-1 border border-base-300 rounded-lg p-2">
                <p v-if="!otherProfiles.length" class="text-sm opacity-70">No other profiles</p>
                <label v-for="other in otherProfiles" :key="other.name" class="flex items-center gap-2 cursor-pointer px-1">
                  <input v-model="draft.switchable_to" type="checkbox" class="checkbox checkbox-sm" :value="other.name" :data-test="`profile-switch-${other.name}`" />
                  <span class="text-sm">{{ other.title || other.name }}</span>
                </label>
              </div>
              <span v-if="fieldErrors.switchable_to" class="text-error text-xs mt-1">{{ fieldErrors.switchable_to }}</span>
            </fieldset>
          </fieldset>

          <!-- Assigned to: who points at this profile (administrators only). -->
          <section v-if="saved.used_by" class="space-y-2" aria-labelledby="profile-assigned-heading" data-test="profile-assigned">
            <h2 id="profile-assigned-heading" class="font-semibold">Assigned to</h2>
            <p v-if="!usedByAny" class="text-sm opacity-70">Nothing yet.</p>
            <ul class="space-y-1 text-sm">
              <li v-for="client in saved.used_by.clients" :key="client.id" class="flex flex-wrap items-center gap-2" :data-test="`profile-assigned-client-${client.id}`">
                <span>Client {{ clientName(client.id) }}</span><span class="badge badge-sm">{{ client.mode === 'locked' ? 'Locked' : 'Switchable' }}</span>
                <button v-if="canEdit" type="button" class="btn btn-xs btn-ghost" :data-test="`profile-unassign-${client.id}`" @click="unassign(client.id)">Unassign</button>
              </li>
              <li v-for="token in saved.used_by.tokens" :key="token" class="flex flex-wrap items-center gap-2" :data-test="`profile-assigned-token-${token}`">
                <span>Token {{ token }}</span>
                <router-link class="link text-xs" :to="{ name: 'tokens', query: { token } }">Open</router-link>
              </li>
              <li v-if="saved.used_by.anonymous_profile" data-test="profile-assigned-anonymous">Anonymous callers</li>
            </ul>
            <div v-if="assignError" role="alert" class="text-sm text-error">{{ assignError }}</div>
            <button v-if="canEdit" type="button" class="btn btn-sm btn-outline" data-test="profile-assign" @click="assignOpen = true">Assign to client&hellip;</button>
          </section>

          <!-- scroll-mb keeps a revealed refusal clear of the sticky footer below. -->
          <div ref="refusalEl" class="scroll-mb-28">
            <GuardRefusal v-if="guard" :refusal="guard" />
            <div v-else-if="saveError" role="alert" class="alert alert-error text-sm" tabindex="-1" data-test="profile-save-error">{{ saveError }}</div>
          </div>

          <!-- The footer stays in view: Save and Discard are never below the fold. -->
          <div v-if="canEdit" class="sticky bottom-0 -mx-1 px-3 py-3 bg-base-100 border-t border-base-300 flex items-center justify-between gap-2 z-10" data-test="profile-editor-footer">
            <span class="text-sm" aria-live="polite" data-test="profile-save-status">{{ dirty ? 'Unsaved changes' : savedNote }}</span>
            <div class="flex gap-2">
              <button type="button" class="btn btn-ghost btn-sm" :disabled="!dirty || saving" data-test="profile-editor-discard" @click="discard">Discard</button>
              <button type="submit" class="btn btn-primary btn-sm" :disabled="!dirty || saving" data-test="profile-editor-save">
                <span v-if="saving" class="loading loading-spinner loading-xs" />
                Save
              </button>
            </div>
          </div>
        </form>

        <!-- Right: what the profile does to each tool, and Try it. -->
        <div class="space-y-4 min-w-0">
          <p v-if="toolsError" role="alert" class="text-sm text-error" data-test="profile-tools-error">{{ toolsError }}</p>
          <ProfileToolTable
            :rows="tools?.tools ?? []"
            :counts="tools?.counts"
            :stale="tools?.stale_classifications"
            :draft-rules="draftTools"
            :profile-label="saved.title || saved.name"
            :servers-chosen="draft.servers.length > 0"
            :loading="toolsLoading"
            :editable="canEdit"
            :focus-key="focusKey"
            @toggle="toggleRule"
            @classify="classify"
            @remove-classification="removeClassification"
          />
          <ProfileTryPanel v-if="canEdit" :draft="() => toConfig(draft)" />
        </div>
      </div>

      <ProfileRenameDialog :open="renameOpen" :profile="saved" @close="renameOpen = false" @renaming="renaming = true" @rename-failed="renaming = false" @renamed="onRenamed" />
      <ProfileDeleteDialog :open="deleteOpen" :profile="saved" @close="deleteOpen = false" @deleted="onDeleted" />
      <AssignClientDialog :open="assignOpen" :profile-name="saved.name" @close="assignOpen = false" @assigned="loadProfile" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import GuardRefusal from '@/components/GuardRefusal.vue'
import ProfileToolTable from '@/components/profiles/ProfileToolTable.vue'
import ProfileTryPanel from '@/components/profiles/ProfileTryPanel.vue'
import ProfileRenameDialog from '@/components/profiles/ProfileRenameDialog.vue'
import ProfileDeleteDialog from '@/components/profiles/ProfileDeleteDialog.vue'
import AssignClientDialog from '@/components/profiles/AssignClientDialog.vue'
import api from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore, CLIENT_BINDING_CHANGED_EVENT } from '@/stores/clients'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { useProfilesStore, PROFILES_CHANGED_EVENT } from '@/stores/profiles'
import { useServersStore } from '@/stores/servers'
import { MAX_TIER_OPTIONS, UNANNOTATED_OPTIONS, describeError, isGuardRefusal, unannotatedLabel } from '@/utils/profiles'
import type { EffectiveToolsResult, ProfileConfig, ProfileToolRules, ProfileView } from '@/types/api'
import type { ApiError } from '@/services/api'

// Spec 108-i I18 / FR-041 / FR-005: the profile policy editor. Edits change a
// local DRAFT; Save PUTs the whole ProfileConfig. The per-tool table is the
// server's evaluation of the SAVED profile; Try it evaluates the draft.
const props = defineProps<{ name: string }>()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const profiles = useProfilesStore()
const clients = useClientsStore()
const bindings = useClientBindingsStore()
const serversStore = useServersStore()

const canEdit = computed(() => auth.principalKind !== 'tenant')

type Tri = 'inherit' | 'on' | 'off'
interface Draft {
  title: string
  description: string
  servers: string[]
  max_tier: '' | 'read' | 'write' | 'destructive'
  unannotated: '' | 'deny' | 'as_write' | 'as_read'
  allow: string[]
  deny: string[]
  classify: Record<string, string>
  code_execution: Tri
  management_tools: Tri
  switch_mode: 'unset' | 'none' | 'list'
  switchable_to: string[]
}

function triFrom(value: boolean | undefined): Tri { return value === undefined ? 'inherit' : value ? 'on' : 'off' }
function triTo(value: Tri): boolean | undefined { return value === 'inherit' ? undefined : value === 'on' }

function draftFrom(view: ProfileView): Draft {
  return {
    title: view.title ?? '',
    description: view.description ?? '',
    servers: [...(view.servers ?? [])],
    max_tier: (view.max_tier ?? '') as Draft['max_tier'],
    unannotated: (view.unannotated ?? '') as Draft['unannotated'],
    allow: [...(view.tools?.allow ?? [])],
    deny: [...(view.tools?.deny ?? [])],
    classify: { ...(view.tools?.classify ?? {}) },
    code_execution: triFrom(view.code_execution),
    management_tools: triFrom(view.management_tools),
    // `switchable_to` absent = legacy; present (even empty) = an explicit list.
    switch_mode: view.switchable_to === undefined ? 'unset' : view.switchable_to.length === 0 ? 'none' : 'list',
    switchable_to: [...(view.switchable_to ?? [])],
  }
}

// The PUT body. Omission semantics are the contract: code_execution and
// management_tools "Inherit" leave the key out; switchable_to "Not set" leaves
// it out and "None" sends []; empty strings and empty rule sets are left out.
function toConfig(d: Draft): ProfileConfig {
  const cfg: ProfileConfig = { name: props.name, servers: [...d.servers] }
  if (d.title.trim()) cfg.title = d.title.trim()
  if (d.description.trim()) cfg.description = d.description.trim()
  if (d.max_tier) cfg.max_tier = d.max_tier
  if (d.unannotated) cfg.unannotated = d.unannotated
  const tools: ProfileToolRules = {}
  if (d.allow.length) tools.allow = [...d.allow]
  if (d.deny.length) tools.deny = [...d.deny]
  if (Object.keys(d.classify).length) tools.classify = { ...d.classify }
  if (Object.keys(tools).length) cfg.tools = tools
  const code = triTo(d.code_execution)
  if (code !== undefined) cfg.code_execution = code
  const mgmt = triTo(d.management_tools)
  if (mgmt !== undefined) cfg.management_tools = mgmt
  if (d.switch_mode === 'none') cfg.switchable_to = []
  else if (d.switch_mode === 'list') cfg.switchable_to = [...d.switchable_to]
  return cfg
}

const saved = ref<ProfileView | null>(null)
const draft = reactive<Draft>(draftFrom({ name: props.name, servers: [] } as unknown as ProfileView))
const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const tools = ref<EffectiveToolsResult | null>(null)
const toolsLoading = ref(false)
const toolsError = ref('')
const saving = ref(false)
const saveError = ref('')
const savedNote = ref('')
const guard = ref<ApiError | null>(null)
const fieldErrors = reactive<Record<string, string>>({})
const warnings = ref<string[]>([])
const changedElsewhere = ref(false)
const renameOpen = ref(false)
const deleteOpen = ref(false)
const assignOpen = ref(false)
const assignError = ref('')
const announcement = ref('')
const refusalEl = ref<HTMLElement | null>(null)
// True from the moment a rename is sent until the route carries the new name. The
// events the rename emits must not reload the OLD name (a 404 per event).
const renaming = ref(false)

const draftTools = computed<ProfileToolRules>(() => ({ allow: draft.allow, deny: draft.deny, classify: draft.classify }))
const dirty = computed(() => Boolean(saved.value) && JSON.stringify(toConfig(draft)) !== JSON.stringify(toConfig(draftFrom(saved.value as ProfileView))))
const usedByAny = computed(() => {
  const used = saved.value?.used_by
  return Boolean(used && (used.clients.length || used.tokens.length || used.anonymous_profile))
})
const otherProfiles = computed(() => profiles.profiles.filter(other => other.name !== props.name))
const unannotatedChoices = computed(() => [
  ...UNANNOTATED_OPTIONS,
  { value: '' as const, label: `Default (${unannotatedLabel(saved.value?.effective_unannotated)})` },
])

// On / Off / Inherit. Inherit names what it resolves to while nothing is set.
function tristate(_label: string, effective?: boolean, explicit?: boolean): Array<{ value: Tri; label: string }> {
  const inherit = explicit === undefined && effective !== undefined ? `Inherit (${effective ? 'on' : 'off'}, from the global setting)` : 'Inherit'
  return [{ value: 'on', label: 'On' }, { value: 'off', label: 'Off' }, { value: 'inherit', label: inherit }]
}

// Configured servers, plus any the profile names that are not configured (a
// typo or a removed server), flagged.
const serverChoices = computed(() => {
  const configured = new Set(serversStore.servers.map(server => server.name))
  const names = new Set([...configured, ...draft.servers])
  return [...names].sort((a, b) => a.localeCompare(b)).map(name => ({ name, unknown: configured.size > 0 && !configured.has(name) }))
})

function clientName(id: string): string { return clients.clients.find(client => client.id === id)?.display_name ?? id }

const focusKey = computed(() => (typeof route.query.focus === 'string' ? route.query.focus : ''))
const focusServerMissing = computed(() => {
  const key = focusKey.value
  if (!key.includes(':')) return ''
  const server = key.slice(0, key.indexOf(':'))
  return saved.value && !draft.servers.includes(server) ? server : ''
})
watch([focusKey, tools], () => {
  if (focusKey.value && tools.value?.tools.some(row => `${row.server}:${row.tool}` === focusKey.value)) announcement.value = `Focused ${focusKey.value}`
})

function clearErrors() {
  for (const key of Object.keys(fieldErrors)) delete fieldErrors[key]
  saveError.value = ''
  guard.value = null
}

// Every load takes a ticket; a response that is not the latest is dropped. A
// rename makes this matter: the profiles.changed event it emits can start a load
// under the OLD name while the route is already moving to the new one, and that
// 404 must never land after the new name's answer.
let loadTicket = 0

async function loadProfile(ticket: number = ++loadTicket): Promise<boolean> {
  const name = props.name
  try {
    const view = await api.getProfile(name)
    if (ticket !== loadTicket) return false
    saved.value = view
    notFound.value = false
    loadError.value = ''
  } catch (err) {
    if (ticket !== loadTicket) return false
    if ((err as ApiError).status === 404) notFound.value = true
    else loadError.value = describeError(err, 'Failed to load the profile')
    saved.value = null
  }
  return true
}

async function loadTools(ticket: number) {
  const name = props.name
  toolsLoading.value = true
  toolsError.value = ''
  try {
    const result = await api.getProfileEffectiveTools(name)
    if (ticket !== loadTicket) return
    tools.value = result
  } catch (err) {
    if (ticket !== loadTicket) return
    tools.value = null
    toolsError.value = (err as ApiError).status === 503 ? 'Profile evaluation is unavailable; retry in a moment' : describeError(err, 'Failed to load the tools')
  } finally {
    if (ticket === loadTicket) toolsLoading.value = false
  }
}

async function load() {
  const ticket = ++loadTicket
  loading.value = true
  clearErrors()
  const current = await loadProfile(ticket)
  if (!current) return
  if (saved.value) {
    Object.assign(draft, draftFrom(saved.value))
    changedElsewhere.value = false
    await loadTools(ticket)
  }
  if (ticket === loadTicket) loading.value = false
}

function reloadFromServer() { void load() }

async function save() {
  if (!dirty.value || saving.value) return
  saving.value = true
  clearErrors()
  savedNote.value = ''
  // The profile this save is for: if the operator navigates to another profile
  // while the PUT is in flight, its answer must not replace the new page's state.
  const name = props.name
  const body = toConfig(draft)
  try {
    const result = await api.updateProfile(name, body)
    if (props.name !== name) return
    saved.value = result.profile
    Object.assign(draft, draftFrom(result.profile))
    warnings.value = result.warnings ?? []
    savedNote.value = 'Saved'
    void profiles.fetchProfiles()
    await loadTools(loadTicket)
  } catch (err) {
    if (props.name !== name) return
    const refused = err as ApiError
    if (isGuardRefusal(err)) guard.value = refused
    else if (refused.field) fieldErrors[refused.field] = refused.message
    else saveError.value = describeError(err, 'Failed to save the profile')
    // The draft stays: nothing was written. Bring the reason into view: it sits
    // under the form, behind the sticky footer, and the footer alone only says
    // "Unsaved changes".
    void revealRefusal()
  } finally {
    saving.value = false
  }
}

async function revealRefusal() {
  await nextTick()
  const target = refusalEl.value?.querySelector<HTMLElement>('[data-test="guard-refusal"], [data-test="profile-save-error"]')
  if (!target) return
  target.setAttribute('tabindex', '-1')
  target.focus({ preventScroll: true })
  target.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
}

function discard() {
  if (!saved.value) return
  Object.assign(draft, draftFrom(saved.value))
  clearErrors()
  savedNote.value = ''
}

// --- draft edits from the table ------------------------------------------------
function toggleRule(payload: { list: 'allow' | 'deny'; key: string }) {
  const list = draft[payload.list]
  const index = list.indexOf(payload.key)
  if (index >= 0) list.splice(index, 1)
  else list.push(payload.key)
}
function classify(payload: { key: string; tier: string }) {
  if (payload.tier) draft.classify = { ...draft.classify, [payload.key]: payload.tier }
  else removeClassification(payload.key)
}
function removeClassification(key: string) {
  const next = { ...draft.classify }
  delete next[key]
  draft.classify = next
}
function addServer(name: string) { if (!draft.servers.includes(name)) draft.servers.push(name) }

async function unassign(clientId: string) {
  assignError.value = ''
  const moved = await bindings.setBinding(clientId, { profile: '' })
  if (moved) await loadProfile()
  else assignError.value = describeError(bindings.rowErrors[clientId])
}

function onRenamed(newName: string) {
  renameOpen.value = false
  void profiles.fetchProfiles()
  void router.replace({ name: 'profile-editor', params: { name: newName } })
}
function onDeleted() {
  deleteOpen.value = false
  void profiles.fetchProfiles()
  void router.push({ name: 'profiles' })
}

// A change made elsewhere never overwrites an unsaved draft.
function onChangedElsewhere() {
  if (!saved.value || saving.value || renaming.value) return
  if (dirty.value) changedElsewhere.value = true
  else void load()
}

onMounted(() => {
  void load()
  if (!serversStore.servers.length) void serversStore.fetchServers()
  if (!profiles.loaded) void profiles.fetchProfiles()
  if (canEdit.value && !clients.clients.length) void clients.refreshPresence()
  window.addEventListener(PROFILES_CHANGED_EVENT, onChangedElsewhere)
  window.addEventListener(CLIENT_BINDING_CHANGED_EVENT, onChangedElsewhere)
})
onBeforeUnmount(() => {
  window.removeEventListener(PROFILES_CHANGED_EVENT, onChangedElsewhere)
  window.removeEventListener(CLIENT_BINDING_CHANGED_EVENT, onChangedElsewhere)
})
watch(() => props.name, () => { renaming.value = false; void load() })
</script>
