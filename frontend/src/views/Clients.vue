<template>
  <div class="space-y-6" data-test="clients-page">
    <div class="flex items-start justify-between gap-4">
      <div><h1 class="text-3xl font-bold">Clients</h1><p class="text-base-content/70 mt-1">Connect AI clients, check presence, and manage agent tokens.</p></div>
      <div v-if="authStore.principalKind !== 'tenant'" class="flex flex-wrap justify-end gap-2" data-test="clients-toolbar">
        <button v-if="tab === 'clients' && hasAdminKeyWarning" type="button" class="btn btn-warning btn-sm" data-test="clients-upgrade-admin-key" @click="upgradeOpen = true">Upgrade admin-key clients&hellip;</button>
        <button v-if="tab === 'clients'" type="button" class="btn btn-outline btn-sm" data-test="clients-bulk-move" @click="bulkOpen = true">Move clients&hellip;</button>
        <button v-if="tab === 'clients'" type="button" class="btn btn-outline btn-sm" data-test="clients-add-other" @click="customOpen = true">Add other client&hellip;</button>
        <button class="btn btn-primary" data-test="open-client-connect" @click="connectOpen = true">Connect client</button>
      </div>
    </div>
    <div role="tablist" class="tabs tabs-boxed w-fit" data-test="clients-tabs">
      <button v-for="item in tabs" :key="item.id" class="tab" :class="tab === item.id && 'tab-active'" @click="selectTab(item.id)">{{ item.label }}</button>
    </div>
    <section v-if="tab === 'clients'" class="space-y-3">
      <ClientsWarningsBanner :warnings="store.warnings" @upgrade="upgradeOpen = true" @move="moveClient" />
      <div v-if="store.loading" class="py-12 text-center"><span class="loading loading-spinner loading-lg" /></div>
      <div v-else-if="store.error" class="alert alert-error">{{ store.error }}</div>
      <div v-else class="overflow-x-auto rounded-box border border-base-300 bg-base-100">
        <table class="table"><thead><tr><th>Client</th><th>State</th><th>Last seen</th><th>Sessions</th><th>Config path</th><th>Profile</th></tr></thead>
          <tbody><template v-for="client in store.clients" :key="client.id"><tr class="cursor-pointer hover" :class="focusedClient === client.id && 'bg-primary/10'" :data-test="focusedClient === client.id ? 'focused-client-row' : undefined" @click="toggle(client.id)"><td class="font-medium">{{ client.display_name }}</td><td><span class="badge badge-sm">{{ stateLabel(client.state) }}</span><button v-if="client.connection_unverified" type="button" class="btn btn-ghost btn-xs ml-2" data-test="check-client-connection" @click.stop="checkConnection(client.id)">Check connection</button></td><td>{{ relative(client.last_seen) }}</td><td>{{ client.active_sessions }}</td><td><code class="text-xs break-all">{{ client.display_path || '—' }}</code></td><td><ProfileChip :client="client" :auto-open="moveRequest === client.id" /></td></tr>
          <tr v-if="rowErrors[client.id]" :data-test="`client-row-error-${client.id}`"><td colspan="6" class="bg-base-200/40"><GuardRefusal v-if="isGuardRefusal(rowErrors[client.id])" :refusal="rowErrors[client.id] as any" @navigate="bindings.clearRowError(client.id)" /><div v-else role="alert" class="alert alert-error text-sm">{{ describeError(rowErrors[client.id]) }}</div></td></tr>
          <tr v-if="expanded === client.id"><td colspan="6" class="bg-base-200/40">
            <p v-if="client.reload_hint" class="text-sm mb-2">{{ client.reload_hint }}</p>
            <div v-if="clientScopeAvailable" class="flex flex-wrap gap-x-3 gap-y-1 mb-2 text-sm">
              <router-link class="link" :data-test="`clients-row-link-activity-${client.id}`" :aria-label="`Activity for ${client.display_name}`" :to="scopeQuery.linkTo('activity', { view: 'calls', client: client.id, token: '' })">Activity</router-link>
              <router-link class="link" :data-test="`clients-row-link-sessions-${client.id}`" :aria-label="`Sessions for ${client.display_name}`" :to="scopeQuery.linkTo('activity', { view: 'sessions', client: client.id, token: '' })">Sessions</router-link>
              <router-link class="link" :data-test="`clients-row-link-tools-${client.id}`" :aria-label="`Tools ${client.display_name} sees`" :to="scopeQuery.linkTo('tools', { client: client.id })">Tools it sees</router-link>
              <router-link class="link" :data-test="`clients-row-link-usage-${client.id}`" :aria-label="`Usage for ${client.display_name}`" :to="scopeQuery.linkTo('usage', { client: client.id, token: '' })">Usage</router-link>
            </div>
            <ClientBindingControls class="mb-3" :client="client" @changed="refreshSilently" @forget="forgetTarget = $event" @credential="showCredential" />
            <p v-if="!client.sessions?.length" class="text-sm opacity-60">No sessions recorded.</p>
            <router-link v-for="session in client.sessions" :key="session.id" :to="`/activity?view=sessions&session=${encodeURIComponent(session.work_session_id || session.id)}`" class="link block text-sm">Session {{ session.work_session_id || session.id }}</router-link>
          </td></tr></template></tbody>
        </table>
      </div>
      <div class="rounded-box border border-dashed border-base-300 p-4 text-sm space-y-2" data-test="other-client-snippet">
        <div><strong>Other client?</strong> Add MCPProxy to its MCP configuration with this endpoint. This example contains no admin key.</div>
        <pre v-if="otherClientSnippet" class="overflow-x-auto rounded bg-base-200 p-3 text-xs"><code>{{ otherClientSnippet }}</code></pre>
        <p v-else class="opacity-60">The endpoint is loading.</p>
        <button v-if="otherClientSnippet" type="button" class="btn btn-ghost btn-xs" data-test="copy-other-client-snippet" @click="copyOtherClientSnippet">{{ snippetCopied ? 'Copied' : 'Copy config' }}</button>
        <p class="text-xs opacity-70">Want this client to have its own credential, bound to a profile? Use <strong>Add other client&hellip;</strong> above.</p>
      </div>
    </section>
    <section v-else-if="tab === 'endpoint'" class="card bg-base-100 border border-base-300"><div class="card-body"><h2 class="card-title">Endpoint &amp; mode</h2><p class="text-sm text-base-content/70">Choose the MCP surface this instance serves. Changes are saved immediately and apply after restart.</p><ModeSwitcher /><dl v-if="store.routing" class="grid gap-1 text-sm" data-test="endpoint-list"><div v-for="(endpoint, name) in store.routing.endpoints" :key="name" class="flex items-start justify-between gap-3 rounded-lg px-2 py-1.5 hover:bg-base-200" :data-test="`endpoint-${name}`"><div class="min-w-0"><dt class="font-medium">{{ name }} <span v-if="name === 'default'" class="badge badge-xs badge-primary">default</span></dt><dd><code>{{ endpoint }}</code></dd><dd class="text-xs text-base-content/60" :data-test="`endpoint-description-${name}`">{{ endpointDescription(name) }}</dd></div><button v-if="systemStore.listenAddr" type="button" class="btn btn-ghost btn-xs shrink-0" :data-test="`copy-endpoint-${name}`" :aria-label="`Copy ${name} endpoint URL`" @click="copyEndpoint(name, endpoint)">{{ copiedEndpoint === name ? 'Copied' : 'Copy URL' }}</button></div></dl><p v-if="store.routing?.restart_required" class="alert alert-warning text-sm">Restart MCPProxy to apply {{ store.routing.pending_routing_mode }} mode.</p></div></section>
    <AgentTokens v-else />
    <ClientConnectList :show="connectOpen" @close="connectOpen = false" @updated="refreshClients" />
    <CustomClientDialog :open="customOpen" @close="customOpen = false" @created="onCustomCreated" />
    <BulkMoveDialog :open="bulkOpen" :clients="store.clients" @close="bulkOpen = false" @done="refreshSilently" />
    <UpgradeAdminKeyDialog :open="upgradeOpen" @close="upgradeOpen = false" @done="refreshSilently" />
    <ForgetClientDialog v-if="forgetTarget" :open="true" :client="forgetTarget" @close="forgetTarget = null" @done="refreshSilently" />
    <CredentialOnceDialog :open="!!secret" :credential="secret?.credential ?? ''" :snippet="secret?.snippet" :title="secret?.title" @close="secret = null" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useClientsStore } from '@/stores/clients'
