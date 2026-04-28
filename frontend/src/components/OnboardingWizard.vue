<template>
  <dialog :open="show" class="modal modal-bottom sm:modal-middle">
    <div class="modal-box max-w-2xl">
      <!-- Header with step indicator -->
      <div class="flex items-start justify-between mb-3">
        <div>
          <h3 class="font-bold text-lg">Welcome to MCPProxy</h3>
          <p class="text-xs opacity-60 mt-1" v-if="totalSteps > 0">
            Step {{ currentIndex + 1 }} of {{ totalSteps }}
          </p>
          <p class="text-xs opacity-60 mt-1" v-else>You're all set</p>
        </div>
        <button class="btn btn-ghost btn-xs" @click="dismiss" aria-label="Close">✕</button>
      </div>

      <!-- Step progress dots -->
      <div v-if="totalSteps > 1" class="flex items-center gap-2 mb-4">
        <div
          v-for="(s, idx) in visibleSteps"
          :key="s"
          class="flex-1 h-1 rounded-full transition-colors"
          :class="idx <= currentIndex ? 'bg-primary' : 'bg-base-300'"
        ></div>
      </div>

      <!-- All-set state when no steps to show -->
      <div v-if="totalSteps === 0" class="py-6 text-center">
        <div class="text-3xl mb-2">✅</div>
        <p class="text-sm">Both an AI client and an MCP server are already set up. Nothing for the wizard to do here.</p>
      </div>

      <!-- Step: Connect a client -->
      <section v-else-if="currentStep === 'connect'" data-test="step-connect">
        <h4 class="font-semibold mb-1">Connect an AI client</h4>
        <p class="text-sm opacity-70 mb-4">
          Pick at least one AI tool. MCPProxy will register itself in that tool's config so the assistant can talk to mcpproxy. A backup is created before any file is modified.
        </p>

        <!-- Loading -->
        <div v-if="loadingClients" class="flex justify-center py-4">
          <span class="loading loading-spinner loading-md"></span>
        </div>

        <!-- Error -->
        <div v-else-if="clientsError" class="alert alert-error mb-2">
          <span class="text-sm">{{ clientsError }}</span>
        </div>

        <!-- Empty state: no clients detected -->
        <div v-else-if="clients.length === 0" class="text-center py-4 opacity-70">
          <p class="text-sm">No supported AI clients were detected on this machine.</p>
          <p class="text-xs mt-2">Install one of: Claude Code, Cursor, VS Code, Windsurf, Codex CLI, or Gemini CLI, then click Skip below to continue.</p>
        </div>

        <!-- Client list -->
        <div v-else class="space-y-2 max-h-64 overflow-y-auto">
          <div
            v-for="client in clients"
            :key="client.id"
            class="flex items-center justify-between p-2 rounded-lg border border-base-300"
            :data-test="`client-row-${client.id}`"
          >
            <div class="min-w-0 flex-1">
              <div class="font-medium text-sm truncate">{{ client.name }}</div>
              <div class="text-xs opacity-50 truncate" :title="client.config_path">{{ client.config_path }}</div>
            </div>
            <div class="flex-shrink-0 ml-2">
              <span v-if="!client.supported" class="badge badge-ghost badge-sm">{{ client.reason || 'Not supported' }}</span>
              <span v-else-if="!client.exists" class="text-xs opacity-40">Not installed</span>
              <span v-else-if="client.connected" class="badge badge-success badge-sm">Connected</span>
              <button
                v-else
                @click="connectOne(client.id)"
                class="btn btn-primary btn-xs"
                :disabled="busyClients[client.id]"
                :data-test="`connect-${client.id}`"
              >
                <span v-if="busyClients[client.id]" class="loading loading-spinner loading-xs"></span>
                <span v-else>Connect</span>
              </button>
            </div>
          </div>
        </div>

        <div v-if="connectMessage" class="mt-3">
          <div class="alert alert-sm" :class="connectMessageOk ? 'alert-success' : 'alert-error'">
            <span class="text-sm">{{ connectMessage }}</span>
          </div>
        </div>

        <div class="mt-4 flex flex-wrap items-center justify-between gap-2">
          <button
            v-if="connectableClients.length > 1"
            @click="connectAll"
            class="btn btn-secondary btn-sm"
            :disabled="connectableClients.length === 0 || busyAny"
            data-test="connect-all"
          >
            Connect to all ({{ connectableClients.length }})
          </button>
          <span v-else></span>
          <div class="flex gap-2">
            <button class="btn btn-ghost btn-sm" @click="skipCurrent" data-test="skip-step">Skip for now</button>
            <button
              class="btn btn-primary btn-sm"
              :disabled="!atLeastOneConnected"
              @click="advanceFromConnect"
              data-test="next-step"
            >
              Next
            </button>
          </div>
        </div>
      </section>

      <!-- Step: Add a server (with quarantine explainer) -->
      <section v-else-if="currentStep === 'server'" data-test="step-server">
        <h4 class="font-semibold mb-1">Add an MCP server</h4>
        <p class="text-sm opacity-70 mb-3">
          Servers expose tools (like GitHub or Slack) to your AI assistant via mcpproxy. You can add one now, or come back later — your AI client can also drive this conversationally once mcpproxy is connected.
        </p>

        <div class="alert alert-info mb-4 text-sm" data-test="quarantine-explainer">
          <div class="flex flex-col items-start gap-1">
            <div class="font-semibold">Every new server starts in quarantine</div>
            <div class="opacity-80">
              MCPProxy holds the server in a safe holding state until you review and approve it. No AI client can call its tools until you do. This is mcpproxy's safety net against tool-poisoning and rug-pull attacks.
            </div>
          </div>
        </div>

        <div class="flex flex-col gap-2">
          <button class="btn btn-primary btn-sm w-full" @click="openAddServer" data-test="add-server-button">
            Add a server now
          </button>
          <p v-if="serverAddedJustNow" class="text-xs text-success">
            ✓ Server added — it's currently in quarantine. You can review it on the Servers page after this wizard.
          </p>
        </div>

        <div class="mt-6 flex justify-end gap-2">
          <button class="btn btn-ghost btn-sm" @click="skipCurrent" data-test="skip-step">Skip for now</button>
          <button
            class="btn btn-primary btn-sm"
            @click="finishFromServer"
            :disabled="!serverAddedJustNow && !hasConfiguredServer"
            data-test="finish-step"
          >
            Finish
          </button>
        </div>
      </section>

      <!-- Final summary screen (when 0 visible steps but wizard explicitly opened) -->
      <div v-else class="py-6 text-center">
        <div class="text-3xl mb-2">✨</div>
        <p class="text-sm">You're all set up. Closing this wizard.</p>
      </div>

      <!-- Modal-level finish action when no step is rendered -->
      <div v-if="totalSteps === 0" class="modal-action">
        <button class="btn btn-primary btn-sm" @click="dismiss">Close</button>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop" @click.prevent="dismiss"><button>close</button></form>
  </dialog>

  <!-- Embedded AddServerModal for the server step -->
  <AddServerModal
    :show="addServerOpen"
    @close="addServerOpen = false"
    @added="onServerAdded"
  />
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import api from '@/services/api'
import { useOnboardingStore } from '@/stores/onboarding'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import AddServerModal from '@/components/AddServerModal.vue'
import type { ClientStatus } from '@/types'

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

