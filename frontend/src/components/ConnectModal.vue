<template>
  <dialog :open="show" class="modal">
    <div class="modal-box max-w-lg">
      <h3 class="font-bold text-lg mb-2">Connect MCPProxy to AI Agents</h3>
      <p class="text-sm opacity-70 mb-4">
        Register MCPProxy as an MCP server in your AI tools. Clicking a client shows the exact change first — nothing is written until you confirm (backup created automatically).
      </p>

      <!-- Loading state -->
      <div v-if="loading.initial" class="flex justify-center py-8">
        <span class="loading loading-spinner loading-md"></span>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="alert alert-error mb-4">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span class="text-sm">{{ error }}</span>
      </div>

      <!-- Client list -->
      <div v-else class="space-y-2">
        <div
          v-for="client in mergedClients"
          :key="client.id"
          class="rounded-lg border border-base-300 hover:bg-base-200/50 transition-colors overflow-hidden"
          :class="accessState(client) === 'denied' ? 'border-error/40' : ''"
        >
          <div class="flex items-center justify-between p-3">
            <div class="flex items-center gap-3 min-w-0 flex-1">
              <div class="w-8 h-8 flex items-center justify-center text-lg shrink-0" :title="client.name">
                {{ clientIcon(client) }}
              </div>
              <div class="min-w-0 flex-1">
                <div class="font-medium text-sm truncate">{{ client.name }}</div>
                <div class="text-xs opacity-50 truncate" :title="client.config_path">{{ client.config_path }}</div>
                <div v-if="client.note" class="text-xs opacity-60 italic mt-0.5" :title="client.note">{{ client.note }}</div>
              </div>
            </div>
            <div class="shrink-0 ml-2 flex flex-col items-end gap-1">
              <span v-if="!client.supported" class="badge badge-ghost badge-sm">{{ client.reason || 'Not supported' }}</span>
              <!-- Spec 075 US2: macOS blocked reading this client's config. -->
              <span
                v-else-if="accessState(client) === 'denied'"
                data-test="connect-blocked-badge"
                class="badge badge-error badge-sm gap-1"
                title="macOS blocked access to this client's config (Privacy & Security ▸ App Data)"
              >
                <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" /></svg>
                Blocked
              </span>
              <!-- Spec 075 FR-003: config exists but is unparseable — distinct from a denial. -->
              <span
                v-else-if="accessState(client) === 'malformed'"
                data-test="connect-malformed-badge"
                class="badge badge-warning badge-sm"
                title="Config exists but could not be parsed"
              >Unreadable config</span>
              <span v-else-if="!client.exists && !client.bridge" class="text-xs opacity-40">Config not found</span>
              <button
                v-else-if="client.connected"
                @click="disconnect(client.id)"
                class="btn btn-ghost btn-xs text-error"
                :disabled="loading.clients[client.id]"
              >
                <span v-if="loading.clients[client.id]" class="loading loading-spinner loading-xs"></span>
                <span v-else>Disconnect</span>
              </button>
              <button
                v-else
                :data-test="`connect-start-${client.id}`"
                @click="startConnect(client.id)"
                class="btn btn-primary btn-xs"
                :disabled="loading.clients[client.id] || previewLoading[client.id]"
              >
                <span v-if="loading.clients[client.id] || previewLoading[client.id]" class="loading loading-spinner loading-xs"></span>
                <span v-else>Review & connect</span>
              </button>
              <!-- Spec 075 US1: explicit, no-eager-read access check. The stat-only
                   listing leaves installed clients 'unknown'; this is the only
                   action that reads the config (the sole macOS privacy-prompt site). -->
              <button
                v-if="client.exists && accessState(client) === 'unknown' && !client.connected"
                data-test="connect-check-access"
                @click="checkAccess(client.id)"
                class="btn btn-ghost btn-2xs h-auto min-h-0 py-0.5 text-[0.7rem] opacity-60 hover:opacity-100"
                :disabled="checking[client.id]"
                title="Read this client's config now to verify access (may prompt on macOS)"
              >
                <span v-if="checking[client.id]" class="loading loading-spinner loading-xs"></span>
                <span v-else>Check access</span>
              </button>
            </div>
          </div>
          <!-- Spec 075 US2: actionable remediation banner for a macOS App-Data denial. -->
          <div
            v-if="accessState(client) === 'denied'"
            data-test="connect-denied-banner"
            class="border-t border-error/30 bg-error/10 px-3 py-2 space-y-2"
          >
            <p class="text-xs whitespace-pre-line leading-relaxed">{{ client.remediation || defaultRemediation(client) }}</p>
            <div class="flex items-center gap-2">
              <button
                data-test="connect-copy-tccutil"
                @click="copyTccutil(client)"
                class="btn btn-xs btn-outline btn-error"
              >
                {{ copiedClient === client.id ? 'Copied ✓' : 'Copy reset command' }}
              </button>
              <button
                @click="checkAccess(client.id)"
                class="btn btn-xs btn-ghost"
                :disabled="checking[client.id]"
              >
                <span v-if="checking[client.id]" class="loading loading-spinner loading-xs"></span>
                <span v-else>Re-check</span>
              </button>
            </div>
          </div>
          <!-- Spec 078 US1 / FR-001,003,004: preview the exact change before it
               is written. Only this entry is added; nothing else in the file
               changes. Confirm/Cancel gate the actual write. -->
          <div
            v-if="previews[client.id]"
            :data-test="`connect-preview-${client.id}`"
            class="border-t border-base-300 bg-base-200/40 px-3 py-3 space-y-2"
          >
            <p class="text-xs opacity-70 leading-relaxed">
              Only this entry is added to
              <code class="font-mono text-[11px] break-all" :title="previews[client.id]!.config_path">{{ previews[client.id]!.config_path }}</code>.
              Everything else in the file stays untouched, and a timestamped backup is created first.
            </p>
            <!-- Overwrite warning (FR-003): an entry with this name already exists. -->
            <p
              v-if="previews[client.id]!.entry_exists"
              :data-test="`connect-preview-overwrite-${client.id}`"
              class="text-xs text-warning leading-relaxed"
            >
              An entry named “{{ previews[client.id]!.server_name }}” already exists — connecting will overwrite it (a backup is saved first).
            </p>
            <!-- Malformed config (Spec 075): the write parses the same bytes and
                 would fail, so connecting is blocked until the file is fixed. -->
            <p
              v-else-if="previews[client.id]!.access_state === 'malformed'"
              :data-test="`connect-preview-malformed-${client.id}`"
              class="text-xs text-warning leading-relaxed"
            >
              Your current config could not be parsed, so connecting would fail rather than modify an unreadable file. Fix or remove {{ previews[client.id]!.config_path }} first, then try again.
            </p>
            <!-- No prior file (bridge / absent): nothing to back up. -->
            <p
              v-else-if="previews[client.id]!.access_state === 'absent'"
              :data-test="`connect-preview-no-file-${client.id}`"
              class="text-xs opacity-60 leading-relaxed"
            >
              This file will be created; there is no prior file to back up.
            </p>
            <div>
              <div class="text-[11px] font-semibold uppercase tracking-wider text-success/80 mb-1">+ will be added</div>
              <pre
                :data-test="`connect-preview-entry-${client.id}`"
                class="text-[11px] font-mono whitespace-pre-wrap break-all rounded bg-base-300/60 border-l-2 border-success px-2 py-1.5 leading-relaxed"
              >{{ previews[client.id]!.entry_text }}</pre>
            </div>
            <!-- API-key honesty (FR-004): masked in the preview, real key written. -->
            <p
              v-if="previews[client.id]!.contains_api_key"
              :data-test="`connect-preview-apikey-${client.id}`"
              class="text-[11px] opacity-60 leading-relaxed"
            >
              This entry includes your API key (shown masked). The real key is written into the config so the client can authenticate.
            </p>
            <div class="flex items-center gap-2 pt-1">
              <button
                :data-test="`connect-preview-confirm-${client.id}`"
                @click="confirmConnect(client.id)"
                class="btn btn-primary btn-xs"
                :disabled="loading.clients[client.id] || previews[client.id]!.access_state === 'malformed'"
              >
                <span v-if="loading.clients[client.id]" class="loading loading-spinner loading-xs"></span>
                <span v-else>Connect</span>
              </button>
              <button
                :data-test="`connect-preview-cancel-${client.id}`"
                @click="cancelPreview(client.id)"
                class="btn btn-ghost btn-xs"
                :disabled="loading.clients[client.id]"
              >
                Cancel
              </button>
            </div>
          </div>
          <!-- Spec 078 US1: preview fetch failed (non-denial). Denials fall
               through to the denied banner above via checkAccess. -->
          <div
            v-else-if="previewError[client.id]"
            :data-test="`connect-preview-error-${client.id}`"
            class="border-t border-base-300 px-3 py-2 text-xs text-error"
          >
            {{ previewError[client.id] }}
          </div>
        </div>

        <div v-if="clients.length === 0 && !loading.initial" class="text-center py-6 opacity-60">
          <p class="text-sm">No AI clients detected on this system.</p>
        </div>
      </div>

      <!-- Result message -->
      <div v-if="resultMessage" class="mt-3">
        <div class="alert alert-sm" :class="resultSuccess ? 'alert-success' : 'alert-error'">
          <span class="text-sm">{{ resultMessage }}</span>
        </div>
        <!-- Spec 078 US2 / FR-006: surface the timestamped backup after a
             successful connect/disconnect; the "no prior file" case is stated
             explicitly rather than showing a blank path. -->
        <div
          v-if="resultSuccess && resultBackupPath"
          data-test="connect-backup-path"
          class="mt-2 flex items-start justify-between gap-2 rounded-lg bg-base-200 px-3 py-2"
        >
          <span class="text-xs leading-relaxed min-w-0">
            A backup of your previous config was saved to
            <code class="font-mono text-[11px] break-all" :title="resultBackupPath">{{ resultBackupPath }}</code>
          </span>
          <button
            data-test="connect-copy-backup"
            @click="copyBackupPath"
            class="btn btn-ghost btn-xs shrink-0"
            title="Copy the backup path to the clipboard"
          >
            {{ copiedBackup ? 'Copied ✓' : 'Copy path' }}
          </button>
        </div>
        <div
          v-else-if="resultSuccess && resultBackupPath === null"
          data-test="connect-no-backup"
          class="mt-2 rounded-lg bg-base-200 px-3 py-2 text-xs opacity-70"
        >
          No prior config file existed, so no backup was needed.
        </div>
        <!-- Spec 078 US3: one-click undo of the connect just performed (session-
             scoped). Clicking Undo first shows the change to be reverted
             (FR-009); the restore only runs on the panel's confirm. -->
        <div
          v-if="resultSuccess && lastConnect && !undoPanelOpen"
          data-test="connect-undo-offer"
          class="mt-2 flex items-center justify-between gap-2 rounded-lg bg-base-200 px-3 py-2"
        >
          <span class="text-xs opacity-70">Changed your mind? You can revert this connect.</span>
          <button
            data-test="connect-undo"
            @click="undoPanelOpen = true"
            class="btn btn-ghost btn-xs shrink-0 text-error"
            :disabled="undoBusy"
            title="Revert this connect: restore the config to its pre-connect state"
          >
            Undo
          </button>
        </div>
        <div
          v-if="resultSuccess && lastConnect && undoPanelOpen"
          data-test="connect-undo-panel"
          class="mt-2 rounded-lg bg-base-200/60 border border-error/40 px-3 py-2 space-y-2"
        >
          <p class="text-xs opacity-70 leading-relaxed">
            <template v-if="lastConnect.backupPath">
              Undo restores
              <code class="font-mono text-[11px] break-all">{{ lastConnect.configPath }}</code>
              to its exact pre-connect state from the backup (a safety copy of the current file is saved first).
            </template>
            <template v-else>
              Undo removes
              <code class="font-mono text-[11px] break-all">{{ lastConnect.configPath }}</code>
              — it did not exist before mcpproxy connected (a safety copy is saved first).
            </template>
          </p>
          <div v-if="lastConnect.preview">
            <div class="text-[11px] font-semibold uppercase tracking-wider text-error/80 mb-1">− will be reverted</div>
            <pre
              data-test="connect-undo-entry"
              class="text-[11px] font-mono whitespace-pre-wrap break-all rounded bg-base-300/60 border-l-2 border-error px-2 py-1.5 leading-relaxed"
            >{{ lastConnect.preview.entry_text }}</pre>
          </div>
          <div class="flex items-center gap-2 pt-0.5">
            <button
              data-test="connect-undo-confirm"
              @click="confirmUndo"
              class="btn btn-error btn-xs"
              :disabled="undoBusy"
            >
              <span v-if="undoBusy" class="loading loading-spinner loading-xs"></span>
              <span v-else>Undo connect</span>
            </button>
            <button
              data-test="connect-undo-cancel"
              @click="undoPanelOpen = false"
              class="btn btn-ghost btn-xs"
              :disabled="undoBusy"
            >
              Keep
            </button>
          </div>
        </div>
        <!-- Spec 078 US2 / SC-005: Connect All renders EVERY successful
             client's backup outcome — one line per client, each with its own
             copy affordance — instead of only the last connect's path. -->
        <div v-if="bulkBackups.length > 0" data-test="connect-bulk-backups" class="mt-2 space-y-1">
          <div
            v-for="b in bulkBackups"
            :key="b.id"
            :data-test="`connect-bulk-backup-${b.id}`"
            class="flex items-start justify-between gap-2 rounded-lg bg-base-200 px-3 py-2"
          >
            <span class="text-xs leading-relaxed min-w-0">
              <span class="font-medium">{{ b.name }}:</span>
              <template v-if="b.backupPath">
                a backup of your previous config was saved to
                <code class="font-mono text-[11px] break-all" :title="b.backupPath">{{ b.backupPath }}</code>
              </template>
              <template v-else>
                No prior config file existed, so no backup was needed.
              </template>
            </span>
            <button
              v-if="b.backupPath"
              :data-test="`connect-bulk-copy-${b.id}`"
              @click="copyBulkBackupPath(b)"
              class="btn btn-ghost btn-xs shrink-0"
              title="Copy this backup path to the clipboard"
            >
              {{ copiedBulkClient === b.id ? 'Copied ✓' : 'Copy path' }}
            </button>
          </div>
        </div>
      </div>

      <div class="modal-action">
        <button
          @click="connectAll"
          class="btn btn-primary btn-sm"
          :disabled="allConnected || connectableClients.length === 0"
        >
          Connect All
        </button>
        <button @click="close" class="btn btn-ghost btn-sm">Close</button>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop" @click.prevent="close"><button>close</button></form>
  </dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import api from '@/services/api'
