<template>
  <div class="space-y-6" data-test="clients-page">
    <div class="flex items-start justify-between gap-4">
      <div><h1 class="text-3xl font-bold">Clients</h1><p class="text-base-content/70 mt-1">Connect AI clients, check presence, and manage agent tokens.</p></div>
      <div v-if="authStore.principalKind !== 'tenant'" class="flex flex-wrap justify-end gap-2" data-test="clients-toolbar">
        <button v-if="tab === 'clients' && hasAdminKeyWarning" type="button" class="btn btn-outline btn-warning btn-sm max-sm:min-h-11" data-test="clients-upgrade-admin-key" @click="upgradeOpen = true">Upgrade admin-key clients&hellip;</button>
        <button v-if="tab === 'clients'" type="button" class="btn btn-outline btn-sm max-sm:min-h-11" data-test="clients-bulk-move" @click="bulkOpen = true">Move clients&hellip;</button>
        <button v-if="tab === 'clients'" type="button" class="btn btn-outline btn-sm max-sm:min-h-11" data-test="clients-add-other" @click="customOpen = true">Add other client&hellip;</button>
        <button class="btn btn-primary max-sm:min-h-11" data-test="open-client-connect" @click="connectOpen = true; connectReveal = ''">Connect client</button>
      </div>
    </div>
    <div role="tablist" class="tabs tabs-boxed w-fit" data-test="clients-tabs">
      <button v-for="item in tabs" :key="item.id" class="tab max-sm:min-h-11" :class="tab === item.id && 'tab-active'" @click="selectTab(item.id)">{{ item.label }}</button>
    </div>
    <!-- Spec 134 FR-016: View as and its chips in the filter row of the Clients
         and Tokens tabs (the Endpoint tab applies no scope). -->
    <div v-if="tab !== 'endpoint'" class="flex flex-wrap items-center gap-2 empty:hidden" data-test="clients-view-as">
      <ViewAs />
      <ScopeChips v-if="tab === 'clients'" page="clients" :scope-query="scopeQuery" :disabled="scopeNotApplied" :unavailable="scopeUnavailable" />
    </div>
    <section v-if="tab === 'clients'" class="space-y-3">
      <ClientsWarningsBanner :warnings="store.warnings" @upgrade="upgradeOpen = true" @move="moveClient" />
      <div v-if="store.loading" class="py-12 text-center"><span class="loading loading-spinner loading-lg" /></div>
      <div v-else-if="store.error" class="alert alert-error">{{ store.error }}</div>
      <template v-else>
        <ClientsTable :clients="shownClients" :focused-id="focusedClient" :errors="rowErrors" :hide-error-for="panelId" test-id="clients-table" @manage="openPanel" @clear-error="bindings.clearRowError" />
        <!-- Spec 134 FR-060: only supported apps that are positively not detected fold. -->
        <details v-if="undetectedClients.length" :open="undetectedOpen" @toggle="undetectedOpen = ($event.target as HTMLDetailsElement).open" class="rounded-box border border-base-300 bg-base-100" data-test="clients-undetected">
          <summary class="flex min-h-11 cursor-pointer items-center gap-2 px-3 text-sm font-medium" data-test="clients-undetected-summary"><UiIcon name="chevronDown" class="h-4 w-4 transition-transform" :class="!undetectedOpen && '-rotate-90'" />{{ undetectedClients.length }} supported app{{ undetectedClients.length === 1 ? '' : 's' }} not detected</summary>
          <ClientsTable :clients="undetectedClients" :focused-id="focusedClient" :errors="rowErrors" :hide-error-for="panelId" test-id="clients-undetected-table" @manage="openPanel" @clear-error="bindings.clearRowError" />
        </details>
      </template>
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
    <ClientManagePanel
      :client="panelClient"
      :move-request="moveRequest"
      @close="closePanel"
      @changed="refreshSilently"
      @forget="forgetTarget = $event"
      @credential="showCredential"
      @check="checkConnection"
      @connect="openConnectFor"
    />
    <ClientConnectList :show="connectOpen" :reveal-client="connectReveal" @close="connectOpen = false; connectReveal = ''" @updated="refreshClients" />
    <CustomClientDialog :open="customOpen" @close="customOpen = false" @created="onCustomCreated" />
    <BulkMoveDialog :open="bulkOpen" :clients="store.clients" @close="bulkOpen = false" @done="refreshSilently" />
    <UpgradeAdminKeyDialog :open="upgradeOpen" @close="upgradeOpen = false" @done="refreshSilently" />
    <ForgetClientDialog v-if="forgetTarget" :open="true" :client="forgetTarget" @close="onForgetClose" @done="refreshSilently" />
    <CredentialOnceDialog :open="!!secret" :credential="secret?.credential ?? ''" :snippet="secret?.snippet" :title="secret?.title" @close="secret = null" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ViewAs from '@/components/ViewAs.vue'
