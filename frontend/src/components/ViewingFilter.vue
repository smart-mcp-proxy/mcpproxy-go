<template>
  <div v-if="visible" class="relative" data-test="viewing-filter">
    <div class="flex items-center gap-1">
      <button
        type="button"
        class="btn btn-sm btn-outline gap-2 max-w-[22rem] normal-case"
        :class="hasValue && 'btn-primary'"
        aria-haspopup="dialog"
        :aria-expanded="open ? 'true' : 'false'"
        :aria-label="ariaLabel"
        :title="TOOLTIP"
        data-test="viewing-filter-button"
        @click="open = !open"
      >
        <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
        </svg>
        <span class="hidden min-[1100px]:inline truncate" data-test="viewing-filter-text">
          Viewing:
          <template v-if="hasValue">
            <span v-if="profileValue" :class="!profileApplies && 'opacity-50'" :title="!profileApplies ? NOT_APPLIED : undefined" data-test="viewing-profile">{{ profileLabel }}</span>
            <template v-if="profileValue && clientValue"> &middot; </template>
            <span v-if="clientValue" :class="!clientApplies && 'opacity-50'" :title="!clientApplies ? NOT_APPLIED : undefined" data-test="viewing-client">{{ clientLabel }}</span>
          </template>
          <template v-else>all</template>
        </span>
      </button>
      <button
        v-if="hasValue"
        type="button"
        class="btn btn-sm btn-ghost btn-square"
        aria-label="Clear view filter"
        data-test="viewing-filter-clear"
        @click="clear"
      >&times;</button>
    </div>

    <div
      v-if="open"
      role="dialog"
      aria-label="Viewing filter"
      class="absolute left-0 top-full mt-2 p-3 shadow-lg bg-base-100 rounded-box w-80 max-w-[calc(100vw-1.5rem)] border border-base-300 z-[var(--z-dropdown)] space-y-3"
      data-test="viewing-filter-popover"
    >
      <p class="text-xs opacity-70" data-test="viewing-filter-help">{{ TOOLTIP }}</p>
      <div class="form-control">
        <label class="label py-1" for="viewing-profile"><span class="label-text font-medium">Profile</span></label>
        <select id="viewing-profile" class="select select-bordered select-sm w-full" :value="profileValue" data-test="viewing-profile-select" @change="setParam('profile', ($event.target as HTMLSelectElement).value)">
          <option value="">All</option>
          <option v-for="profile in profiles.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}</option>
          <option v-if="profileValue && !profiles.byName.has(profileValue)" :value="profileValue">{{ profileValue }}</option>
        </select>
      </div>
      <div class="form-control">
        <label class="label py-1" for="viewing-client"><span class="label-text font-medium">Client</span></label>
        <select id="viewing-client" class="select select-bordered select-sm w-full" :value="clientValue" data-test="viewing-client-select" @change="setParam('client', ($event.target as HTMLSelectElement).value)">
          <option value="">All</option>
          <option v-for="client in credentialedClients" :key="client.id" :value="client.id">{{ client.display_name }}</option>
          <option v-if="clientValue && !credentialedClients.some(client => client.id === clientValue)" :value="clientValue">{{ clientValue }}</option>
        </select>
      </div>
      <div class="flex justify-end"><button type="button" class="btn btn-ghost btn-xs" :disabled="!hasValue" @click="clear">Clear</button></div>
    </div>
    <div v-if="open" class="fixed inset-0 z-[calc(var(--z-dropdown)-1)]" @click="open = false" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import { useSystemStore } from '@/stores/system'
import { isScopeParamAvailable, pageIdForRouteName, scopeParamAppliesToPage, useScopeQuery } from '@/composables/useScopeQuery'

// Spec 108-i I16 / FR-044: the header's "Viewing" chip. It narrows what a PAGE
// shows; it never changes what any client or agent can access, and it calls no
// binding route. It reads and writes the URL filter contract (Spec 109-k) and
// replaces the v2 "Profile:" switcher, which looked like agent scoping.
const TOOLTIP = 'Filters what this page shows. It does not change what any client or agent can access.'
const NOT_APPLIED = 'Not applied on this page'

const route = useRoute()
const auth = useAuthStore()
const profiles = useProfilesStore()
const clients = useClientsStore()
const system = useSystemStore()
// profile and client are registered (sticky) on the Activity page, so its
// instance carries the URL writes for every page; reads and the "applies here"
// test use the current page's own registration.
const scope = useScopeQuery('activity')
const open = ref(false)

const isTenant = computed(() => auth.principalKind === 'tenant')
const page = computed(() => pageIdForRouteName(route.name))
const credentialedClients = computed(() => clients.clients.filter(client => client.credential_state === 'client'))

function queryValue(name: string): string {
  const raw = route.query[name]
  const value = Array.isArray(raw) ? raw[0] : raw
  return typeof value === 'string' ? value : ''
}
const profileValue = computed(() => (isScopeParamAvailable('profile') ? queryValue('profile') : ''))
const clientValue = computed(() => (isScopeParamAvailable('client') ? queryValue('client') : ''))
const hasValue = computed(() => Boolean(profileValue.value || clientValue.value))
const profileApplies = computed(() => scopeParamAppliesToPage('profile', page.value))
const clientApplies = computed(() => scopeParamAppliesToPage('client', page.value))

const profileLabel = computed(() => profiles.titleFor(profileValue.value))
const clientLabel = computed(() => clients.clients.find(client => client.id === clientValue.value)?.display_name ?? clientValue.value)

// Hidden: before the server lists the filters, for a tenant, and while there is
// nothing to filter by (no profile and no client with a credential). A value
// already in the URL keeps it visible so it can always be cleared.
const visible = computed(() => {
  if (isTenant.value || !isScopeParamAvailable('profile')) return false
  return hasValue.value || profiles.hasProfiles || credentialedClients.value.length > 0
})

const ariaLabel = computed(() => {
  if (!hasValue.value) return 'Viewing: all. Filters what this page shows'
  const parts = [profileValue.value ? profileLabel.value : '', clientValue.value ? clientLabel.value : ''].filter(Boolean)
  return `Viewing: ${parts.join(' · ')}. Filters what this page shows`
})

function setParam(name: 'profile' | 'client', value: string) { scope.set({ [name]: value || undefined }) }
function clear() { scope.clear(['profile', 'client']); open.value = false }

// The header is global, so it loads the list itself (TopHeader no longer does).
// App only mounts the shell once the principal is known; a tenant never reads
// the admin profile list.
function load() {
  if (isTenant.value) return
  if (!auth.isTeamsEdition || auth.canLoadCore) void profiles.fetchProfiles()
}
onMounted(load)
watch(() => system.authEpoch, load)
</script>