import { useSystemStore } from '@/stores/system'
import { useOnboardingStore } from '@/stores/onboarding'
import type { ClientStatus, AccessState, ConnectPreview } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const systemStore = useSystemStore()
const onboarding = useOnboardingStore()

const clients = ref<ClientStatus[]>([])
const error = ref<string | null>(null)
const resultMessage = ref('')
const resultSuccess = ref(false)
// Spec 078 US2: backup path of the last successful connect/disconnect.
// string = timestamped backup created; null = success but no prior file to
// back up; undefined = no successful operation to report on.
const resultBackupPath = ref<string | null | undefined>(undefined)
const copiedBackup = ref(false)
// Spec 078 US2 / SC-005: per-client backup outcomes for Connect All. Every
// successful connect in the bulk run keeps its own entry (string = backup
// created; null = no prior file), so no client's backup path is overwritten
// by the next one. Empty when the last operation was a single connect.
const bulkBackups = ref<Array<{ id: string; name: string; backupPath: string | null }>>([])
const copiedBulkClient = ref<string | null>(null)
const loading = reactive({
  initial: false,
  clients: {} as Record<string, boolean>,
})
// Spec 075: per-client GET status results keyed by id. The stat-only listing
// reports access_state='unknown'; an explicit "Check access" (or a failed
// connect/disconnect) reads one config on demand and resolves it here. Because
// this GET actually read the file, it is authoritative over the listing.
const resolved = ref<Record<string, ClientStatus>>({})
const checking = reactive<Record<string, boolean>>({})
const copiedClient = ref<string | null>(null)
// Spec 078 US1: a fetched preview per client (the exact change a connect would
// make, no write yet). Present => the confirm/cancel panel is shown for that
// client. previewError holds a non-denial fetch failure (a denial resolves to
// the access-state banner instead).
const previews = ref<Record<string, ConnectPreview>>({})
const previewLoading = reactive<Record<string, boolean>>({})
const previewError = ref<Record<string, string>>({})
// Spec 078 US3: the connect performed last in THIS modal session, so a one-
// click Undo can revert it. preview is the confirmed preview snapshot (what
// was written), shown again in the undo panel before reverting (FR-009).
// null = no undoable connect (none yet, undone, disconnected, or bulk run).
const lastConnect = ref<{
  id: string
  serverName: string
  configPath: string
  backupPath: string | null
  preview?: ConnectPreview
} | null>(null)
const undoPanelOpen = ref(false)
const undoBusy = ref(false)

