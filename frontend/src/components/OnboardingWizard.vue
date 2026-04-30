<template>
  <dialog :open="show" class="modal modal-bottom sm:modal-middle">
    <div
      class="modal-box p-0 overflow-hidden flex flex-col"
      :style="modalSizing"
    >
      <!-- Header: title + close -->
      <div class="flex items-start justify-between px-6 pt-5 pb-3">
        <div>
          <h3 class="font-bold text-lg">MCPProxy setup</h3>
          <p class="text-xs opacity-60 mt-1">
            <template v-if="onboarding.incompleteTabCount > 0">
              {{ onboarding.incompleteTabCount }}
              {{ onboarding.incompleteTabCount === 1 ? 'step' : 'steps' }}
              still to do
            </template>
            <template v-else>You're all set up.</template>
          </p>
        </div>
        <button class="btn btn-ghost btn-sm btn-square" @click="dismiss" aria-label="Close">✕</button>
      </div>

      <!-- Tabs -->
      <div role="tablist" class="tabs tabs-bordered px-6 shrink-0">
        <a
          v-for="tab in tabs"
          :key="tab.id"
          role="tab"
          class="tab gap-2"
          :class="{ 'tab-active text-primary': activeTab === tab.id }"
          :data-test="`tab-${tab.id}`"
          @click="activeTab = tab.id"
        >
          <span
            class="inline-flex items-center justify-center w-5 h-5 rounded-full text-[11px] font-semibold"
            :class="tab.complete ? 'bg-success text-success-content' : 'bg-base-300 text-base-content/60'"
          >
            <span v-if="tab.complete">✓</span>
            <span v-else>{{ tab.idx }}</span>
          </span>
          <span>{{ tab.label }}</span>
        </a>
      </div>

      <!-- Body (scrollable) -->
      <div class="px-6 py-4 overflow-y-auto flex-1">
        <!-- ============================ -->
        <!-- Tab: Clients -->
        <!-- ============================ -->
        <section v-if="activeTab === 'clients'" data-test="panel-clients">
          <p class="text-sm opacity-70 mb-4">
            Pick at least one AI tool. MCPProxy registers itself in that tool's config so the assistant can talk to mcpproxy. A timestamped backup is created before any file is modified.
          </p>

          <div v-if="loadingClients" class="flex justify-center py-6">
            <span class="loading loading-spinner loading-md"></span>
          </div>
          <div v-else-if="clientsError" class="alert alert-error mb-2">
            <span class="text-sm">{{ clientsError }}</span>
          </div>

          <template v-else>
            <!-- Detected clients -->
            <div v-if="detectedClients.length > 0" class="space-y-2 mb-4">
              <div class="text-[11px] font-semibold uppercase tracking-wider opacity-50">Detected on this machine</div>
              <ClientRow
                v-for="c in detectedClients"
                :key="c.id"
                :client="c"
                :busy="busyClients[c.id]"
                @connect="connectOne"
              />
            </div>

            <!-- Pinned trio (only the ones not already in detected) -->
            <div v-if="pinnedClients.length > 0" class="space-y-2 mb-4">
              <div class="text-[11px] font-semibold uppercase tracking-wider opacity-50">Most popular</div>
              <ClientRow
                v-for="c in pinnedClients"
                :key="c.id"
                :client="c"
                :busy="busyClients[c.id]"
                @connect="connectOne"
              />
            </div>

            <!-- Collapsible: more clients -->
            <details v-if="moreClients.length > 0" class="group mb-2">
              <summary class="cursor-pointer text-sm opacity-70 hover:opacity-100 select-none flex items-center gap-1 py-1">
                <span class="transition-transform group-open:rotate-90">▸</span>
                Show {{ moreClients.length }} more {{ moreClients.length === 1 ? 'client' : 'clients' }}
              </summary>
              <div class="space-y-2 mt-2">
                <ClientRow
                  v-for="c in moreClients"
                  :key="c.id"
                  :client="c"
                  :busy="busyClients[c.id]"
                  @connect="connectOne"
                />
              </div>
            </details>

            <div v-if="connectMessage" class="mt-3">
              <div class="alert alert-sm" :class="connectMessageOk ? 'alert-success' : 'alert-error'">
                <span class="text-sm">{{ connectMessage }}</span>
              </div>
            </div>
          </template>

          <!-- Inline security expander (Spec 046 v2 FR-V06) -->
          <details class="mt-6 border-t border-base-300 pt-4">
            <summary class="cursor-pointer text-sm font-medium opacity-80 hover:opacity-100 select-none flex items-center gap-1">
              <span class="transition-transform group-open:rotate-90">▸</span>
              Show security settings
            </summary>
            <div class="mt-3 space-y-3 text-sm">
              <div class="flex items-start gap-3">
                <div class="flex-1 min-w-0">
                  <div class="font-medium">Bind interface</div>
                  <div class="text-xs opacity-60 mt-0.5">
                    mcpproxy is listening on <code class="font-mono text-[11px]">{{ listenAddr || 'localhost' }}</code>.
                    To expose it on the LAN, edit <span class="opacity-80">listen</span> in
                    <router-link to="/settings" class="link link-primary">Settings → Configuration</router-link>.
                  </div>
                </div>
              </div>
              <div class="flex items-start gap-3">
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm mt-0.5"
                  :checked="requireMcpAuth"
                  :disabled="securityBusy"
                  @change="onToggleRequireAuth(($event.target as HTMLInputElement).checked)"
                  data-test="toggle-require-mcp-auth"
                />
                <div class="flex-1 min-w-0">
                  <div class="font-medium">Require API key on /mcp</div>
                  <div class="text-xs opacity-60 mt-0.5">
                    On by default for LAN-bound mcpproxy. Off-by-default keeps localhost-only setup frictionless.
                  </div>
                </div>
              </div>
            </div>
          </details>
        </section>

        <!-- ============================ -->
        <!-- Tab: Servers -->
        <!-- ============================ -->
        <section v-else-if="activeTab === 'servers'" data-test="panel-servers">
          <p class="text-sm opacity-70 mb-4">
            Pick which servers from your existing AI clients to import. Same name in multiple sources? We auto-rename collisions like
            <code class="font-mono text-[11px] bg-base-200 px-1 rounded">mcpproxy_claude_code</code> so each entry stays distinct.
          </p>

          <!-- Detected import sources (Spec 046 v2 — sectioned checkbox layout) -->
          <div v-if="loadingImportSources" class="flex justify-center py-4">
            <span class="loading loading-spinner loading-md"></span>
          </div>
          <div v-else-if="importSourcesWithServers.length === 0" class="text-sm opacity-60 py-4 text-center">
            No client configs with importable servers detected on this machine.
          </div>
          <div v-else class="border border-base-300 rounded-lg overflow-hidden mb-5">
            <div
              v-for="(src, idx) in importSourcesWithServers"
              :key="src.path"
              :data-test="`import-section-${src.format}`"
              :class="idx > 0 ? 'border-t border-base-300' : ''"
            >
              <!-- Section header: client name + path + select-all checkbox -->
              <label class="flex items-start gap-3 px-3 py-2 bg-base-200/50 cursor-pointer">
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm mt-0.5"
                  :checked="isAllSelected(src)"
                  :indeterminate.prop="isIndeterminate(src)"
                  :data-test="`select-all-${src.format}`"
                  @change="toggleAllInSource(src, ($event.target as HTMLInputElement).checked)"
                />
                <div class="min-w-0 flex-1">
                  <div class="font-medium text-sm flex items-center gap-2">
                    <span>{{ src.name }}</span>
                    <span class="text-[11px] opacity-50">— {{ src.serverCount }} server{{ src.serverCount === 1 ? '' : 's' }}</span>
                  </div>
                  <div class="text-[11px] opacity-50 truncate font-mono" :title="src.path">{{ src.path }}</div>
                </div>
              </label>
              <!-- Server rows (indented + with vertical guide so the visual
                   hierarchy 'this server belongs to that client' is obvious) -->
              <ul class="divide-y divide-base-300">
                <li
                  v-for="name in src.serverNames"
                  :key="name"
                  class="flex items-center gap-3 pl-10 pr-3 py-2 relative hover:bg-base-200/40"
                >
                  <!-- Vertical guide -->
                  <span class="absolute left-5 top-0 bottom-0 w-px bg-base-300" aria-hidden="true"></span>
                  <label class="flex items-center gap-3 flex-1 min-w-0 cursor-pointer">
                    <input
                      type="checkbox"
                      class="checkbox checkbox-sm"
                      :checked="isSelected(src.path, name)"
                      :data-test="`server-checkbox-${src.format}-${name}`"
                      @change="toggleServer(src.path, name, ($event.target as HTMLInputElement).checked)"
                    />
                    <span class="text-sm truncate">{{ name }}</span>
                  </label>
                  <span
                    v-if="conflictTarget(src, name)"
                    class="badge badge-warning badge-sm gap-1 shrink-0 font-normal"
                    :title="`This name conflicts across sources — will be imported as ${conflictTarget(src, name)}`"
                  >
                    →
                    <code class="font-mono text-[11px]">{{ conflictTarget(src, name) }}</code>
                  </span>
                </li>
              </ul>
            </div>
          </div>

          <!-- Selection error (after import attempt) -->
          <div v-if="selectionImportMessage" class="alert mb-4" :class="selectionImportOk ? 'alert-success' : 'alert-error'">
            <span class="text-sm">{{ selectionImportMessage }}</span>
          </div>

          <!-- Compact toggles (Spec 046 v2 FR-V07) -->
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-4">
            <label class="cursor-pointer flex items-start gap-3 p-3 rounded-lg border border-base-300">
              <input
                type="checkbox"
                class="checkbox checkbox-sm mt-0.5"
                :checked="dockerIsolationDefault"
                :disabled="securityBusy"
                @change="onToggleDockerIsolation(($event.target as HTMLInputElement).checked)"
                data-test="toggle-docker-isolation"
              />
              <div class="flex-1 min-w-0">
                <div class="font-medium text-sm">Docker isolation</div>
                <div class="text-xs opacity-60 mt-0.5">
                  Runs stdio servers in throwaway containers.
                </div>
                <div
                  v-if="dockerIsolationDefault && dockerStatus !== null && !dockerStatus"
                  class="alert alert-warning mt-2 py-2 px-3 text-xs"
                  data-test="docker-warning"
                >
                  <span>⚠️ Docker isn't detected. Install or start Docker Desktop, or stdio servers will run unsandboxed.</span>
                </div>
              </div>
            </label>
            <label class="cursor-pointer flex items-start gap-3 p-3 rounded-lg border border-base-300">
              <input
                type="checkbox"
                class="checkbox checkbox-sm mt-0.5"
                :checked="quarantineEnabled"
                :disabled="securityBusy"
                @change="onToggleQuarantine(($event.target as HTMLInputElement).checked)"
                data-test="toggle-quarantine"
              />
              <div class="flex-1 min-w-0">
                <div class="font-medium text-sm">Quarantine new tools</div>
                <div class="text-xs opacity-60 mt-0.5">
                  Hold every newly added server in a safe zone until you approve it. Defends against tool poisoning and rug-pulls.
                </div>
              </div>
            </label>
          </div>

          <!-- Scanner / post-hoc quarantine note (Spec 046 v2) -->
          <div class="alert alert-info text-sm mb-4" data-test="scanner-note">
            <div class="flex flex-col items-start gap-1">
              <div class="font-semibold">Already imported a server?</div>
              <div class="opacity-80">
                You can run security scanners on any server and move it back into quarantine if anything looks off — open the server in
                <router-link to="/servers" class="link link-primary">Servers</router-link> and use <em>Run scan</em>.
                Scanners (Trivy, Semgrep, MCP Scan) flag tool-poisoning, prompt-injection, and supply-chain risks.
              </div>
            </div>
          </div>

          <details class="border border-base-300 rounded-lg p-3 text-sm">
            <summary class="cursor-pointer font-medium flex items-center gap-2 select-none">
              <span class="transition-transform group-open:rotate-90">▸</span>
              Add a single server manually instead
            </summary>
            <div class="mt-3">
              <button
                class="btn btn-primary btn-sm w-full"
                @click="openAddServer"
                data-test="add-server-button"
              >
                Open the add-server form
              </button>
              <p v-if="serverAddedJustNow" class="text-xs text-success mt-2">
                ✓ Server added — it's currently in quarantine. Review it on the Servers page after this wizard.
              </p>
              <p v-else-if="onboarding.hasConfiguredServer" class="text-xs opacity-60 mt-2">
                {{ serverCountLabel }} configured.
              </p>
            </div>
          </details>
        </section>

        <!-- ============================ -->
        <!-- Tab: Verify -->
        <!-- ============================ -->
        <section v-else-if="activeTab === 'verify'" data-test="panel-verify">
          <template v-if="onboarding.firstMCPClientEver">
            <div class="flex flex-col items-center gap-2 py-6 text-center">
              <div class="text-4xl">✅</div>
              <div class="font-semibold text-lg">Round-trip verified</div>
              <div class="text-sm opacity-70 max-w-md">
                We've seen at least one MCP request from your AI client(s). mcpproxy is wired up correctly.
              </div>
              <div v-if="onboarding.mcpClientsSeenEver.length > 0" class="text-xs opacity-60 mt-2">
                Recognized: <span class="font-medium">{{ onboarding.mcpClientsSeenEver.join(', ') }}</span>
              </div>
            </div>
          </template>
          <template v-else>
            <div class="flex flex-col items-center gap-3 py-6 text-center">
              <div class="text-4xl opacity-50">📡</div>
              <div class="font-semibold text-lg">Waiting for your first request</div>
              <div class="text-sm opacity-70 max-w-md">
                Open your AI agent and ask it to call <code class="font-mono text-[12px]">retrieve_tools</code> through mcpproxy. We'll detect the round-trip live.
              </div>
              <div class="flex items-center gap-2 mt-2 text-xs opacity-60">
                <span class="loading loading-dots loading-sm"></span>
                <span>Listening…</span>
              </div>
            </div>
          </template>

          <!-- Quick prompt suggestions: each one exercises a different built-
               in mcpproxy tool so the user can see the proxy's value surface
               immediately. -->
          <div class="mt-4 border-t border-base-300 pt-4">
            <div class="text-[11px] font-semibold uppercase tracking-wider opacity-50 mb-2">Try one of these prompts</div>
            <ul class="space-y-1.5" data-test="verify-sample-prompts">
              <li class="bg-base-200 rounded-lg p-2.5 text-sm font-mono">
                "Search for MCP filesystem tools."
                <span class="text-[11px] opacity-50 ml-2 not-italic font-sans">→ retrieve_tools</span>
              </li>
              <li class="bg-base-200 rounded-lg p-2.5 text-sm font-mono">
                "List my upstream MCP servers and their connection status."
                <span class="text-[11px] opacity-50 ml-2 not-italic font-sans">→ upstream_servers</span>
              </li>
              <li class="bg-base-200 rounded-lg p-2.5 text-sm font-mono">
                "Show me tools pending quarantine approval in mcpproxy."
                <span class="text-[11px] opacity-50 ml-2 not-italic font-sans">→ quarantine_security</span>
              </li>
            </ul>
          </div>

          <!-- Recent activity preview — every MCP request is observable here
               and on the Activity Log page (Spec 046 v2) -->
          <div class="mt-4 border-t border-base-300 pt-4" data-test="verify-activity-section">
            <div class="flex items-center justify-between mb-2">
              <div class="text-[11px] font-semibold uppercase tracking-wider opacity-50">Recent activity</div>
              <router-link to="/activity" class="text-[11px] link link-primary opacity-80 hover:opacity-100" data-test="link-activity-log">
                View all in Activity Log →
              </router-link>
            </div>
            <div v-if="loadingActivity" class="flex justify-center py-3">
              <span class="loading loading-spinner loading-sm"></span>
            </div>
            <div
              v-else-if="recentActivity.length === 0"
              class="bg-base-200 rounded-lg p-3 text-sm opacity-70 text-center"
              data-test="verify-activity-empty"
            >
              Once your AI starts calling tools, every request shows up here — and in the
              <router-link to="/activity" class="link link-primary">Activity Log</router-link>.
            </div>
            <ul v-else class="space-y-1.5" data-test="verify-activity-list">
              <li
                v-for="rec in recentActivity"
                :key="rec.id"
                class="flex items-center gap-3 px-3 py-2 rounded-lg bg-base-200/60 text-sm"
              >
                <span
                  class="badge badge-xs shrink-0"
                  :class="rec.status === 'success' ? 'badge-success' : rec.status === 'error' ? 'badge-error' : 'badge-warning'"
                  :title="rec.status"
                >{{ rec.status === 'success' ? '✓' : rec.status === 'error' ? '✗' : '!' }}</span>
                <span class="font-mono text-xs opacity-60 shrink-0">{{ formatTime(rec.timestamp) }}</span>
                <span class="font-medium truncate flex-1">
                  <span v-if="rec.server_name" class="opacity-70">{{ rec.server_name }}:</span>{{ rec.tool_name || rec.type }}
                </span>
                <span v-if="rec.duration_ms !== undefined" class="text-xs opacity-50 shrink-0">{{ rec.duration_ms }}ms</span>
              </li>
            </ul>
          </div>
        </section>
      </div>

      <!-- Footer (sticky, non-scrollable) -->
      <!-- Servers tab gets a dedicated import-action footer when at least one
           detectable server source exists. The action buttons stay visible
           even as the list above scrolls. -->
      <div
        v-if="activeTab === 'servers' && importSourcesWithServers.length > 0"
        class="flex items-center justify-between gap-3 px-6 py-3 border-t border-base-300 shrink-0 bg-base-200/40"
      >
        <div class="text-xs">
          <span v-if="selectedCount === 0" class="opacity-50">Select at least one server to import</span>
          <span v-else>
            <span class="font-semibold text-primary">{{ selectedCount }}</span>
            <span class="opacity-70"> selected</span>
            <span v-if="conflictCount > 0" class="opacity-70">
              · <span class="text-warning">{{ conflictCount }} renamed</span>
            </span>
          </span>
        </div>
        <div class="flex items-center gap-2">
          <button
            class="btn btn-ghost btn-sm"
            @click="dismiss"
            data-test="close-wizard"
          >Close</button>
          <button
            class="btn btn-secondary btn-sm gap-1 min-w-[180px]"
            :disabled="selectedCount === 0 || importBusyAny"
            @click="onBulkImport(false)"
            data-test="bulk-import-active"
          >
            <span v-if="bulkImportBusy === 'active'" class="loading loading-spinner loading-xs"></span>
            <span v-else>⚡</span>
            Import as active
          </button>
          <button
            class="btn btn-primary btn-sm gap-1 min-w-[180px]"
            :disabled="selectedCount === 0 || importBusyAny"
            @click="onBulkImport(true)"
            data-test="bulk-import-quarantine"
          >
            <span v-if="bulkImportBusy === 'quarantine'" class="loading loading-spinner loading-xs"></span>
            <span v-else>🛡</span>
            Import &amp; quarantine
          </button>
        </div>
      </div>
      <!-- Default footer for other tabs / empty server state -->
      <div
        v-else
        class="flex items-center justify-between px-6 py-3 border-t border-base-300 shrink-0"
      >
        <div class="text-xs opacity-50">
          Tip: you can always re-open this from the sidebar's <span class="font-medium">Setup</span> entry.
        </div>
        <button class="btn btn-primary btn-sm" @click="dismiss" data-test="close-wizard">
          {{ onboarding.incompleteTabCount === 0 ? 'Done' : 'Close for now' }}
        </button>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop" @click.prevent="dismiss"><button>close</button></form>
  </dialog>

  <!-- Embedded AddServerModal for the server tab -->
  <AddServerModal
    :show="addServerOpen"
    @close="addServerOpen = false"
    @added="onServerAdded"
  />
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted, h, type FunctionalComponent } from 'vue'
import api from '@/services/api'
import { useOnboardingStore } from '@/stores/onboarding'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import AddServerModal from '@/components/AddServerModal.vue'
import type { ClientStatus, ActivityRecord } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const onboarding = useOnboardingStore()
const systemStore = useSystemStore()
const serversStore = useServersStore()