// Local UI state
const clients = ref<ClientStatus[]>([])
const loadingClients = ref(false)
const clientsError = ref<string | null>(null)
const busyClients = reactive<Record<string, boolean>>({})
const connectMessage = ref('')
const connectMessageOk = ref(true)
const currentIndex = ref(0)
const addServerOpen = ref(false)
const serverAddedJustNow = ref(false)

// Derived from the store. Frozen at the moment the wizard opened so steps
// don't disappear under the user mid-flow.
const visibleSteps = computed(() => onboarding.visibleSteps)
const totalSteps = computed(() => visibleSteps.value.length)
const currentStep = computed(() => visibleSteps.value[currentIndex.value] ?? null)

const hasConfiguredServer = computed(() => onboarding.hasConfiguredServer)

const connectableClients = computed(() =>
  clients.value.filter(c => c.supported && c.exists && !c.connected)
)
const atLeastOneConnected = computed(() =>
  clients.value.some(c => c.connected)
)
const busyAny = computed(() => Object.values(busyClients).some(Boolean))

watch(() => props.show, async (open) => {
  if (open) {
    currentIndex.value = 0
    serverAddedJustNow.value = false
    connectMessage.value = ''
    await onboarding.fetchState()
    if (visibleSteps.value[0] === 'connect') {
      await fetchClients()
    }
  }
})

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

async function connectOne(clientId: string) {
  busyClients[clientId] = true
  connectMessage.value = ''
  try {
    const res = await api.connectClient(clientId)
    if (res.success && res.data) {
      connectMessageOk.value = true
      connectMessage.value = res.data.message || `Connected ${clientId}`
      const c = clients.value.find(c => c.id === clientId)
      if (c) c.connected = true
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

async function connectAll() {
  for (const c of connectableClients.value) {
    await connectOne(c.id)
  }
}

async function advanceFromConnect() {
  await onboarding.markConnectCompleted()
  // If the server step is also visible, advance; otherwise finish.
  if (currentIndex.value + 1 < totalSteps.value) {
    currentIndex.value += 1
  } else {
    await finishWizard()
  }
}

async function skipCurrent() {
  if (currentStep.value === 'connect') {
    await onboarding.markConnectSkipped()
  } else if (currentStep.value === 'server') {
    await onboarding.markServerSkipped()
  }
  if (currentIndex.value + 1 < totalSteps.value) {
    currentIndex.value += 1
  } else {
    await finishWizard()
  }
}

function openAddServer() {
  addServerOpen.value = true
}

async function onServerAdded() {
  addServerOpen.value = false
  serverAddedJustNow.value = true
  // Refresh server count and onboarding state.
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

async function finishFromServer() {
  await onboarding.markServerCompleted()
  await finishWizard()
}

async function finishWizard() {
  await onboarding.markEngaged()
  emit('close')
}

async function dismiss() {
  // If user closed without finishing any step, still mark engaged so the
  // wizard does not auto-show on next load. The user can re-open it
  // manually from the Clients section.
  if (!onboarding.isEngaged) {
    await onboarding.markEngaged()
  }
  emit('close')
}
</script>