// MCP-2952: `GET /api/v1/connect` is stat-only (#706/MCP-2829) and reports
// connected=false for every client. Merge the content-resolved
// connected_client_ids (fetched on open via onboarding.fetchState) so already
// connected clients render Disconnect instead of a fresh Connect button.
// Derived (not mutated) so refreshes stay correct.
const mergedClients = computed<ClientStatus[]>(() => {
  const connectedIds = new Set(onboarding.connectedClientIds)
  return clients.value.map(c => {
    // A per-client GET (Check access / post-action resolve) read the config and
    // is authoritative — it supersedes the stat-only listing for this client.
    const override = resolved.value[c.id]
    if (override) {
      return { ...c, ...override }
    }
    return c.connected || !connectedIds.has(c.id) ? c : { ...c, connected: true }
  })
})

// Default to 'unknown' for the content-read-free listing (no eager read).
function accessState(client: ClientStatus): AccessState {
  return client.access_state ?? 'unknown'
}

const connectableClients = computed(() =>
  // Bridge clients (e.g. Claude Desktop) can be connected even without an
  // existing config file — Connect creates it. A client macOS has blocked
  // ('denied') is excluded: the write would fail with the same privacy error.
  mergedClients.value.filter(
    c => c.supported && (c.exists || c.bridge) && !c.connected && accessState(c) !== 'denied'
  )
)

