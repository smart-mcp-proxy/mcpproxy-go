<template>
  <div v-if="client" class="fixed inset-0 z-50" data-test="client-manage-root">
    <div class="absolute inset-0 bg-base-content/30" data-test="client-manage-backdrop" @click="emit('close')" />
    <aside
      ref="panelEl"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      class="absolute inset-y-0 right-0 flex w-full flex-col border-l border-base-300 bg-base-100 shadow-xl sm:w-[30rem] sm:max-w-full"
      :data-test="`client-manage-panel-${client.id}`"
      @keydown="onKeydown"
    >
      <header class="flex items-start justify-between gap-3 border-b border-base-300 p-4">
        <div class="min-w-0">
          <h2 :id="titleId" ref="titleEl" tabindex="-1" class="text-xl font-semibold break-words" data-test="client-manage-title">{{ client.display_name }}</h2>
          <p class="mt-1 text-sm text-base-content/70">
            <StateTag :tone="client.state === 'connected_seen' ? 'ok' : client.state === 'connected_never_seen' ? 'review' : client.state === 'installed' ? 'busy' : 'off'">{{ clientStateLabel(client.state) }}</StateTag>
            <span v-if="client.connection_unverified" class="ml-2">unverified</span>
            <span class="ml-2" :data-test="`client-manage-last-seen-${client.id}`">Last seen {{ relative(client.last_seen) }}</span>
          </p>
        </div>
        <button type="button" class="btn btn-sm btn-ghost btn-square max-sm:h-11 max-sm:w-11 max-sm:min-h-11" data-test="client-manage-close" aria-label="Close" @click="emit('close')"><UiIcon name="x" class="h-5 w-5" /></button>
      </header>

      <div class="flex-1 space-y-6 overflow-y-auto p-4">
        <!-- Links and sessions first (FR-060). -->
        <section class="space-y-2" :data-test="`client-manage-top-${client.id}`">
          <p v-if="client.reload_hint" class="text-sm" data-test="client-reload-hint">{{ client.reload_hint }}</p>
          <div v-if="clientScopeAvailable" class="flex flex-wrap gap-x-4 gap-y-2 text-sm">
            <router-link class="link inline-flex min-h-11 items-center sm:min-h-0" :data-test="`clients-row-link-activity-${client.id}`" :aria-label="`Activity for ${client.display_name}`" :to="scopeQuery.linkTo('activity', { view: 'calls', client: client.id, token: '' })">Activity</router-link>
            <router-link class="link inline-flex min-h-11 items-center sm:min-h-0" :data-test="`clients-row-link-sessions-${client.id}`" :aria-label="`Sessions for ${client.display_name}`" :to="scopeQuery.linkTo('activity', { view: 'sessions', client: client.id, token: '' })">Sessions</router-link>
            <router-link class="link inline-flex min-h-11 items-center sm:min-h-0" :data-test="`clients-row-link-tools-${client.id}`" :aria-label="`Tools it sees for ${client.display_name}`" :to="scopeQuery.linkTo('tools', { client: client.id })">Tools it sees</router-link>
            <router-link class="link inline-flex min-h-11 items-center sm:min-h-0" :data-test="`clients-row-link-usage-${client.id}`" :aria-label="`Usage for ${client.display_name}`" :to="scopeQuery.linkTo('usage', { client: client.id, token: '' })">Usage</router-link>
          </div>
          <p class="text-sm" :data-test="`client-active-sessions-${client.id}`">{{ client.active_sessions ?? 0 }} active session{{ (client.active_sessions ?? 0) === 1 ? '' : 's' }}</p>
          <p v-if="!client.sessions?.length" class="text-sm text-base-content/70">No sessions recorded.</p>
          <router-link v-for="session in client.sessions" :key="session.id" :to="`/activity?view=sessions&session=${encodeURIComponent(session.work_session_id || session.id)}`" class="link block text-sm">Session {{ session.work_session_id || session.id }}</router-link>
        </section>

        <div v-if="rowError" :data-test="`client-row-error-${client.id}`">
          <GuardRefusal v-if="isGuardRefusal(rowError)" :refusal="rowError as any" @navigate="bindings.clearRowError(client.id)" />
          <div v-else role="alert" class="alert alert-error text-sm">{{ describeError(rowError) }}</div>
        </div>

        <!-- 1. What it can use -->
        <section class="space-y-3" :data-test="`client-manage-use-${client.id}`" :aria-labelledby="`${titleId}-use`">
          <h3 :id="`${titleId}-use`" class="text-base font-semibold">What it can use</h3>
          <ProfileChip :client="client" :auto-open="moveRequest === client.id" />
          <ClientBindingControls :client="client" section="access" @changed="emit('changed')" />
        </section>

        <!-- 2. How it connects -->
        <section class="space-y-3" :data-test="`client-manage-connect-${client.id}`" :aria-labelledby="`${titleId}-connect`">
          <h3 :id="`${titleId}-connect`" class="text-base font-semibold">How it connects</h3>
          <div v-if="configPath" class="space-y-1" data-test="client-config-path">
            <div class="text-sm text-base-content/70">Config path</div>
            <div class="flex items-start gap-2">
              <code class="min-w-0 flex-1 break-all text-sm" :title="configPath">{{ configPath }}</code>
              <button type="button" class="btn btn-sm btn-outline shrink-0 max-sm:min-h-11" data-test="client-copy-path" :aria-label="`Copy config path of ${client.display_name}`" @click="copyPath">{{ pathCopied ? 'Copied' : 'Copy' }}</button>
            </div>
          </div>
          <p v-else class="text-sm text-base-content/70">No config path: this client is not configured from a file.</p>
          <ClientCredentials :client="client" section="status" @changed="emit('changed')" />
          <div v-if="client.connection_unverified" class="space-y-1">
            <button type="button" class="btn btn-sm btn-outline max-sm:min-h-11" data-test="check-client-connection" @click="emit('check', client.id)">Check connection</button>
            <p class="text-sm text-base-content/70">Reads the client's config to confirm it points at this MCPProxy.</p>
          </div>
          <div v-if="client.kind === 'supported'" class="space-y-1">
            <button type="button" class="btn btn-sm btn-outline max-sm:min-h-11" :data-test="`client-connect-open-${client.id}`" @click="emit('connect', client.id)">Connect or check access&hellip;</button>
            <p class="text-sm text-base-content/70">Opens the connect list on this client: preview of the exact change before anything is written, check access, verify endpoint, and undo of the last write.</p>
          </div>
        </section>

        <!-- 3. Key -->
        <section class="space-y-3" :data-test="`client-manage-key-${client.id}`" :aria-labelledby="`${titleId}-key`">
          <h3 :id="`${titleId}-key`" class="text-base font-semibold">Key</h3>
          <CredentialLifecycle v-if="client.kind === 'custom' || client.issuer" :id="client.id" kind="client" :profile="client.profile" :mode="client.profile_mode" :issuer="client.issuer" :lease="client.lease" :expires-at="client.expires_at" :revoked="client.credential_state === 'revoked'" :revoked-at="client.revoked_at" :credential-state="client.credential_state" :purpose="client.purpose" :profile-state="client.profile_state" :show-state="true" />
          <ClientBindingControls :client="client" section="key" @changed="emit('changed')" @credential="emit('credential', $event)" @busy="emit('busy', $event)" />
        </section>

        <!-- 4. Disconnect or remove (closed by default) -->
        <details class="rounded-box border border-base-300" :data-test="`client-manage-remove-${client.id}`">
          <summary class="flex min-h-11 cursor-pointer items-center px-3 text-base font-semibold">Disconnect or remove</summary>
          <div class="space-y-4 border-t border-base-300 p-3">
            <div v-if="client.kind === 'supported' && client.connected" class="space-y-1">
              <button type="button" class="btn btn-sm btn-outline btn-error max-sm:min-h-11" :data-test="`client-disconnect-${client.id}`" @click="emit('connect', client.id)">Disconnect&hellip;</button>
              <p class="text-sm text-base-content/70">Opens the connect list on this client, where Disconnect asks for a confirmation that names the file; the client's credential is revoked too.</p>
            </div>
            <ClientCredentials :client="client" section="revoke" @changed="emit('changed')" />
            <ClientBindingControls :client="client" section="remove" @changed="emit('changed')" @forget="emit('forget', $event)" />
            <p v-if="!removable" class="text-sm text-base-content/70">Nothing to disconnect or revoke for this client.</p>
          </div>
        </details>
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import UiIcon from '@/components/UiIcon.vue'
import StateTag from '@/components/StateTag.vue'
import ProfileChip from '@/components/ProfileChip.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import ClientBindingControls from '@/components/ClientBindingControls.vue'
import ClientCredentials from '@/components/clients/ClientCredentials.vue'
import CredentialLifecycle from '@/components/clients/CredentialLifecycle.vue'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { useScopeQuery, isScopeParamAvailable } from '@/composables/useScopeQuery'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import { clientStateLabel } from '@/utils/clientReach'
import type { ClientPresence } from '@/types/api'