import ScopeChips from '@/components/scope/ScopeChips.vue'
import { useRoute, useRouter } from 'vue-router'
import { useClientsStore } from '@/stores/clients'
import { useAuthStore } from '@/stores/auth'
import ClientConnectList from '@/components/ClientConnectList.vue'
import UiIcon from '@/components/UiIcon.vue'
import ClientsTable from '@/components/clients/ClientsTable.vue'
import ClientManagePanel from '@/components/clients/ClientManagePanel.vue'
import ClientsWarningsBanner from '@/components/clients/ClientsWarningsBanner.vue'
import CustomClientDialog from '@/components/clients/CustomClientDialog.vue'
import BulkMoveDialog from '@/components/clients/BulkMoveDialog.vue'
import UpgradeAdminKeyDialog from '@/components/clients/UpgradeAdminKeyDialog.vue'
import CredentialOnceDialog from '@/components/clients/CredentialOnceDialog.vue'
import ForgetClientDialog from '@/components/clients/ForgetClientDialog.vue'
import { useProfilesStore } from '@/stores/profiles'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { isUndetectedSupported } from '@/utils/clientReach'
import type { ClientPresence, CustomClientResponse } from '@/types/api'
import ModeSwitcher from '@/components/ModeSwitcher.vue'
import AgentTokens from '@/views/AgentTokens.vue'
import { useSystemStore } from '@/stores/system'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import { browserMcpEndpoint } from '@/utils/mcpEndpoint'

