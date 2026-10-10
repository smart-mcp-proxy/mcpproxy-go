<template>
  <div class="space-y-1 min-w-0 w-full max-w-[15rem]" :data-test="`client-profile-cell-${client.id}`" @click.stop>
    <!-- No client credential to bind (none, admin key, revoked, expired,
         unknown): the chip and the lock switch are disabled and never call PUT.
         The call to action depends on why (Spec 108-i I8). -->
    <template v-if="noRecord">
      <span class="opacity-70" :data-test="`client-profile-none-${client.id}`">&mdash;</span>
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
        class="btn btn-xs btn-primary h-auto min-h-6 py-1 whitespace-normal"
        :data-test="`client-credential-cta-${client.id}`"
        @click="openConnect"
      >{{ credentialCta(client.credential_state) }}</button>
    </template>
    <template v-else>
      <div class="flex items-center gap-2 flex-wrap min-w-0">
        <button
          ref="chipEl"
          type="button"
          class="btn btn-xs btn-outline max-w-full min-w-0 h-auto min-h-6 py-1 text-left normal-case"
          :class="missing && 'btn-error'"
          aria-haspopup="menu"
          :aria-expanded="open ? 'true' : 'false'"
          :disabled="busy"
          :data-test="`client-profile-chip-${client.id}`"
          @click="toggleMenu"
          @keydown="onChipKeydown"
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
          @click="openMenu(true)"
        >Move&hellip;</button>
      </div>
      <div
        v-if="open"
        ref="menuEl"
        role="menu"
        :aria-label="`Profile for ${client.display_name}`"
        class="rounded-box border border-base-300 bg-base-100 p-1 space-y-0.5 text-sm"
        :data-test="`client-profile-menu-${client.id}`"
        @keydown="onMenuKeydown"
      >
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          role="menuitemradio"
          :aria-checked="option.value === (client.profile ?? '') ? 'true' : 'false'"
          :tabindex="-1"
          class="w-full text-left px-2 py-1 rounded hover:bg-base-200 flex flex-col"
          :class="option.value === (client.profile ?? '') && 'bg-base-200 font-medium'"
          :data-test="`client-profile-option-${client.id}-${option.value || 'all'}`"
          @click="select(option.value)"
        >
          <span>{{ option.title }}</span>
          <span v-if="option.slug" class="text-xs opacity-70">{{ option.slug }}</span>
        </button>
      </div>
      <div class="flex items-center gap-2" :title="client.profile ? '' : 'Choose a profile to lock'">
        <input
          ref="lockEl"
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
        <span class="text-xs">{{ modeLabel(locked ? 'locked' : 'switchable') }}</span>
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
import { computed, nextTick, ref, watch } from 'vue'
import { useProfilesStore } from '@/stores/profiles'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { CONNECT_CLIENT_EVENT } from '@/navigation/navModel'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import { credentialCta, credentialLabel, modeLabel, tierPhrase } from '@/utils/profiles'
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
const chipEl = ref<HTMLButtonElement | null>(null)
const lockEl = ref<HTMLInputElement | null>(null)
const menuEl = ref<HTMLElement | null>(null)
// `?move=1` (the explainer's "Move client" fix) lands here with the menu open.
watch(() => props.autoOpen, value => { if (value) open.value = true }, { immediate: true })

const busy = computed(() => bindings.busy[props.client.id] === true)
// A row with nothing to bind: an observed "other" client, a custom client whose
// record is gone, or a supported client that is neither installed nor connected
// and holds no credential (there is nothing to upgrade, so no call to action).
const noRecord = computed(() => {
  const c = props.client
  if (c.kind === 'other') return true
  const credentialless = !c.credential_state || c.credential_state === 'none' || c.credential_state === 'unknown'
  if (c.kind === 'custom') return c.credential_state === 'none'
  return credentialless && !c.installed && !c.connected
})
const bindable = computed(() => props.client.credential_state === 'client')
const locked = computed(() => props.client.profile_mode === 'locked' || props.client.profile_source === 'pin')
const missing = computed(() => props.client.profile_missing === true)

const label = computed(() => {
  const c = props.client
  if (!c.profile) return 'All servers'
  if (missing.value) return `${c.profile} (missing — deny-all)`
  const profile = profiles.byName.get(c.profile)
  const parts = [c.profile_title || profile?.title || c.profile]
  const tier = tierPhrase(profile?.max_tier, profile?.tool_counts)
  if (tier) parts.push(tier)
  parts.push(locked.value ? 'locked by credential' : 'switchable')
  return parts.join(' · ')
})

const options = computed(() => [
  { value: '', title: 'All servers', slug: '' },
  ...profiles.profiles.map(profile => ({ value: profile.name, title: profile.title || profile.name, slug: profile.title ? profile.name : '' })),
])

// The chip and the switch are disabled while a PUT is in flight, and a disabled
// control drops keyboard focus to <body>. Put focus back where it was once the
// row is enabled again, so a second Space or Enter keeps working.
async function restoreFocus(target: () => HTMLElement | null) {
  await nextTick()
  if (document.activeElement && document.activeElement !== document.body) return
  target()?.focus()
}

async function select(profile: string) {
  open.value = false
  if (profile === (props.client.profile ?? '')) {
    void restoreFocus(() => chipEl.value)
    return
  }
  await bindings.setBinding(props.client.id, { profile })
  await restoreFocus(() => chipEl.value)
}

async function toggleLock(on: boolean) {
  if (!props.client.profile) return
  await bindings.setBinding(props.client.id, { profile: props.client.profile, mode: on ? 'locked' : 'switchable' })
  await restoreFocus(() => lockEl.value)
}

function items(): HTMLElement[] {
  return menuEl.value ? Array.from(menuEl.value.querySelectorAll<HTMLElement>('[role="menuitemradio"]')) : []
}

// Open the menu; a keyboard open (or an explicit request) moves focus into it
// on the current choice, so ArrowUp/ArrowDown and Escape work from there.
async function openMenu(focusItem: boolean) {
  open.value = true
  if (!focusItem) return
  await nextTick()
  const all = items()
  ;(all.find(item => item.getAttribute('aria-checked') === 'true') ?? all[0])?.focus()
}

function closeMenu(returnFocus: boolean) {
  open.value = false
  if (returnFocus) void nextTick(() => chipEl.value?.focus())
}

function toggleMenu(event: MouseEvent) {
  if (open.value) closeMenu(false)
  // detail 0 is a keyboard activation (Enter or Space).
  else void openMenu(event.detail === 0)
}

function onChipKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    closeMenu(true)
  } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    void openMenu(true)
  }
}

function onMenuKeydown(event: KeyboardEvent) {
  const all = items()
  const index = all.indexOf(document.activeElement as HTMLElement)
  const focusAt = (next: number) => { event.preventDefault(); all[(next + all.length) % all.length]?.focus() }
  if (event.key === 'Escape') {
    event.preventDefault()
    closeMenu(true)
  } else if (event.key === 'ArrowDown') focusAt(index + 1)
  else if (event.key === 'ArrowUp') focusAt(index < 0 ? all.length - 1 : index - 1)
  else if (event.key === 'Home') focusAt(0)
  else if (event.key === 'End') focusAt(all.length - 1)
  else if (event.key === 'Tab') {
    // Leave the menu closed and let Tab carry on from the chip.
    chipEl.value?.focus()
    open.value = false
  }
}

function openConnect() {
  window.dispatchEvent(new CustomEvent(CONNECT_CLIENT_EVENT, { detail: { client: props.client.id } }))
}
</script>