// Spec 134 FR-060: the Manage side panel of one client. Four groups in order: what
// it can use, how it connects, key, and a closed "Disconnect or remove". The page
// owns every dialog that must outlive the row (Forget result, one-time credential)
// and the shared connect list; the panel only asks for them.
const props = defineProps<{ client: ClientPresence | null; moveRequest?: string }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'changed'): void
  (e: 'forget', client: ClientPresence): void
  (e: 'credential', payload: { credential: string; snippet?: { generic_http: string; header_name: string } | null; title: string }): void
  (e: 'check', id: string): void
  (e: 'busy', busy: boolean): void
  (e: 'connect', id: string): void
}>()

const bindings = useClientBindingsStore()
const scopeQuery = useScopeQuery('clients')
const clientScopeAvailable = computed(() => isScopeParamAvailable('client'))
const panelEl = ref<HTMLElement | null>(null)
const titleEl = ref<HTMLElement | null>(null)
const titleId = `client-manage-${Math.random().toString(36).slice(2, 8)}`
const pathCopied = ref(false)

const rowError = computed(() => props.client ? bindings.rowErrors[props.client.id] : undefined)
const configPath = computed(() => props.client?.display_path || props.client?.config_path || '')
const removable = computed(() => {
  const c = props.client
  if (!c) return false
  return c.kind === 'supported' || (c.credentials ?? []).some(cred => cred.state !== 'revoked') ||
    Boolean(c.token_name) || ['client', 'revoked', 'expired'].includes(c.credential_state ?? '')
})