const route = useRoute(); const router = useRouter(); const store = useClientsStore(); const profilesStore = useProfilesStore(); const bindings = useClientBindingsStore(); const rowErrors = bindings.rowErrors; const authStore = useAuthStore(); const systemStore = useSystemStore()
const tabs = [{ id: 'clients', label: 'Clients' }, { id: 'endpoint', label: 'Endpoint & mode' }, { id: 'tokens', label: 'Agent tokens' }]
const defaultTab = 'clients'
function validTab(value: unknown): value is string { return typeof value === 'string' && tabs.some(item => item.id === value) }
const tab = ref(validTab(route.query.tab) ? route.query.tab : defaultTab)
// Spec 134 FR-060: the Manage side panel replaces the inline expansion.
const panelId = ref('')
const focusedClient = ref('')
const connectReveal = ref('')
const undetectedOpen = ref(false)
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
// Spec 134 FR-016: every carried view-as value stays visible and removable here.
// A tenant's list is never narrowed by them; a value the build does not list is
// marked "Filter unavailable on this server".
const TENANT_SCOPE_NOTE = 'your account sees its own clients'
const scopeNotApplied = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  if (authStore.principalKind === 'tenant') { out.profile = TENANT_SCOPE_NOTE; out.client = TENANT_SCOPE_NOTE }
  return out
})
const scopeUnavailable = computed(() =>
  (['profile', 'client'] as const).filter((name) => typeof route.query[name] === 'string' && route.query[name] !== '' && !isScopeParamAvailable(name)))
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
    await navigator.clipboard.writeText(browserMcpEndpoint(path))
    copiedEndpoint.value = String(name)
    window.setTimeout(() => { if (copiedEndpoint.value === String(name)) copiedEndpoint.value = '' }, 2000)
  } catch {
    copiedEndpoint.value = ''
  }
}
const otherClientSnippet = computed(() => {
  return JSON.stringify({ mcpServers: { mcpproxy: { url: browserMcpEndpoint() } } }, null, 2)
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
const panelClient = computed(() => store.clients.find(client => client.id === panelId.value) ?? null)
// A focused client or the one in the panel is always revealed, even when undetected.
function isFolded(client: ClientPresence) { return isUndetectedSupported(client) && client.id !== focusedClient.value && client.id !== panelId.value }
const shownClients = computed(() => store.clients.filter(client => !isFolded(client)))
const undetectedClients = computed(() => store.clients.filter(isFolded))
async function openPanel(id: string) {
  panelId.value = id
  await store.loadDetail(id)
}
async function closePanel() {
  const id = panelId.value
  // A panel opened on an undetected app revealed its row; once closed the row folds again,
  // so the fold opens to keep the Manage button where focus returns.
  const closing = store.clients.find(client => client.id === id)
  if (closing && isUndetectedSupported(closing)) undetectedOpen.value = true
  panelId.value = ''
  await nextTick()
  await nextTick()
  // Back to the row's Manage button, or the toolbar when the row no longer exists.
  const trigger = id ? document.querySelector<HTMLElement>(`[data-test="client-manage-${id}"]`) : null
  const fallback = document.querySelector<HTMLElement>('[data-test="open-client-connect"]') ?? document.querySelector<HTMLElement>('[data-test="clients-tabs"] button')
  ;(trigger ?? fallback)?.focus()
}
async function checkConnection(id: string) { await store.loadDetail(id) }
// Dismissing the Forget dialog with no panel left puts focus back on the toolbar.
async function onForgetClose() {
  forgetTarget.value = null
  if (panelId.value) return
  await nextTick()
  document.querySelector<HTMLElement>('[data-test="open-client-connect"]')?.focus()
}
function openConnectFor(id: string) { connectReveal.value = id; connectOpen.value = true }
// The panel's client left the roster (Forget, a refresh): close it and keep focus on the page.
watch(() => store.clients.some(client => client.id === panelId.value), (present) => { if (panelId.value && !present && !store.loading) void closePanel() })
// `?focus=<id>` opens the panel once per value, on the Clients tab, for a client in the roster this caller received.
let handledFocus = ''
async function focusClientFromRoute() {
  const id = typeof route.query.focus === 'string' ? route.query.focus : ''
  if (!id || tab.value !== 'clients' || !store.clients.some(client => client.id === id)) {
    focusedClient.value = ''
    if (!id) handledFocus = ''
    return
  }
  focusedClient.value = id
  if (handledFocus === id) return
  handledFocus = id
  await openPanel(id)
}
watch([() => route.query.focus, () => tab.value, () => store.clients.map(client => client.id).join('|')], () => { void focusClientFromRoute() }, { immediate: true })
watch(undetectedClients, (list) => { if (list.length === 0) undetectedOpen.value = false })
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
  moveRequest.value = id
  await openPanel(id)
}
watch(() => [scopeQuery.state.profile, scopeQuery.state.client], () => { if (authStore.principalKind !== 'tenant') refreshClients() })
watch(() => route.query.move, value => { moveRequest.value = value === '1' && typeof route.query.focus === 'string' ? route.query.focus : '' }, { immediate: true })
onMounted(() => {
  if (authStore.principalKind === 'tenant') return
  void profilesStore.fetchProfiles()
  refreshClients()
})
onBeforeUnmount(() => store.clearScope())
</script>
