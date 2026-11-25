<template>
  <section v-if="visible && (section !== 'revoke' || revocable.length)" class="space-y-2" :data-test="`client-credentials-${client.id}${section === 'revoke' ? '-revoke' : ''}`">
    <h3 v-if="section === 'all'" class="text-sm font-semibold">Credentials</h3>
    <!-- Spec 134 FR-060: in the Manage panel's closed "Disconnect or remove" group each
         revocation is a button with one line saying what it does. -->
    <ul v-if="section === 'revoke'" class="space-y-3">
      <li v-for="cred in revocable" :key="credKey(cred)" class="space-y-1">
        <button v-if="cred.kind === 'api_key'" type="button" class="btn btn-sm btn-outline btn-error max-sm:min-h-11" data-test="revoke-api-key" @click="ask({ kind: 'api_key' })">Revoke API key</button>
        <button v-else type="button" class="btn btn-sm btn-outline btn-error max-sm:min-h-11" :data-test="`revoke-login-${cred.grant_id}`" :aria-label="`Revoke this login of ${client.display_name}`" @click="ask({ kind: 'browser_login', cred })">Revoke login of <bdi class="font-mono">{{ cred.reported_name }}</bdi></button>
        <p v-if="cred.kind === 'browser_login'" class="text-sm" :data-test="`revoke-login-facts-${cred.grant_id}`">Approved {{ when(cred.approved_at) }} · last used {{ when(cred.last_used_at) }} · returns to <bdi class="font-mono">{{ cred.redirect_host }}</bdi></p>
        <p class="text-sm text-base-content/70">{{ cred.kind === 'api_key' ? 'Stops the key working; browser logins and the profile binding stay.' : `Stops the login returning to ${cred.redirect_host} on its next request; other credentials and the profile binding stay.` }}</p>
      </li>
    </ul>
    <template v-else>
    <p v-if="client.browser_login_disabled" class="text-xs opacity-70" data-test="browser-login-disabled">
      Browser login is disabled. Existing logins are still listed and can be revoked; none can sign in until it is turned on again.
    </p>
    <p v-if="!credentials.length" class="text-sm opacity-60" data-test="credentials-empty">No credentials.</p>
    <ul v-else class="space-y-2">
      <li v-for="cred in credentials" :key="credKey(cred)" class="rounded-box border border-base-300 bg-base-100 p-2 text-sm" :data-test="cred.kind === 'api_key' ? 'credential-api_key' : `credential-login-${cred.grant_id}`">
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span class="font-medium">{{ cred.kind === 'api_key' ? 'API key' : 'Browser login' }}</span>
          <code v-if="cred.kind === 'api_key' && cred.prefix" class="text-xs">{{ cred.prefix }}…</code>
          <span class="badge badge-sm" :class="stateClass(cred.state)" :data-test="`credential-state-${cred.kind === 'api_key' ? 'api_key' : cred.grant_id}`">{{ cred.state }}</span>
          <span class="grow" />
          <template v-if="section === 'all'">
            <button v-if="cred.kind === 'api_key' && cred.state !== 'revoked'" type="button" class="btn btn-ghost btn-xs text-error" data-test="revoke-api-key" @click="ask({ kind: 'api_key' })">Revoke API key</button>
            <button v-if="cred.kind === 'browser_login' && cred.state !== 'revoked'" type="button" class="btn btn-ghost btn-xs text-error" :data-test="`revoke-login-${cred.grant_id}`" :aria-label="`Revoke this login of ${client.display_name}`" @click="ask({ kind: 'browser_login', cred })">Revoke login</button>
          </template>
        </div>
        <div class="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-xs opacity-80">
          <span v-if="cred.kind === 'browser_login'">Approved {{ when(cred.approved_at) }}</span>
          <span v-if="cred.kind === 'api_key' && cred.expires_at">Expires {{ when(cred.expires_at) }}</span>
          <span>Last used {{ when(cred.last_used_at) }}</span>
          <span v-if="cred.kind === 'browser_login'">Calls itself <bdi class="font-mono" data-test="credential-reported-name">{{ cred.reported_name }}</bdi></span>
          <span v-if="cred.kind === 'browser_login'">Returns to <bdi class="font-mono" data-test="credential-redirect-host">{{ cred.redirect_host }}</bdi></span>
        </div>
        <p v-if="cred.kind === 'browser_login' && cred.name_mismatch" class="mt-1 text-xs text-warning" :data-test="`credential-mismatch-${cred.grant_id}`" role="note">
          Name mismatch: this login was approved for <bdi class="font-mono">{{ cred.reported_name }}</bdi>, but its client introduced itself as <bdi class="font-mono" data-test="credential-observed-name">{{ cred.observed_name }}</bdi>.
        </p>
      </li>
    </ul>

    </template>

    <BaseDialog v-if="section !== 'status'" :open="!!pending" :title="dialogTitle" test-id="revoke-credential-dialog" @close="cancel">
      <template v-if="pending">
        <p v-if="pending.kind === 'api_key'" class="text-sm">
          This revokes the client's API key. Its browser logins and its profile binding stay. A client configured with the key can no longer call MCPProxy.
        </p>
        <p v-else class="text-sm">
          This revokes the login of <bdi class="font-mono">{{ pending.cred.reported_name }}</bdi> returning to <bdi class="font-mono">{{ pending.cred.redirect_host }}</bdi> (approved {{ when(pending.cred.approved_at) }}, last used {{ when(pending.cred.last_used_at) }}). It stops working on its next request. The client's other credentials and its profile binding stay.
        </p>
        <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="revoke-credential-error">{{ error }}</div>
      </template>
      <template #actions>
        <button type="button" class="btn btn-ghost btn-sm" data-test="revoke-credential-cancel" :disabled="busy" @click="cancel">Cancel</button>
        <button type="button" class="btn btn-error btn-sm" :disabled="busy" data-test="revoke-credential-confirm" @click="confirm">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Revoke
        </button>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import api from '@/services/api'