type TabID = 'clients' | 'servers' | 'verify'
const activeTab = ref<TabID>('clients')

const clients = ref<ClientStatus[]>([])
const loadingClients = ref(false)
const clientsError = ref<string | null>(null)
const busyClients = reactive<Record<string, boolean>>({})
const connectMessage = ref('')
const connectMessageOk = ref(true)
const addServerOpen = ref(false)
const serverAddedJustNow = ref(false)

const requireMcpAuth = ref(false)
const dockerIsolationDefault = ref(true)
const quarantineEnabled = ref(true)
const listenAddr = ref('')
const securityBusy = ref(false)
// null = not yet known (don't show warning); true/false = detected state
const dockerStatus = ref<boolean | null>(null)

// Spec 046 v2 — server-import preview rows.
interface ImportSource {
  name: string
  format: string
  path: string
  exists: boolean
  previewLoading: boolean
  previewError: string
  serverCount: number
  serverNames: string[]
}
const importSources = ref<ImportSource[]>([])
const loadingImportSources = ref(false)

// Verify tab — recent activity preview.
const recentActivity = ref<ActivityRecord[]>([])
const loadingActivity = ref(false)

// Selection: keyed by `${path}::${serverName}`. Default unchecked.
const selection = ref<Set<string>>(new Set())
const bulkImportBusy = ref<'' | 'quarantine' | 'active'>('')
const importBusyAny = computed(() => bulkImportBusy.value !== '')
const selectionImportMessage = ref('')
const selectionImportOk = ref(false)

