<template>
  <div class="space-y-1 min-w-[10rem]" :data-test="`client-profile-cell-${client.id}`" @click.stop>
    <!-- No client credential to bind (none, admin key, revoked, expired,
         unknown): the chip and the lock switch are disabled and never call PUT.
         The call to action depends on why (Spec 108-i I8). -->
    <template v-if="noRecord">
      <span class="opacity-60" :data-test="`client-profile-none-${client.id}`">&mdash;</span>
    </template>
    <template v-else-if="!bindable">
      <button
        type="button"
        class="btn btn-xs btn-outline"
        disabled
        :data-test="`client-profile-chip-${client.id}`"
        aria-haspopup="menu"
      >All servers</button>
      <div class="flex items-center gap-2">
        <input
          type="checkbox"
          role="switch"
          class="toggle toggle-xs"
          disabled
          :aria-label="`Lock ${client.display_name} to this profile`"
          :aria-checked="false"
          :data-test="`client-lock-switch-${client.id}`"
        />
        <span class="text-xs opacity-70">Locked</span>
      </div>
      <button
        type="button"
        class="btn btn-xs btn-primary"
        :data-test="`client-credential-cta-${client.id}`"
        @click="openConnect"
      >{{ credentialCta(client.credential_state) }}</button>
    </template>
    <template v-else>
      <div class="flex items-center gap-2 flex-wrap">
        <button
          type="button"
          class="btn btn-xs btn-outline max-w-[18rem] h-auto min-h-6 py-1 text-left normal-case"
          :class="missing && 'btn-error'"
          aria-haspopup="menu"
          :aria-expanded="open ? 'true' : 'false'"
          :disabled="busy"
          :data-test="`client-profile-chip-${client.id}`"
          @click="open = !open"
        >
          <span v-if="locked" aria-hidden="true">&#128274;</span>
          <span v-if="locked" class="sr-only">Locked</span>
          <span class="whitespace-normal break-words">{{ label }}</span>
        </button>
        <button
          v-if="missing"
          type="button"
          class="btn btn-xs btn-ghost text-error"
          :data-test="`client-profile-move-${client.id}`"
          @click="open = true"
        >Move&hellip;</button>
      </div>
      <div
        v-if="open"
        role="menu"
        :aria-label="`Profile for ${client.display_name}`"
        class="rounded-box border border-base-300 bg-base-100 p-1 space-y-0.5 text-sm"
        :data-test="`client-profile-menu-${client.id}`"
      >
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          role="menuitemradio"
          :aria-checked="option.value === (client.profile ?? '') ? 'true' : 'false'"
          class="w-full text-left px-2 py-1 rounded hover:bg-base-200 flex flex-col"
          :class="option.value === (client.profile ?? '') && 'bg-base-200 font-medium'"
          :data-test="`client-profile-option-${client.id}-${option.value || 'all'}`"
          @click="select(option.value)"
        >
          <span>{{ option.title }}</span>
          <span v-if="option.slug" class="text-xs opacity-60">{{ option.slug }}</span>
        </button>
      </div>
      <div class="flex items-center gap-2" :title="client.profile ? '' : 'Choose a profile to lock'">
        <input
          type="checkbox"
          role="switch"
          class="toggle toggle-xs"
          :checked="locked"
          :disabled="!client.profile || busy"
          :aria-label="`Lock ${client.display_name} to this profile`"
          :aria-checked="locked ? 'true' : 'false'"
          :data-test="`client-lock-switch-${client.id}`"
          @change="toggleLock(($event.target as HTMLInputElement).checked)"
        />
        <span class="text-xs">{{ locked ? 'Locked' : 'Switchable' }}</span>
      </div>
    </template>

    <div v-if="!noRecord" class="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
      <span class="badge badge-xs badge-outline" :data-test="`client-credential-badge-${client.id}`">{{ credentialLabel(client.credential_state) }}</span>
      <router-link
        v-if="(client.blocked_24h ?? 0) > 0 && clientScopeAvailable"
        class="link"
        :data-test="`client-blocked-link-${client.id}`"
        :to="scope.linkTo('activity', { client: client.id, status: 'blocked' })"
      >{{ client.blocked_24h }} blocked (24h)</router-link>
      <span v-else-if="(client.blocked_24h ?? 0) > 0">{{ client.blocked_24h }} blocked (24h)</span>
      <span v-if="client.rotation_pending" class="badge badge-xs badge-info" :data-test="`client-rotation-badge-${client.id}`">Rotation pending</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useProfilesStore } from '@/stores/profiles'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { CONNECT_CLIENT_EVENT } from '@/navigation/navModel'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import { credentialCta, credentialLabel, tierPhrase } from '@/utils/profiles'
import type { ClientPresence } from '@/types/api'

// Spec 108-i I7: the profile chip of a Clients row. A menu button lists All
// servers and the profiles with a Locked switch. Choosing a profile sends PUT
// binding {profile} WITHOUT a mode, which keeps the credential's mode; the
// switch sends {profile, mode}. There is no optimistic update: the row is
// refreshed from the response, and a guard refusal lands in the store for the
// page to show under the row.
const props = defineProps<{ client: ClientPresence; autoOpen?: boolean }>()
const profiles = useProfilesStore()
const bindings = useClientBindingsStore()
const scope = useScopeQuery('clients')
const clientScopeAvailable = computed(() => isScopeParamAvailable('client'))
const open = ref(false)
// `?move=1` (the explainer's "Move client" fix) lands here with the menu open.
watch(() => props.autoOpen, value => { if (value) open.value = true }, { immediate: true })

const busy = computed(() => bindings.busy[props.client.id] === true)
// A client that has no credential record at all (an observed "other" client).
const noRecord = computed(() => props.client.kind === 'other' || (props.client.kind === 'custom' && props.client.credential_state === 'none'))
const bindable = computed(() => props.client.credential_state === 'client')
const locked = computed(() => props.client.profile_mode === 'locked' || props.client.profile_source === 'pin')
const missing = computed(() => props.client.profile_missing === true)

const label = computed(() => {
  const c = props.client
  if (!c.profile) return 'All servers'
  if (missing.value) return `${c.profile} (missing — deny-all)`
  const profile = profiles.byName.get(c.profile)
  const parts = [c.profile_title || profile?.title || c.profile]
  const tier = tierPhrase(profile?.max_tier)
  if (tier) parts.push(tier)
  parts.push(locked.value ? 'locked by credential' : 'switchable')
  return parts.join(' · ')
})

const options = computed(() => [
  { value: '', title: 'All servers', slug: '' },
  ...profiles.profiles.map(profile => ({ value: profile.name, title: profile.title || profile.name, slug: profile.title ? profile.name : '' })),
])

async function select(profile: string) {
  open.value = false
  if (profile === (props.client.profile ?? '')) return
  await bindings.setBinding(props.client.id, { profile })
}

async function toggleLock(on: boolean) {
  if (!props.client.profile) return
  await bindings.setBinding(props.client.id, { profile: props.client.profile, mode: on ? 'locked' : 'switchable' })
}

function openConnect() {
  window.dispatchEvent(new CustomEvent(CONNECT_CLIENT_EVENT, { detail: { client: props.client.id } }))
}
</script>
