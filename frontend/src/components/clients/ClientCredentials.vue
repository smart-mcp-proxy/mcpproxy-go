<template>
  <section v-if="visible" class="space-y-2" :data-test="`client-credentials-${client.id}`">
    <h3 class="text-sm font-semibold">Credentials</h3>
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
          <button v-if="cred.kind === 'api_key' && cred.state !== 'revoked'" type="button" class="btn btn-ghost btn-xs text-error" data-test="revoke-api-key" @click="ask({ kind: 'api_key' })">Revoke API key</button>
          <button v-if="cred.kind === 'browser_login' && cred.state !== 'revoked'" type="button" class="btn btn-ghost btn-xs text-error" :data-test="`revoke-login-${cred.grant_id}`" :aria-label="`Revoke this login of ${client.display_name}`" @click="ask({ kind: 'browser_login', cred })">Revoke login</button>
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

    <BaseDialog :open="!!pending" :title="dialogTitle" test-id="revoke-credential-dialog" @close="cancel">
      <template v-if="pending">
        <p v-if="pending.kind === 'api_key'" class="text-sm">
          This revokes the client's API key. Its browser logins and its profile binding stay. A client configured with the key can no longer call MCPProxy.
        </p>
        <p v-else class="text-sm">
          This revokes the login of <bdi class="font-mono">{{ pending.cred.reported_name }}</bdi> returning to <bdi class="font-mono">{{ pending.cred.redirect_host }}</bdi>. It stops working on its next request. The client's other credentials and its profile binding stay.
        </p>
        <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="revoke-credential-error">{{ error }}</div>
      </template>
      <template #actions>
        <button type="button" class="btn btn-ghost btn-sm" data-test="revoke-credential-cancel" @click="cancel">Cancel</button>
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

const props = defineProps<{ client: Pick<ClientPresence, 'id' | 'display_name' | 'credentials' | 'browser_login_disabled'> }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const pending = ref<Pending | null>(null)
const busy = ref(false)
const error = ref('')

const visible = computed(() => props.client.credentials !== undefined || props.client.browser_login_disabled !== undefined)
const credentials = computed(() => props.client.credentials ?? [])
const dialogTitle = computed(() => pending.value?.kind === 'api_key' ? 'Revoke API key?' : 'Revoke browser login?')

function credKey(cred: ClientCredential) { return cred.kind === 'api_key' ? 'api_key' : `login-${cred.grant_id}` }
function when(value?: string | null) { return value ? new Date(value).toLocaleString() : 'never' }
function stateClass(state: string) {
  if (state === 'active') return 'badge-success'
  if (state === 'lapsed') return 'badge-warning'
  return 'badge-ghost'
}

function ask(next: Pending) { error.value = ''; busy.value = false; pending.value = next }
function cancel() { pending.value = null; error.value = '' }
async function confirm() {
  const target = pending.value
  if (!target || busy.value) return
  busy.value = true
  error.value = ''
  try {
    if (target.kind === 'api_key') await api.revokeClientAPIKey(props.client.id)
    else await api.revokeClientLogin(props.client.id, target.cred.grant_id ?? '')
    pending.value = null
    emit('changed')
  } catch (err) {
    error.value = describeError(err)
  } finally {
    busy.value = false
  }
}
</script>