const importSourcesWithServers = computed(() =>
  importSources.value.filter(s => s.serverCount > 0)
)

function selectionKey(path: string, name: string) {
  return `${path}::${name}`
}
function isSelected(path: string, name: string): boolean {
  return selection.value.has(selectionKey(path, name))
}
function toggleServer(path: string, name: string, on: boolean) {
  const key = selectionKey(path, name)
  // Vue's reactivity needs a fresh Set instance for refs holding Set/Map.
  const next = new Set(selection.value)
  if (on) next.add(key)
  else next.delete(key)
  selection.value = next
}
function isAllSelected(src: ImportSource): boolean {
  return src.serverNames.length > 0 && src.serverNames.every(n => isSelected(src.path, n))
}
function isIndeterminate(src: ImportSource): boolean {
  const some = src.serverNames.some(n => isSelected(src.path, n))
  return some && !isAllSelected(src)
}
function toggleAllInSource(src: ImportSource, on: boolean) {
  const next = new Set(selection.value)
  for (const n of src.serverNames) {
    const key = selectionKey(src.path, n)
    if (on) next.add(key)
    else next.delete(key)
  }
  selection.value = next
}

const selectedCount = computed(() => selection.value.size)

// Build the conflict map across SELECTED servers only. A name is conflicted
// iff it is selected from 2+ sources. Conflicted servers are renamed to
// `<originalName>_<format>` so the resulting mcpproxy entries are distinct.
const conflictTargets = computed<Map<string, string>>(() => {
  // name -> Set<path> for selected servers
  const namesToPaths = new Map<string, Set<string>>()
  for (const key of selection.value) {
    const sep = key.indexOf('::')
    if (sep === -1) continue
    const path = key.slice(0, sep)
    const name = key.slice(sep + 2)
    if (!namesToPaths.has(name)) namesToPaths.set(name, new Set())
    namesToPaths.get(name)!.add(path)
  }
  // For each name selected from 2+ sources, mark all (path, name) → renamed.
  const out = new Map<string, string>() // selectionKey -> newName
  for (const [name, paths] of namesToPaths) {
    if (paths.size < 2) continue
    for (const path of paths) {
      const src = importSources.value.find(s => s.path === path)
      if (!src) continue
      out.set(selectionKey(path, name), `${name}_${src.format.replace(/-/g, '_')}`)
    }
  }
  return out
})
function conflictTarget(src: ImportSource, name: string): string | undefined {
  return conflictTargets.value.get(selectionKey(src.path, name))
}
const conflictCount = computed(() => conflictTargets.value.size)