import { describeError } from '@/utils/profiles'
import type { ClientCredential, ClientPresence } from '@/types/api'

// Spec 132 US3: the credentials of one client in the Clients hub. It renders
// nothing until the payload carries `credentials` or `browser_login_disabled`,
// which the core adds only once browser login was used — so while the feature is
// off and was never used the page reads exactly as it did before.
//
// Reported names, observed names and redirect hosts come from the client
// itself: they are rendered as text inside <bdi>, never as HTML.
type Pending = { kind: 'api_key' } | { kind: 'browser_login'; cred: ClientCredential }

// `section` (Spec 134 FR-060): `status` lists the credentials without revoke buttons
// (group "How it connects"), `revoke` shows only the revoke actions (group
// "Disconnect or remove"); `all` is the whole section as before.
const props = withDefaults(defineProps<{ client: Pick<ClientPresence, 'id' | 'display_name' | 'credentials' | 'browser_login_disabled'>; section?: 'all' | 'status' | 'revoke' }>(), { section: 'all' })
const emit = defineEmits<{ (e: 'changed'): void }>()

const pending = ref<Pending | null>(null)
const busy = ref(false)
const error = ref('')

const visible = computed(() => props.client.credentials !== undefined || props.client.browser_login_disabled !== undefined)
const credentials = computed(() => props.client.credentials ?? [])
const revocable = computed(() => credentials.value.filter(cred => cred.state !== 'revoked'))
const dialogTitle = computed(() => pending.value?.kind === 'api_key' ? 'Revoke API key?' : 'Revoke browser login?')

function credKey(cred: ClientCredential) { return cred.kind === 'api_key' ? 'api_key' : `login-${cred.grant_id}` }
function when(value?: string | null) { return value ? new Date(value).toLocaleString() : 'never' }
function stateClass(state: string) {
  if (state === 'active') return 'badge-success'
  if (state === 'lapsed') return 'badge-warning'
  return 'badge-ghost'
}

// Every dialog opening and every revoke gets an identity. A completion that is
// no longer the current one changes nothing in the UI.
let opSeq = 0
let current = 0

function ask(next: Pending) {
  if (busy.value) return // a revoke is in flight: the target cannot change under it
  current = ++opSeq
  error.value = ''
  pending.value = next
}
function cancel() {
  if (busy.value) return // Cancel, Escape and the backdrop wait for the revoke to settle
  current = ++opSeq
  pending.value = null
  error.value = ''
}
async function confirm() {
  const target = pending.value
  if (!target || busy.value) return
  const op = current
  busy.value = true
  error.value = ''
  try {
    if (target.kind === 'api_key') await api.revokeClientAPIKey(props.client.id)
    else await api.revokeClientLogin(props.client.id, target.cred.grant_id ?? '')
    // The revoke happened whatever the dialog shows now: tell the page.
    emit('changed')
    if (op === current) pending.value = null
  } catch (err) {
    if (op === current) error.value = describeError(err)
  } finally {
    if (op === current) busy.value = false
  }
}
</script>