const allConnected = computed(() =>
  connectableClients.value.length === 0
)

function clientIcon(client: ClientStatus): string {
  // Map client icons based on id/name
  const iconMap: Record<string, string> = {
    'claude-desktop': '\u2728',
    'claude-code': '\u{1F4BB}',
    'cursor': '\u{1F4DD}',
    'vscode': '\u{1F4D0}',
    'windsurf': '\u{1F3C4}',
    'opencode': '\u26A1',
    'gemini': '\u264A',
    'codex': '\u2318',
    'zed': '\u26A1',
    'cline': '\u{1F916}',
    'continue': '\u27A1\uFE0F',
  }
  return iconMap[client.id] || client.icon || '\u{1F527}'
}

async function fetchClients() {
  loading.initial = true
  error.value = null

  try {
    const response = await api.getConnectStatus()
    if (response.success && response.data) {
      clients.value = Array.isArray(response.data) ? response.data : []
      // A fresh stat-only listing supersedes any earlier on-demand resolutions.
      resolved.value = {}
    } else {
      error.value = response.error || 'Failed to load client status'
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to connect to API'
  } finally {
    loading.initial = false
  }
}

// Spec 078 US1: clicking the row's Connect fetches the preview first and shows
// the confirm/cancel panel — no file is written until the user confirms. A
// denied read (403) is surfaced as the access-state banner via checkAccess.
async function startConnect(clientId: string) {
  previewLoading[clientId] = true
  previewError.value = { ...previewError.value, [clientId]: '' }
  try {
    const response = await api.getConnectPreview(clientId)
    if (response.success && response.data) {
      previews.value = { ...previews.value, [clientId]: response.data }
    } else {
      // The preview read may have been blocked by macOS App-Data — resolve the
      // access state so a denial renders the existing remediation banner.
      previewError.value = { ...previewError.value, [clientId]: response.error || 'Failed to load preview' }
      void checkAccess(clientId)
    }
  } catch (err) {
    previewError.value = { ...previewError.value, [clientId]: err instanceof Error ? err.message : 'Failed to load preview' }
    void checkAccess(clientId)
  } finally {
    previewLoading[clientId] = false
  }
}

// Cancel dismisses the preview WITHOUT writing anything (Spec 078 US1).
function cancelPreview(clientId: string) {
  const next = { ...previews.value }
  delete next[clientId]
  previews.value = next
}

function clearPreview(clientId: string) {
  cancelPreview(clientId)
  const nextErr = { ...previewError.value }
  delete nextErr[clientId]
  previewError.value = nextErr
}

// Confirm proceeds with the connect. If an entry already exists, confirming
// implies force=true (the user saw the overwrite warning in the preview).
async function confirmConnect(clientId: string) {
  const preview = previews.value[clientId]
  const force = preview?.entry_exists === true
  bulkBackups.value = []
  copiedBulkClient.value = null
  const outcome = await connect(clientId, force)
  // Spec 078 US3: a successful single connect becomes undoable in this session.
  if (outcome.ok) {
    lastConnect.value = {
      id: clientId,
      serverName: 'mcpproxy',
      configPath: outcome.configPath,
      backupPath: outcome.backupPath,
      preview,
    }
    undoPanelOpen.value = false
  }
  clearPreview(clientId)
}

// Returns the outcome so connectAll can accumulate per-client backup results
// (ok=true with backupPath string = backup created; null = no prior file).
async function connect(clientId: string, force = false): Promise<{ ok: boolean; backupPath: string | null; configPath: string }> {
  loading.clients[clientId] = true
  resultMessage.value = ''
  resultBackupPath.value = undefined
  copiedBackup.value = false
  lastConnect.value = null
  undoPanelOpen.value = false

  try {
    const response = await api.connectClient(clientId, 'mcpproxy', force)
    if (response.success && response.data) {
      resultMessage.value = response.data.message || `Connected to ${clientId}`
      resultSuccess.value = true
      // Empty/absent backup_path on success means no prior file existed.
      const backupPath = response.data.backup_path || null
      resultBackupPath.value = backupPath
      await fetchClients()
      systemStore.addToast({
        type: 'success',
        title: 'Client Connected',
        message: `MCPProxy registered in ${clientId}`,
      })
      return { ok: true, backupPath, configPath: response.data.config_path }
    }
    resultMessage.value = response.error || 'Failed to connect'
    resultSuccess.value = false
    // The write may have failed because macOS blocked the config. Resolve the
    // access state in-band so a denial surfaces as the actionable banner.
    void checkAccess(clientId)
  } catch (err) {
    resultMessage.value = err instanceof Error ? err.message : 'Unknown error'
    resultSuccess.value = false
    void checkAccess(clientId)
  } finally {
    loading.clients[clientId] = false
  }
  return { ok: false, backupPath: null, configPath: '' }
}

// Spec 078 US3: revert the last connect performed in this modal session. The
// backend refuses (409) when the config changed since the connect — surfaced
// honestly as the error message; it never clobbers later edits.
async function confirmUndo() {
  const target = lastConnect.value
  if (!target) return
  undoBusy.value = true
  try {
    const response = await api.undoConnectClient(target.id, target.serverName, target.backupPath)
    if (response.success && response.data) {
      resultMessage.value = response.data.message || `Reverted the ${target.id} connect`
      resultSuccess.value = true
      resultBackupPath.value = undefined
      lastConnect.value = null
      undoPanelOpen.value = false
      await fetchClients()
      void onboarding.fetchState()
      systemStore.addToast({
        type: 'info',
        title: 'Connect undone',
        message: `${target.id} restored to its pre-connect state`,
      })
    } else {
      resultMessage.value = response.error || 'Failed to undo the connect'
      resultSuccess.value = false
      undoPanelOpen.value = false
    }
  } catch (err) {
    resultMessage.value = err instanceof Error ? err.message : 'Unknown error'
    resultSuccess.value = false
    undoPanelOpen.value = false
  } finally {
    undoBusy.value = false
  }
}

async function disconnect(clientId: string) {
  loading.clients[clientId] = true
  resultMessage.value = ''
  resultBackupPath.value = undefined
  copiedBackup.value = false
  bulkBackups.value = []
  copiedBulkClient.value = null
  // A disconnect supersedes any pending session undo (the entry is gone).
  lastConnect.value = null
  undoPanelOpen.value = false

  try {
    const client = clients.value.find(c => c.id === clientId)
    const response = await api.disconnectClient(clientId, client?.server_name || 'mcpproxy')
    if (response.success && response.data) {
      resultMessage.value = response.data.message || `Disconnected from ${clientId}`
      resultSuccess.value = true
      resultBackupPath.value = response.data.backup_path || null
      await fetchClients()
      systemStore.addToast({
        type: 'info',
        title: 'Client Disconnected',
        message: `MCPProxy removed from ${clientId}`,
      })
    } else {
      resultMessage.value = response.error || 'Failed to disconnect'
      resultSuccess.value = false
      void checkAccess(clientId)
    }
  } catch (err) {
    resultMessage.value = err instanceof Error ? err.message : 'Unknown error'
    resultSuccess.value = false
    void checkAccess(clientId)
  } finally {
    loading.clients[clientId] = false
  }
}

// Spec 075 US1/US2: read one client's config on demand to resolve its
// access_state (accessible/absent/denied/malformed) and remediation. This is
// the only Connect call that opens a config file, so it is the sole legitimate
// macOS App-Data privacy-prompt site — always tied to an explicit user action.
async function checkAccess(clientId: string) {
  checking[clientId] = true
  try {
    const response = await api.getConnectClientStatus(clientId)
    if (response.success && response.data) {
      resolved.value = { ...resolved.value, [clientId]: response.data }
    }
  } catch {
    // Best-effort: leave the client's state as-is (unknown) on failure.
  } finally {
    checking[clientId] = false
  }
}

// Fallback remediation if the backend omitted the text (defensive; the core
// always populates `remediation` on a denial). Mirrors connect/access.go.
function defaultRemediation(client: ClientStatus): string {
  return (
    `macOS blocked mcpproxy from reading ${client.name}'s configuration (Privacy & Security ▸ App Data).\n` +
    'Fix: System Settings ▸ Privacy & Security ▸ App Data ▸ enable mcpproxy,\n' +
    'or run: tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy\n' +
    '(dev builds: com.smartmcpproxy.mcpproxy.dev)'
  )
}

// Extract the exact `tccutil reset …` command from the remediation text so the
// user can paste it directly into a terminal.
function tccutilCommand(client: ClientStatus): string {
  const text = client.remediation || defaultRemediation(client)
  const line = text.split('\n').find(l => l.includes('tccutil reset'))
  // Strip a leading "or run: " prefix if present.
  return (line ?? 'tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy')
    .replace(/^.*?(tccutil reset)/, '$1')
    .trim()
}

async function copyTccutil(client: ClientStatus) {
  const cmd = tccutilCommand(client)
  try {
    await navigator.clipboard.writeText(cmd)
    copiedClient.value = client.id
    setTimeout(() => {
      if (copiedClient.value === client.id) copiedClient.value = null
    }, 2000)
  } catch {
    // Clipboard unavailable (e.g. insecure context): surface the command so the
    // user can copy it manually.
    resultMessage.value = cmd
    resultSuccess.value = false
  }
}

// Spec 078 US2: one-click copy of the backup path (same clipboard pattern as
// copyTccutil above).
async function copyBackupPath() {
  if (!resultBackupPath.value) return
  try {
    await navigator.clipboard.writeText(resultBackupPath.value)
    copiedBackup.value = true
    setTimeout(() => {
      copiedBackup.value = false
    }, 2000)
  } catch {
    // Clipboard unavailable (e.g. insecure context): the full path is already
    // rendered, so the user can select and copy it manually.
  }
}

// Spec 078 US2 / SC-005: Connect All accumulates every successful client's
// backup outcome instead of letting each connect() overwrite the previous
// client's backup path.
async function connectAll() {
  bulkBackups.value = []
  copiedBulkClient.value = null
  // Snapshot: connect() refetches the client list mid-loop, which mutates the
  // connectableClients computed while we iterate it.
  const targets = [...connectableClients.value]
  const collected: Array<{ id: string; name: string; backupPath: string | null }> = []
  for (const client of targets) {
    const outcome = await connect(client.id)
    if (outcome.ok) {
      collected.push({ id: client.id, name: client.name, backupPath: outcome.backupPath })
    }
  }
  if (collected.length > 0) {
    bulkBackups.value = collected
    // The per-client list is authoritative for a bulk run; suppress the
    // single-result line that would otherwise repeat only the last backup.
    resultBackupPath.value = undefined
  }
}

// Per-row copy for the Connect All backup list.
async function copyBulkBackupPath(entry: { id: string; backupPath: string | null }) {
  if (!entry.backupPath) return
  try {
    await navigator.clipboard.writeText(entry.backupPath)
    copiedBulkClient.value = entry.id
    setTimeout(() => {
      if (copiedBulkClient.value === entry.id) copiedBulkClient.value = null
    }, 2000)
  } catch {
    // Clipboard unavailable (e.g. insecure context): the full path is already
    // rendered, so the user can select and copy it manually.
  }
}

function close() {
  resultMessage.value = ''
  resultBackupPath.value = undefined
  copiedBackup.value = false
  bulkBackups.value = []
  copiedBulkClient.value = null
  previews.value = {}
  previewError.value = {}
  lastConnect.value = null
  undoPanelOpen.value = false
  emit('close')
}

// Fetch client list when modal opens. Also refresh the onboarding state so
// connected_client_ids is current — this is the wizard-scoped, #706-safe path
// that already content-resolves connections (MCP-2952).
watch(() => props.show, (newVal) => {
  if (newVal) {
    fetchClients()
    void onboarding.fetchState()
    resultMessage.value = ''
    resultBackupPath.value = undefined
    copiedBackup.value = false
    bulkBackups.value = []
    copiedBulkClient.value = null
    previews.value = {}
    previewError.value = {}
    lastConnect.value = null
    undoPanelOpen.value = false
  }
})
</script>