let pollHandle: ReturnType<typeof setInterval> | null = null

// Tabs config
const tabs = computed(() => [
  {
    id: 'clients' as TabID,
    label: 'Clients',
    idx: 1,
    complete: onboarding.hasConnectedClient,
  },
  {
    id: 'servers' as TabID,
    label: 'Servers',
    idx: 2,
    complete: onboarding.hasConfiguredServer,
  },
  {
    id: 'verify' as TabID,
    label: 'Verify',
    idx: 3,
    complete: onboarding.firstMCPClientEver,
  },
])

// Modal sizing per spec FR-V10
const modalSizing = computed(() => ({
  width: 'min(960px, 90vw)',
  maxWidth: 'min(960px, 90vw)',
  height: 'min(640px, 85vh)',
  maxHeight: 'min(640px, 85vh)',
}))

// --- Client sort: detected → pinned trio → others -----------------------
const PINNED_TRIO: readonly string[] = ['claude-code', 'codex', 'gemini']

const detectedClients = computed(() =>
  clients.value.filter(c => c.exists)
)
const pinnedClients = computed(() =>
  PINNED_TRIO
    .map(id => clients.value.find(c => c.id === id))
    .filter((c): c is ClientStatus => !!c && !c.exists)
)
const moreClients = computed(() => {
  const detectedIds = new Set(detectedClients.value.map(c => c.id))
  const pinnedIds = new Set(pinnedClients.value.map(c => c.id))
  return clients.value.filter(c => !detectedIds.has(c.id) && !pinnedIds.has(c.id))
})