import { useAuthStore } from '@/stores/auth'
import ClientConnectList from '@/components/ClientConnectList.vue'
import ProfileChip from '@/components/ProfileChip.vue'
import ClientBindingControls from '@/components/ClientBindingControls.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import ClientsWarningsBanner from '@/components/clients/ClientsWarningsBanner.vue'
import CustomClientDialog from '@/components/clients/CustomClientDialog.vue'
import BulkMoveDialog from '@/components/clients/BulkMoveDialog.vue'
import UpgradeAdminKeyDialog from '@/components/clients/UpgradeAdminKeyDialog.vue'
import CredentialOnceDialog from '@/components/clients/CredentialOnceDialog.vue'
import ForgetClientDialog from '@/components/clients/ForgetClientDialog.vue'
import { useProfilesStore } from '@/stores/profiles'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import type { ClientPresence, CustomClientResponse } from '@/types/api'
import ModeSwitcher from '@/components/ModeSwitcher.vue'
import AgentTokens from '@/views/AgentTokens.vue'
import { useSystemStore } from '@/stores/system'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'

const route = useRoute(); const router = useRouter(); const store = useClientsStore(); const profilesStore = useProfilesStore(); const bindings = useClientBindingsStore(); const rowErrors = bindings.rowErrors; const authStore = useAuthStore(); const systemStore = useSystemStore()
const tabs = [{ id: 'clients', label: 'Clients' }, { id: 'endpoint', label: 'Endpoint & mode' }, { id: 'tokens', label: 'Agent tokens' }]
const defaultTab = 'clients'
function validTab(value: unknown): value is string { return typeof value === 'string' && tabs.some(item => item.id === value) }
const tab = ref(validTab(route.query.tab) ? route.query.tab : defaultTab)
const expanded = ref('')
const focusedClient = ref('')
const connectOpen = ref(false)
const customOpen = ref(false)
const bulkOpen = ref(false)
const upgradeOpen = ref(false)
// The one-time credential of a custom client or a custom rotation. It lives only
// in this ref, for the life of the dialog: closing the dialog clears it, and it
// never reaches Pinia, localStorage or the URL (Spec 108-i I12).
const secret = ref<{ credential: string; snippet?: { generic_http: string; header_name: string } | null; title?: string } | null>(null)
const moveRequest = ref('')
const forgetTarget = ref<ClientPresence | null>(null)
const hasAdminKeyWarning = computed(() => store.warnings.some(warning => warning.code === 'client_holds_admin_key'))
const scopeQuery = useScopeQuery('clients')
const clientScopeAvailable = computed(() => isScopeParamAvailable('client'))
const snippetCopied = ref(false)
// Endpoint copy + descriptions moved here from the retired header dropdown
// (Spec 109-i). Descriptions are keyed by endpoint name from GET /routing.
const copiedEndpoint = ref('')
function endpointDescription(name: string | number): string {
  switch (name) {
    case 'default': {
      const mode = store.routing?.routing_mode
      return `Default endpoint (${mode === 'direct' ? 'direct' : mode === 'code_execution' ? 'code execution' : 'retrieve tools'} mode)`
    }
    case 'retrieve_tools': return 'Retrieve tools + call_tool_read/write/destructive'
    case 'direct': return 'Direct access to all tools (serverName__toolName)'
    case 'code_execution': return 'Code execution + retrieve_tools for discovery'
    default: return ''
  }
}
async function copyEndpoint(name: string | number, path: string) {
  try {
    await navigator.clipboard.writeText(`http://${systemStore.listenAddr}${path}`)
    copiedEndpoint.value = String(name)
    window.setTimeout(() => { if (copiedEndpoint.value === String(name)) copiedEndpoint.value = '' }, 2000)
  } catch {
    copiedEndpoint.value = ''
  }
}
const otherClientSnippet = computed(() => {
  if (!systemStore.listenAddr) return ''
  return JSON.stringify({ mcpServers: { mcpproxy: { url: `http://${systemStore.listenAddr}/mcp` } } }, null, 2)
})
async function copyOtherClientSnippet() {
  try {
    await navigator.clipboard.writeText(otherClientSnippet.value)
    snippetCopied.value = true
    window.setTimeout(() => { snippetCopied.value = false }, 2000)
  } catch {
    snippetCopied.value = false
  }
}
function selectTab(id: string) { tab.value = id }
watch(tab, value => {
  if (route.path !== '/clients') return
  const query = { ...route.query }
  if (value === defaultTab) delete query.tab
  else query.tab = value
  if (route.query.tab !== query.tab) void router.replace({ query })
})
watch(() => route.query.tab, value => {
  // A navigation away (e.g. a guard fix to /settings?tab=security) changes
  // route.query before this page unmounts; its tab is not ours to rewrite.
  if (route.path !== '/clients') return
  const next = validTab(value) ? value : defaultTab
  if (tab.value !== next) tab.value = next
  if (value !== undefined && !validTab(value)) {
    const query = { ...route.query }
    delete query.tab
    void router.replace({ query })
  }
}, { immediate: true })
async function toggle(id: string) { expanded.value = expanded.value === id ? '' : id; if (expanded.value) await store.loadDetail(id) }
async function checkConnection(id: string) { expanded.value = id; await store.loadDetail(id) }
async function focusClientFromRoute() {
  const id = typeof route.query.focus === 'string' ? route.query.focus : ''
  if (!id || !store.clients.some(client => client.id === id)) {
    focusedClient.value = ''
    return
  }
  focusedClient.value = id
  if (expanded.value !== id) {
    expanded.value = id
    await store.loadDetail(id)
  }
}
watch([() => route.query.focus, () => store.clients.map(client => client.id).join('|')], () => { void focusClientFromRoute() }, { immediate: true })
// Spec 108-f: the profile/client scope (Viewing chip or a link) filters the rows
// on the server; the warnings stay instance-level.
function restScope(): { profile?: string; client?: string } {
  const rest = scopeQuery.toRest()
  return { profile: rest?.profile, client: rest?.client }
}
function refreshClients() { void store.load(restScope()) }
// After an action: refresh in place, without the list spinner that would unmount
// the open row (and the result an action is still showing).
function refreshSilently() { void store.refreshPresence() }
function onCustomCreated(result: CustomClientResponse) {
  customOpen.value = false
  secret.value = { credential: result.credential, snippet: result.snippet, title: `Credential for ${result.client.display_name}` }
  refreshSilently()
}
function showCredential(payload: { credential: string; snippet?: { generic_http: string; header_name: string } | null; title: string }) { secret.value = payload }
async function moveClient(id: string) {
  expanded.value = id
  moveRequest.value = id
  await store.loadDetail(id)
}
function stateLabel(value: string) { return value.replaceAll('_', ' ') }
function relative(value?: string | null) { return value ? new Date(value).toLocaleString() : 'Never' }
watch(() => [scopeQuery.state.profile, scopeQuery.state.client], () => { if (authStore.principalKind !== 'tenant') refreshClients() })
watch(() => route.query.move, value => { moveRequest.value = value === '1' && typeof route.query.focus === 'string' ? route.query.focus : '' }, { immediate: true })
onMounted(() => {
  if (authStore.principalKind === 'tenant') return
  void profilesStore.fetchProfiles()
  refreshClients()
})
onBeforeUnmount(() => store.clearScope())
</script>
