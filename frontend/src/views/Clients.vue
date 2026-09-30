<template>
  <div class="space-y-6" data-test="clients-page">
    <div class="flex items-start justify-between gap-4">
      <div><h1 class="text-3xl font-bold">Clients</h1><p class="text-base-content/70 mt-1">Connect AI clients, check presence, and manage agent tokens.</p></div>
      <button v-if="authStore.principalKind !== 'tenant'" class="btn btn-primary" data-test="open-client-connect" @click="connectOpen = true">Connect client</button>
    </div>
    <div role="tablist" class="tabs tabs-boxed w-fit" data-test="clients-tabs">
      <button v-for="item in tabs" :key="item.id" class="tab" :class="tab === item.id && 'tab-active'" @click="selectTab(item.id)">{{ item.label }}</button>
    </div>
    <section v-if="tab === 'clients'" class="space-y-3">
      <div v-if="store.loading" class="py-12 text-center"><span class="loading loading-spinner loading-lg" /></div>
      <div v-else-if="store.error" class="alert alert-error">{{ store.error }}</div>
      <div v-else class="overflow-x-auto rounded-box border border-base-300 bg-base-100">
        <table class="table"><thead><tr><th>Client</th><th>State</th><th>Last seen</th><th>Sessions</th><th>Config path</th></tr></thead>
          <tbody><template v-for="client in store.clients" :key="client.id"><tr class="cursor-pointer hover" :class="focusedClient === client.id && 'bg-primary/10'" :data-test="focusedClient === client.id ? 'focused-client-row' : undefined" @click="toggle(client.id)"><td class="font-medium">{{ client.display_name }}</td><td><span class="badge badge-sm">{{ stateLabel(client.state) }}</span><button v-if="client.connection_unverified" type="button" class="btn btn-ghost btn-xs ml-2" data-test="check-client-connection" @click.stop="checkConnection(client.id)">Check connection</button></td><td>{{ relative(client.last_seen) }}</td><td>{{ client.active_sessions }}</td><td><code class="text-xs">{{ client.display_path || '—' }}</code></td></tr>
          <tr v-if="expanded === client.id"><td colspan="5" class="bg-base-200/40">
            <p v-if="client.reload_hint" class="text-sm mb-2">{{ client.reload_hint }}</p>
            <div v-if="clientScopeAvailable" class="flex gap-3 mb-2 text-sm">
              <router-link class="link" :to="scopeQuery.linkTo('activity', { view: 'calls', client: client.id })">Activity</router-link>
              <router-link class="link" :to="scopeQuery.linkTo('tools', { client: client.id })">Tools</router-link>
              <router-link class="link" :to="scopeQuery.linkTo('usage', { client: client.id })">Usage</router-link>
            </div>
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
      </div>
    </section>
    <section v-else-if="tab === 'endpoint'" class="card bg-base-100 border border-base-300"><div class="card-body"><h2 class="card-title">Endpoint &amp; mode</h2><p class="text-sm text-base-content/70">Choose the MCP surface this instance serves. Changes are saved immediately and apply after restart.</p><ModeSwitcher /><dl v-if="store.routing" class="grid sm:grid-cols-2 gap-2 text-sm"><template v-for="(endpoint, name) in store.routing.endpoints" :key="name"><dt class="font-medium">{{ name }}</dt><dd><code>{{ endpoint }}</code></dd></template></dl><p v-if="store.routing?.restart_required" class="alert alert-warning text-sm">Restart MCPProxy to apply {{ store.routing.pending_routing_mode }} mode.</p></div></section>
    <AgentTokens v-else />
    <ClientConnectList :show="connectOpen" @close="connectOpen = false" @updated="refreshClients" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useClientsStore } from '@/stores/clients'
import { useAuthStore } from '@/stores/auth'
import ClientConnectList from '@/components/ClientConnectList.vue'
import ModeSwitcher from '@/components/ModeSwitcher.vue'
import AgentTokens from '@/views/AgentTokens.vue'
import { useSystemStore } from '@/stores/system'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'

const route = useRoute(); const router = useRouter(); const store = useClientsStore(); const authStore = useAuthStore(); const systemStore = useSystemStore()
const tabs = [{ id: 'clients', label: 'Clients' }, { id: 'endpoint', label: 'Endpoint & mode' }, { id: 'tokens', label: 'Agent tokens' }]
const defaultTab = 'clients'
function validTab(value: unknown): value is string { return typeof value === 'string' && tabs.some(item => item.id === value) }
const tab = ref(validTab(route.query.tab) ? route.query.tab : defaultTab)
const expanded = ref('')
const focusedClient = ref('')
const connectOpen = ref(false)
const scopeQuery = useScopeQuery('clients')
const clientScopeAvailable = computed(() => isScopeParamAvailable('client'))
const snippetCopied = ref(false)
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
  const query = { ...route.query }
  if (value === defaultTab) delete query.tab
  else query.tab = value
  if (route.query.tab !== query.tab) void router.replace({ query })
})
watch(() => route.query.tab, value => {
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
function refreshClients() { void store.load() }
function stateLabel(value: string) { return value.replaceAll('_', ' ') }
function relative(value?: string | null) { return value ? new Date(value).toLocaleString() : 'Never' }
onMounted(() => { if (authStore.principalKind !== 'tenant') void store.load() })
</script>