const serverCountLabel = computed(() => {
  const n = onboarding.state?.configured_server_count ?? 0
  return n === 1 ? '1 server' : `${n} servers`
})

// Open lifecycle: refresh state, fetch clients + config, start polling.
watch(() => props.show, async (open) => {
  if (open) {
    serverAddedJustNow.value = false
    connectMessage.value = ''
    await onboarding.fetchState()
    await Promise.all([
      fetchClients(),
      fetchSecurityState(),
      fetchDockerStatus(),
      fetchImportSources(),
      fetchRecentActivity(),
    ])
    activeTab.value = pickInitialTab()
    startPolling()
  } else {
    stopPolling()
  }
})

function pickInitialTab(): TabID {
  if (!onboarding.hasConnectedClient) return 'clients'
  if (!onboarding.hasConfiguredServer) return 'servers'
  if (!onboarding.firstMCPClientEver) return 'verify'
  return 'clients'
}

function startPolling() {
  stopPolling()
  // Poll while wizard is open so the Verify tab flips to green within ~5s
  // of the AfterInitialize hook firing without an SSE channel. Also
  // refresh the Verify tab's recent-activity panel on the same cadence so
  // newly-arrived MCP requests are reflected without manual reload.
  pollHandle = setInterval(() => {
    void onboarding.fetchState()
    if (activeTab.value === 'verify') {
      void fetchRecentActivity()
    }
  }, 5000)
}