async function copyPath() {
  try {
    await navigator.clipboard.writeText(configPath.value)
    pathCopied.value = true
    window.setTimeout(() => { pathCopied.value = false }, 2000)
  } catch { pathCopied.value = false }
}

// Focus enters the panel on its title when it opens. Escape closes it unless the
// key came from a nested dialog (rotate, revoke, explain), which owns it.
watch(() => props.client?.id, async (id) => {
  if (!id) return
  await nextTick()
  titleEl.value?.focus()
}, { immediate: true, flush: 'post' })

function focusables(): HTMLElement[] {
  if (!panelEl.value) return []
  // A control inside the closed "Disconnect or remove" group, or inside a nested
  // dialog, is not a tab stop of the panel.
  return [...panelEl.value.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), summary')]
    .filter(el => {
      if (el.closest('dialog')) return false
      // Inside a closed <details> only its own summary is a tab stop.
      const details = el.closest('details')
      if (details && !details.open && !(el.tagName === 'SUMMARY' && el.parentElement === details)) return false
      return el.offsetParent !== null
    })
}
function onKeydown(event: KeyboardEvent) {
  if ((event.target as HTMLElement | null)?.closest('dialog')) return
  if (event.key === 'Escape') {
    if (event.defaultPrevented) return
    event.stopPropagation()
    emit('close')
    return
  }
  if (event.key !== 'Tab') return
  const items = focusables()
  if (!items.length) return
  const first = items[0]; const last = items[items.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === titleEl.value)) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && active === last) { event.preventDefault(); first.focus() }
}
function relative(value?: string | null) { return value ? new Date(value).toLocaleString() : 'Never' }
</script>
