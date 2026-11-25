<template>
  <div class="rounded-box border border-base-300 bg-base-100" :data-test="testId">
    <table class="table w-full max-sm:block">
      <thead class="max-sm:sr-only">
        <tr>
          <th scope="col">Client</th><th scope="col">State</th><th scope="col">Reach</th><th scope="col">Last seen</th><th scope="col"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody class="max-sm:block">
        <template v-for="client in clients" :key="client.id">
          <tr
            class="cursor-pointer max-sm:grid max-sm:grid-cols-[minmax(0,1fr)_auto] max-sm:gap-x-3 max-sm:gap-y-1 max-sm:px-3 max-sm:py-3 max-sm:border-b max-sm:border-base-300"
            :class="focusedId === client.id && 'bg-primary/10'"
            :data-test="focusedId === client.id ? 'focused-client-row' : `clients-row-${client.id}`"
            :data-client="client.id"
            @click="onRowClick($event, client.id)"
          >
            <td class="font-medium max-sm:p-0 max-sm:border-0! max-sm:col-start-1 max-sm:row-start-1 break-words" :data-test="`client-name-${client.id}`">{{ client.display_name }}</td>
            <td class="whitespace-nowrap max-sm:p-0 max-sm:border-0! max-sm:col-start-1 max-sm:row-start-2" :data-test="`client-state-${client.id}`">
              <StateTag :tone="stateTone(client)">{{ clientStateLabel(client.state) }}</StateTag>
              <span v-if="client.connection_unverified" class="ml-2 text-sm text-base-content/70" :data-test="`client-unverified-${client.id}`">unverified</span>
            </td>
            <td class="max-sm:p-0 max-sm:border-0! max-sm:col-start-1 max-sm:row-start-3 text-sm" :data-test="`client-reach-${client.id}`">
              <template v-if="reachOf(client).kind === 'unknown'">
                <span aria-hidden="true">—</span><span class="sr-only">Reach unknown: no client credential</span>
              </template>
              <template v-else>
                <span :class="reachOf(client).kind === 'missing' && 'text-error font-medium'">{{ reachOf(client).text }}</span>
                <span v-if="reachOf(client).note" class="block text-xs text-base-content/70">{{ reachOf(client).note }}</span>
              </template>
            </td>
            <td class="whitespace-nowrap text-sm max-sm:p-0 max-sm:border-0! max-sm:col-start-1 max-sm:row-start-4" :data-test="`client-last-seen-${client.id}`">
              <span class="sm:hidden text-base-content/70">Last seen </span>{{ relative(client.last_seen) }}
            </td>
            <td class="text-right max-sm:p-0 max-sm:border-0! max-sm:col-start-2 max-sm:row-start-1 max-sm:row-span-4 max-sm:self-center">
              <button
                type="button"
                class="btn btn-sm btn-outline max-sm:min-h-11"
                :data-test="`client-manage-${client.id}`"
                :aria-label="`Manage ${client.display_name}`"
                aria-haspopup="dialog"
                @click="emit('manage', client.id)"
              >Manage</button>
            </td>
          </tr>
          <tr v-if="errorFor(client.id)" :data-test="`client-row-error-${client.id}`">
            <td colspan="5" class="bg-base-200/40">
              <GuardRefusal v-if="isGuardRefusal(errorFor(client.id))" :refusal="errorFor(client.id) as any" @navigate="emit('clear-error', client.id)" />
              <div v-else role="alert" class="alert alert-error text-sm">{{ describeError(errorFor(client.id)) }}</div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import StateTag from '@/components/StateTag.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import { useProfilesStore } from '@/stores/profiles'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import { clientReach, clientStateLabel } from '@/utils/clientReach'
import type { StatusTone } from '@/utils/healthTone'
import type { ClientPresence } from '@/types/api'

// Spec 134 FR-060: one row per client with name, state, reach, last seen and one
// button. Everything else is in the Manage panel.
const props = defineProps<{
  clients: ClientPresence[]
  focusedId?: string
  /** Row errors to show under a row (the open panel shows its own client's). */
  errors?: Record<string, unknown>
  hideErrorFor?: string
  testId?: string
}>()
const emit = defineEmits<{ (e: 'manage', id: string): void; (e: 'clear-error', id: string): void }>()
const profiles = useProfilesStore()

// The row opens the panel too, as the old row toggled its details; buttons and links keep their own action.
function onRowClick(event: MouseEvent, id: string) {
  if ((event.target as HTMLElement).closest('button, a, input, select, summary')) return
  emit('manage', id)
}
function reachOf(client: ClientPresence) { return clientReach(client, name => profiles.byName.get(name) as never) }
function errorFor(id: string): unknown { return id === props.hideErrorFor ? undefined : props.errors?.[id] }
function relative(value?: string | null) { return value ? new Date(value).toLocaleString() : 'Never' }
function stateTone(client: ClientPresence): StatusTone {
  switch (client.state) {
    case 'connected_seen': return 'ok'
    case 'connected_never_seen': return 'review'
    case 'installed': return 'busy'
    default: return 'off'
  }
}
</script>