function stopPolling() {
  if (pollHandle) {
    clearInterval(pollHandle)
    pollHandle = null
  }
}

onUnmounted(() => stopPolling())

async function fetchClients() {
  loadingClients.value = true
  clientsError.value = null
  try {
    const res = await api.getConnectStatus()
    if (res.success && res.data) {
      clients.value = Array.isArray(res.data) ? res.data : []
    } else {
      clientsError.value = res.error ?? 'Failed to load client status'
    }
  } catch (err) {
    clientsError.value = (err as Error).message
  } finally {
    loadingClients.value = false
  }
}

async function fetchRecentActivity() {
  loadingActivity.value = true
  try {
    const res = await api.getActivities({ limit: 5 })
    if (res.success && res.data) {
      recentActivity.value = res.data.activities ?? []
    }
  } catch {
    // graceful — keep prior list
  } finally {
    loadingActivity.value = false
  }
}

function formatTime(ts: string): string {
  const d = new Date(ts)
  const now = Date.now()
  const diff = now - d.getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return d.toLocaleDateString()
}

async function fetchSecurityState() {
  try {
    const res = await api.getConfig()
    if (res.success && res.data) {
      const cfg = res.data.config ?? {}
      requireMcpAuth.value = !!cfg.require_mcp_auth
      quarantineEnabled.value = cfg.quarantine_enabled ?? true
      listenAddr.value = cfg.listen ?? ''
      const iso = cfg.docker_isolation ?? cfg.isolation ?? null
      dockerIsolationDefault.value = iso?.enabled ?? true
    }
  } catch {
    // graceful degradation
  }
}

async function fetchDockerStatus() {
  // Spec 046 v2: feed the Docker isolation toggle's "is Docker actually
  // available?" warning. We mirror the Dashboard's logic so the message is
  // consistent: if Docker reports unavailable but at least one connected
  // stdio server is running, treat as available (the Docker health checker
  // can lag behind reality).
  try {
    const res = await api.getDockerStatus()
    if (res.success && res.data) {
      let available = res.data.docker_available ?? false
      if (!available && serversStore.servers.some(s => s.connected && s.protocol === 'stdio')) {
        available = true
      }
      dockerStatus.value = available
    } else {
      dockerStatus.value = false
    }
  } catch {
    dockerStatus.value = false
  }
}

