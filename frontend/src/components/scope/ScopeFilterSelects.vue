<template>
  <div v-if="shown.length > 0" class="flex flex-wrap items-end gap-4" data-test="scope-selects">
    <div v-if="shown.includes('profile')" class="form-control min-w-[150px]">
      <label class="label py-1" :for="`${uid}-profile`"><span class="label-text text-xs">Profile</span></label>
      <select
        :id="`${uid}-profile`"
        class="select select-bordered select-sm"
        :value="current('profile')"
        data-test="scope-select-profile"
        @change="pick('profile', $event)"
      >
        <option value="">All profiles</option>
        <option v-if="allowUnattributed" value="-">Unattributed</option>
        <option v-for="profile in profiles.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}</option>
        <option v-if="orphan('profile', profileNames)" :value="current('profile')">{{ current('profile') }}</option>
      </select>
    </div>
    <div v-if="shown.includes('client')" class="form-control min-w-[150px]">
      <label class="label py-1" :for="`${uid}-client`"><span class="label-text text-xs">Client</span></label>
      <select
        :id="`${uid}-client`"
        class="select select-bordered select-sm"
        :value="current('client')"
        data-test="scope-select-client"
        @change="pick('client', $event)"
      >
        <option value="">All clients</option>
        <option v-if="allowUnattributed" value="-">Unattributed</option>
        <option v-for="client in clientOptions" :key="client.id" :value="client.id">{{ client.display_name }}</option>
        <option v-if="orphan('client', clientOptions.map(client => client.id))" :value="current('client')">{{ current('client') }}</option>
      </select>
    </div>
    <div v-if="shown.includes('token')" class="form-control min-w-[150px]">
      <label class="label py-1" :for="`${uid}-token`"><span class="label-text text-xs">Token</span></label>
      <select
        :id="`${uid}-token`"
        class="select select-bordered select-sm"
        :value="current('token')"
        data-test="scope-select-token"
        @change="pick('token', $event)"
      >
        <option value="">All tokens</option>
        <option v-if="allowUnattributed" value="-">Unattributed</option>
        <option v-for="name in tokenNames" :key="name" :value="name">{{ name }}</option>
        <option v-if="orphan('token', tokenNames)" :value="current('token')">{{ current('token') }}</option>
      </select>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import api from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import {
  isScopeParamAvailable,
  scopeParamAppliesToPage,
  type PageId,
  type UseScopeQueryResult,
} from '@/composables/useScopeQuery'

// Spec 108-j J5 (FR-081): the Profile, Client and Token pickers of a page. Each
// renders only when the parameter is registered on the page AND advertised by
// the build, and writes through useScopeQuery, the same URL parameter the
// header Viewing chip writes, so the two cannot disagree. A tenant (non-admin)
// gets no Client or Token picker: both lists are administrator-only.
const props = defineProps<{
  page: PageId
  scopeQuery: UseScopeQueryResult
  /** Activity and Usage accept `-` ("unattributed"); Tools does not. */
  allowUnattributed?: boolean
}>()

type ScopeName = 'profile' | 'client' | 'token'

const uid = `scope-${Math.random().toString(36).slice(2, 8)}`
const auth = useAuthStore()
const profiles = useProfilesStore()
const clients = useClientsStore()
const tokenNames = ref<string[]>([])

const isTenant = computed(() => auth.principalKind === 'tenant')

const shown = computed<ScopeName[]>(() =>
  (['profile', 'client', 'token'] as const).filter(name => {
    if (!isScopeParamAvailable(name) || !scopeParamAppliesToPage(name, props.page)) return false
    if (isTenant.value && name !== 'profile') return false
    return true
  })
)

// Historic records name revoked clients too, so only a client that never had a
// credential is left out of the list.
const clientOptions = computed(() => clients.clients.filter(client => client.credential_state !== 'none'))
const profileNames = computed(() => profiles.profiles.map(profile => profile.name))

function current(name: ScopeName): string {
  return props.scopeQuery.state[name] ?? ''
}

// A value already in the URL but absent from the loaded list (a dangling
// reference, a list still loading) keeps an option so the select never shows
// a value that is not there.
function orphan(name: ScopeName, known: string[]): boolean {
  const value = current(name)
  return value !== '' && value !== '-' && !known.includes(value)
}

function pick(name: ScopeName, event: Event) {
  const value = (event.target as HTMLSelectElement).value
  props.scopeQuery.set({ [name]: value || undefined })
}

onMounted(() => {
  if (!profiles.loaded && !profiles.loading) void profiles.fetchProfiles()
  if (isTenant.value) return
  if (shown.value.includes('client') && clients.clients.length === 0) void clients.refreshPresence()
  if (shown.value.includes('token')) {
    void (async () => {
      try {
        const response = await api.listAgentTokens()
        // Per-client credentials are filtered as clients, not as tokens.
        tokenNames.value = (response.data?.tokens ?? []).filter(token => token.kind !== 'client').map(token => token.name)
      } catch {
        tokenNames.value = []
      }
    })()
  }
})
</script>
