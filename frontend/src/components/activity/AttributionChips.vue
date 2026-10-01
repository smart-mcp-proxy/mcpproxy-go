<template>
  <div :class="stacked ? 'flex flex-col items-start gap-1' : 'flex flex-wrap items-center gap-1'" :data-test="testId ? `activity-attribution-${testId}` : 'attribution-chips'">
    <template v-if="hasAny">
      <router-link
        v-if="clientId"
        :to="scopeQuery.linkTo('clients', { client: clientId })"
        class="badge badge-sm badge-outline max-w-full truncate hover:bg-base-200"
        :title="`Client ${clientId}`"
        data-test="attribution-client"
        @click.stop
      >{{ clientLabel }}</router-link>
      <span
        v-else-if="record.client_name"
        class="badge badge-sm badge-ghost max-w-full truncate"
        title="The client reported this name about itself; it is not a verified identity"
        data-test="attribution-client-reported"
      >~{{ record.client_name }} (reported)</span>

      <router-link
        v-if="record.profile"
        :to="profileEditorLink(record.profile)"
        class="badge badge-sm badge-outline max-w-full truncate hover:bg-base-200"
        :title="`Open profile ${record.profile}`"
        data-test="attribution-profile"
        @click.stop
      >{{ profileLabel }}</router-link>

      <router-link
        v-if="showToken"
        :to="scopeQuery.linkTo('tokens', { token: record.token_name! })"
        class="badge badge-sm badge-outline max-w-full truncate hover:bg-base-200"
        :title="`Token ${record.token_name}`"
        data-test="attribution-token"
        @click.stop
      >{{ record.token_name }}</router-link>
    </template>
    <span v-else class="text-xs text-base-content/60" data-test="attribution-none">{{ emptyLabel }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import { useScopeQuery } from '@/composables/useScopeQuery'
import { profileEditorLink } from '@/utils/profileRoute'
import { profileSourceLabel } from '@/utils/profiles'
import type { ProfileSource } from '@/types/api'

// Spec 108-j J10 (FR-029, US3-1/4; link map "Activity row chips"): who made a
// call and under which profile. The same chips render in an Activity row, the
// detail drawer and a Sessions row (a session has the same four fields).
//
//  - Client: the display name from the clients store, else the id, linking to
//    the Clients page filtered to it. With no client id but a self-reported
//    name, a muted "~name (reported)" that is NOT a link: a client can claim
//    any name, so it is never offered as an identity.
//  - Profile: "<title> · <how it was resolved>", linking to the editor.
//  - Token: the token name, omitted when it is the client's own credential
//    (`client-<id>`): the client chip already says that.
//  - Nothing at all: "unattributed" (a record from before Spec 108).
interface Attributed {
  type?: string
  profile?: string
  profile_source?: ProfileSource | string
  client_id?: string
  client_name?: string
  token_name?: string
}

const props = defineProps<{
  record: Attributed
  testId?: string
  /** One chip per line (a narrow table column); each truncates instead of widening it. */
  stacked?: boolean
}>()

const profiles = useProfilesStore()
const clients = useClientsStore()
const scopeQuery = useScopeQuery('activity')

const clientId = computed(() => props.record.client_id || '')
const clientLabel = computed(() => clients.clients.find(client => client.id === clientId.value)?.display_name ?? clientId.value)

const profileLabel = computed(() => {
  const title = profiles.titleFor(props.record.profile)
  const source = profileSourceLabel(props.record.profile_source)
  return source ? `${title} · ${source}` : title
})

const showToken = computed(() => {
  const name = props.record.token_name
  if (!name) return false
  return !(clientId.value && name === `client-${clientId.value}`)
})

const hasAny = computed(() => Boolean(clientId.value || props.record.client_name || props.record.profile || props.record.token_name))

// A system event (a config change, a scan) is never attributed: a dash says so
// without calling it "unattributed" like a call that lost its attribution.
const CALL_TYPES = new Set(['tool_call', 'internal_tool_call', 'policy_decision', 'prompt_get'])
const emptyLabel = computed(() => (props.record.type && !CALL_TYPES.has(props.record.type) ? '—' : 'unattributed'))
</script>