async function fetchImportSources() {
  // Spec 046 v2: parity with the Clients tab. Discover canonical client
  // config paths, then for each existing one, run a no-side-effect preview
  // to count importable servers.
  loadingImportSources.value = true
  try {
    const res = await api.getCanonicalConfigPaths()
    if (!res.success || !res.data) {
      importSources.value = []
      return
    }
    const sources: ImportSource[] = res.data.paths
      .filter(p => p.exists)
      .map(p => ({
        name: p.name,
        format: p.format,
        path: p.path,
        exists: true,
        previewLoading: true,
        previewError: '',
        serverCount: 0,
        serverNames: [],
        importBusy: '',
        importMessage: '',
        importMessageOk: false,
      }))
    importSources.value = sources

    // Run previews in parallel — each is bounded by the user's local file
    // size, so this is safe even with many sources.
    await Promise.all(
      sources.map(async (src, idx) => {
        try {
          const r = await api.importServersFromPath({
            path: src.path,
            format: src.format,
            preview: true,
          })
          if (r.success && r.data) {
            const imported = r.data.imported ?? []
            importSources.value[idx] = {
              ...src,
              previewLoading: false,
              serverCount: imported.length,
              serverNames: imported.map(s => s.name),
            }
          } else {
            importSources.value[idx] = {
              ...src,
              previewLoading: false,
              previewError: r.error ?? 'preview failed',
            }
          }
        } catch (err) {
          importSources.value[idx] = {
            ...src,
            previewLoading: false,
            previewError: (err as Error).message,
          }
        }
      })
    )
  } finally {
    loadingImportSources.value = false
  }
}

async function onBulkImport(quarantine: boolean) {
  if (selection.value.size === 0) return
  bulkImportBusy.value = quarantine ? 'quarantine' : 'active'
  selectionImportMessage.value = ''

  // Group selected (path, name) pairs by source path. Build per-source
  // server_names + rename map (only for entries flagged as conflicts).
  type Job = {
    src: ImportSource
    serverNames: string[]
    rename: Record<string, string>
  }
  const jobs = new Map<string, Job>()
  for (const key of selection.value) {
    const sep = key.indexOf('::')
    if (sep === -1) continue
    const path = key.slice(0, sep)
    const name = key.slice(sep + 2)
    const src = importSources.value.find(s => s.path === path)
    if (!src) continue
    if (!jobs.has(path)) {
      jobs.set(path, { src, serverNames: [], rename: {} })
    }
    const job = jobs.get(path)!
    job.serverNames.push(name)
    const renamed = conflictTargets.value.get(key)
    if (renamed) job.rename[name] = renamed
  }

  let totalImported = 0
  let totalSkipped = 0
  let totalFailed = 0
  const errors: string[] = []
  try {
    const results = await Promise.all(
      Array.from(jobs.values()).map(async job => {
        const r = await api.importServersFromPath({
          path: job.src.path,
          format: job.src.format,
          server_names: job.serverNames,
          rename: Object.keys(job.rename).length > 0 ? job.rename : undefined,
          skip_quarantine: !quarantine,
        })
        return { job, r }
      })
    )
    for (const { job, r } of results) {
      if (r.success && r.data) {
        totalImported += r.data.summary?.imported ?? 0
        totalSkipped += r.data.summary?.skipped ?? 0
        totalFailed += r.data.summary?.failed ?? 0
      } else {
        errors.push(`${job.src.name}: ${r.error ?? 'unknown error'}`)
      }
    }
    selectionImportOk.value = errors.length === 0
    if (errors.length === 0) {
      const dest = quarantine ? 'into quarantine' : 'as active'
      let msg = `✓ Imported ${totalImported} server${totalImported === 1 ? '' : 's'} ${dest}`
      if (totalSkipped > 0) msg += ` · ${totalSkipped} skipped (already configured)`
      if (totalFailed > 0) msg += ` · ${totalFailed} failed`
      if (quarantine && totalImported > 0) msg += '. Approve from the Servers page.'
      selectionImportMessage.value = msg
      selection.value = new Set()
      systemStore.addToast({
        type: 'success',
        title: 'Import complete',
        message: `${totalImported} server${totalImported === 1 ? '' : 's'}${conflictCount.value > 0 ? ` (${conflictCount.value} renamed)` : ''}`,
      })
    } else {
      selectionImportMessage.value = `Some imports failed: ${errors.join(' · ')}`
    }
    await Promise.all([
      serversStore.fetchServers(),
      onboarding.fetchState(),
      // Refresh previews so already-imported servers drop off the list.
      fetchImportSources(),
    ])
  } catch (err) {
    selectionImportMessage.value = (err as Error).message
    selectionImportOk.value = false
  } finally {
    bulkImportBusy.value = ''
  }
}

