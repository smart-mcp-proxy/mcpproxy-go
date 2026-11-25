<template>
  <div v-if="visible" class="relative flex flex-wrap items-center gap-x-3 gap-y-1" data-test="viewing-filter" @keydown.esc="closePopover">
    <div class="flex items-center gap-1">
      <button
        type="button"
        class="btn btn-sm btn-outline gap-2 max-w-[22rem] normal-case min-h-10 md:min-h-8"
        :class="hasValue && 'btn-active'"
        aria-haspopup="dialog"
        :aria-expanded="open ? 'true' : 'false'"
        :aria-label="ariaLabel"
        :title="TOOLTIP"
        data-test="viewing-filter-button"
        ref="button"
        @click="open = !open"
      >
        <UiIcon name="eye" class="w-4 h-4 shrink-0" />
        <span class="truncate" data-test="viewing-filter-text">
          View as:
          <template v-if="hasValue">
            <span v-if="profileValue" :class="!profileApplies && 'opacity-50'" :title="!profileApplies ? NOT_APPLIED : undefined" data-test="viewing-profile">{{ profileLabel }}</span>
            <template v-if="profileValue && clientValue"> &middot; </template>
            <span v-if="clientValue" :class="!clientApplies && 'opacity-50'" :title="!clientApplies ? NOT_APPLIED : undefined" data-test="viewing-client">{{ clientLabel }}</span>
          </template>
          <template v-else>everyone</template>
        </span>
      </button>
      <button
        v-if="hasValue"
        type="button"
        class="btn btn-sm btn-ghost btn-square min-h-10 min-w-10 md:min-h-8 md:min-w-8"
        aria-label="Clear view filter"
        data-test="viewing-filter-clear"
        @click="clear"
      ><UiIcon name="x" class="w-4 h-4" /></button>
    </div>
    <!-- Spec 134 FR-016: a narrowed page cannot be mistaken for the full one.
         The removable "Viewing as <name>" chip is the page's scope chip row. -->
    <p v-if="narrowing && visible" class="text-xs text-base-content/70" data-test="view-as-note">
      Viewing as {{ viewingNames }}: this page shows only what it can reach. Header counts stay fleet-wide.
    </p>

    <div
      v-if="open"
      role="dialog"
      aria-label="View as"
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
import UiIcon from '@/components/UiIcon.vue'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import { useSystemStore } from '@/stores/system'
import { isScopeParamAvailable, pageIdForRouteName, scopeParamAppliesToPage, useScopeQuery } from '@/composables/useScopeQuery'

// Spec 134 FR-016 (was Spec 108-i I16 / FR-044, the header's "Viewing" chip):
// the "View as" control in the filter row of Servers, Tools, Activity and Usage. It narrows what a PAGE
// shows; it never changes what any client or agent can access, and it calls no
// binding route. It reads and writes the URL filter contract (Spec 109-k) and
// replaces the v2 "Profile:" switcher, which looked like agent scoping.
const props = defineProps<{ conflict?: boolean }>()
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
const page = computed(() => pageIdForRouteName(route.name, route.query.tab))
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

const profileLabel = computed(() => (profileValue.value === '-' ? 'unattributed' : profiles.titleFor(profileValue.value)))
const clientLabel = computed(() => (clientValue.value === '-' ? 'unattributed' : clients.clients.find(client => client.id === clientValue.value)?.display_name ?? clientValue.value))

// Hidden: before the server lists the filters, for a tenant, and while there is
// nothing to filter by (no profile and no client with a credential). A value
// already in the URL keeps it visible so it can always be cleared.
const visible = computed(() => {
  if (isTenant.value || !isScopeParamAvailable('profile')) return false
  return hasValue.value || profiles.hasProfiles || credentialedClients.value.length > 0
})

const ariaLabel = computed(() => {
  if (!hasValue.value) return 'View as: everyone. Filters what this page shows'
  const raw = [profileValue.value ? profileLabel.value : '', clientValue.value ? clientLabel.value : ''].filter(Boolean).join(' · ')
  return `View as: ${raw}. Filters what this page shows`
})
// Only the filters this page actually applies count as narrowing ("-" is the
// unattributed marker, not a view-as subject).
const narrowingProfile = computed(() => Boolean(profileValue.value && profileValue.value !== '-' && profileApplies.value))
const narrowingClient = computed(() => Boolean(clientValue.value && clientValue.value !== '-' && clientApplies.value))
// A page that cannot apply both at once (Tools: profile + client) shows its own
// conflict state; the note must not claim a narrowing that did not happen.
const narrowing = computed(() => !props.conflict && (narrowingProfile.value || narrowingClient.value))
const viewingNames = computed(() =>
  [narrowingProfile.value ? profileLabel.value : '', narrowingClient.value ? clientLabel.value : ''].filter(Boolean).join(' · '),
)

function setParam(name: 'profile' | 'client', value: string) { scope.set({ [name]: value || undefined }) }
function clear() { scope.clear(['profile', 'client']); open.value = false }

// Escape closes the popover and hands focus back to the chip.
const button = ref<HTMLButtonElement | null>(null)
function closePopover() {
  if (!open.value) return
  open.value = false
  button.value?.focus()
}

// Loads the profile list only when no one has (pages share the profiles store).
// App only mounts the shell once the principal is known; a tenant never reads
// the admin profile list.
function load() {
  if (isTenant.value) return
  if ((!auth.isTeamsEdition || auth.canLoadCore) && !profiles.loaded && !profiles.loading) void profiles.fetchProfiles()
}
onMounted(load)
watch(() => system.authEpoch, () => { if (!isTenant.value && (!auth.isTeamsEdition || auth.canLoadCore)) void profiles.fetchProfiles() })
</script>