async function patchConfig(patch: Record<string, unknown>) {
  // /api/v1/config/apply decodes the body directly into config.Config (no
  // {config: ...} wrapper). Settings.vue calls applyConfig(config) the same
  // bare way; the wizard's earlier {config: merged} wrap was sending an
  // unrelated outer object that the backend rejected with a stale field-
  // validation error ("tools_limit: must be between 1 and 1000"). Send
  // the bare config to match the existing contract.
  securityBusy.value = true
  try {
    const cur = await api.getConfig()
    if (!cur.success || !cur.data) {
      throw new Error(cur.error ?? 'failed to read config')
    }
    const merged = { ...cur.data.config, ...patch }
    const res = await api.applyConfig(merged)
    if (!res.success) {
      throw new Error(res.error ?? 'failed to apply config')
    }
    await fetchSecurityState()
    systemStore.addToast({ type: 'success', title: 'Settings saved', message: 'Updated' })
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Save failed',
      message: (err as Error).message,
    })
  } finally {
    securityBusy.value = false
  }
}

function onToggleRequireAuth(v: boolean) {
  void patchConfig({ require_mcp_auth: v })
}

function onToggleDockerIsolation(v: boolean) {
  // Toggle the global docker_isolation default. Per-server overrides are
  // unaffected. The Server tab's per-server form remains the source of
  // truth for granular control.
  void patchConfig({ docker_isolation: { enabled: v } })
}

function onToggleQuarantine(v: boolean) {
  void patchConfig({ quarantine_enabled: v })
}

async function connectOne(clientId: string) {
  busyClients[clientId] = true
  connectMessage.value = ''
  try {
    const res = await api.connectClient(clientId)
    if (res.success && res.data) {
      connectMessageOk.value = true
      connectMessage.value = res.data.message || `Connected ${clientId}`
      await fetchClients()
      await onboarding.fetchState()
      systemStore.addToast({
        type: 'success',
        title: 'Client connected',
        message: `mcpproxy registered in ${clientId}`,
      })
    } else {
      connectMessageOk.value = false
      connectMessage.value = res.error ?? 'Failed to connect'
    }
  } catch (err) {
    connectMessageOk.value = false
    connectMessage.value = (err as Error).message
  } finally {
    busyClients[clientId] = false
  }
}

function openAddServer() {
  addServerOpen.value = true
}

async function onServerAdded() {
  addServerOpen.value = false
  serverAddedJustNow.value = true
  await Promise.all([
    serversStore.fetchServers(),
    onboarding.fetchState(),
  ])
  systemStore.addToast({
    type: 'success',
    title: 'Server added',
    message: 'It is in quarantine. Review and approve from the Servers page.',
  })
}

async function dismiss() {
  // Engagement is permanent: once the wizard has been opened and dismissed,
  // we don't auto-popup again. The sidebar Setup entry remains visible so
  // the user can return.
  if (!onboarding.isEngaged) {
    await onboarding.markEngaged()
  }
  emit('close')
}

onMounted(() => {
  // If wizard is already open at mount time (rare; usually opened reactively
  // via :show), kick off initial load.
  if (props.show) {
    void onboarding.fetchState()
    void fetchClients()
    void fetchSecurityState()
    void fetchDockerStatus()
    void fetchImportSources()
    startPolling()
  }
})

// --- ClientRow component ------------------------------------------------
// Inlined as a functional component to keep this file self-contained while
// the row layout stays consistent across all three lists.
const ClientRow: FunctionalComponent<{ client: ClientStatus; busy?: boolean }, { connect: (id: string) => void }> = (props, { emit: rowEmit }) => {
  const c = props.client
  return h(
    'div',
    {
      class: 'flex items-center justify-between p-2 rounded-lg border border-base-300',
      'data-test': `client-row-${c.id}`,
    },
    [
      h('div', { class: 'min-w-0 flex-1' }, [
        h('div', { class: 'font-medium text-sm truncate' }, c.name),
        h('div', { class: 'text-xs opacity-50 truncate', title: c.config_path }, c.config_path),
      ]),
      h('div', { class: 'flex-shrink-0 ml-2' }, [
        !c.supported
          ? h('span', { class: 'badge badge-ghost badge-sm' }, c.reason || 'Not supported')
          : !c.exists
            ? h('span', { class: 'text-xs opacity-40' }, 'Not installed')
            : c.connected
              ? h('span', { class: 'badge badge-success badge-sm' }, 'Connected')
              : h(
                  'button',
                  {
                    class: 'btn btn-primary btn-xs',
                    disabled: props.busy,
                    'data-test': `connect-${c.id}`,
                    onClick: () => rowEmit('connect', c.id),
                  },
                  props.busy
                    ? [h('span', { class: 'loading loading-spinner loading-xs' })]
                    : ['Connect']
                ),
      ]),
    ]
  )
}
ClientRow.props = { client: { type: Object, required: true }, busy: { type: Boolean, default: false } }
ClientRow.emits = ['connect']
</script>
