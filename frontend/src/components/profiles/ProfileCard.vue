<template>
  <article class="card bg-base-100 border border-base-300" :data-test="`profile-card-${profile.name}`" :aria-labelledby="`profile-card-title-${profile.name}`">
    <div class="card-body p-4 space-y-2">
      <div class="flex items-start justify-between gap-2">
        <div class="min-w-0">
          <h2 :id="`profile-card-title-${profile.name}`" class="card-title text-base truncate">{{ profile.title || profile.name }}</h2>
          <p v-if="profile.title" class="text-xs font-mono opacity-70 truncate">{{ profile.name }}</p>
        </div>
        <span v-if="profile.is_legacy" class="badge badge-ghost badge-sm shrink-0" :data-test="`profile-legacy-${profile.name}`" title="This profile only lists servers; it sets no tier cap or tool rules.">Servers only</span>
      </div>

      <p class="text-sm" data-test="profile-tier">
        <span class="opacity-70">Max tier</span>
        {{ tierPhrase(profile.max_tier) || 'No cap' }}
      </p>
      <dl class="flex flex-wrap gap-x-4 gap-y-1 text-sm" data-test="profile-counts">
        <div class="flex gap-1"><dt class="opacity-70">Read</dt><dd>{{ profile.tool_counts.read }}</dd></div>
        <div class="flex gap-1"><dt class="opacity-70">Write</dt><dd>{{ profile.tool_counts.write }}</dd></div>
        <div class="flex gap-1"><dt class="opacity-70">Destructive</dt><dd>{{ profile.tool_counts.destructive }}</dd></div>
      </dl>
      <p v-if="profile.tool_counts.unannotated_hidden > 0" class="text-xs opacity-70" data-test="profile-unannotated">{{ profile.tool_counts.unannotated_hidden }} unannotated hidden</p>

      <!-- used_by is administrator-only: absent, never empty, for anyone else. -->
      <p v-if="profile.used_by" class="text-sm" data-test="profile-used-by">
        <span class="opacity-70">Used by</span>
        {{ ' ' }}{{ usedByText || 'nothing yet' }}
      </p>

      <p class="text-xs opacity-70" data-test="profile-stats">{{ profile.calls_24h }} calls &middot; {{ profile.blocked_24h }} blocked (24 h)</p>

      <nav v-if="profileScopeAvailable" class="flex flex-wrap gap-x-3 text-sm" :aria-label="`${profile.title || profile.name} links`">
        <router-link class="link" :to="scope.linkTo('tools', { profile: profile.name })" data-test="profile-link-tools">Tools</router-link>
        <router-link class="link" :to="scope.linkTo('activity', { profile: profile.name })" data-test="profile-link-activity">Activity</router-link>
        <router-link v-if="!tenant" class="link" :to="scope.linkTo('clients', { profile: profile.name })" data-test="profile-link-clients">Clients</router-link>
        <router-link v-if="!tenant" class="link" :to="scope.linkTo('tokens', { profile: profile.name })" data-test="profile-link-tokens">Tokens</router-link>
      </nav>

      <div class="card-actions justify-end items-center pt-1">
        <router-link class="btn btn-sm btn-primary" :to="{ name: 'profile-editor', params: { name: profile.name } }" :data-test="`profile-edit-${profile.name}`">{{ tenant ? 'View' : 'Edit' }}</router-link>
        <!-- A plain disclosure (not a hidden daisyUI dropdown): nothing of the
             menu is in the DOM, or measured by the contrast sweep, while it is closed. -->
        <div v-if="!tenant" class="relative" @focusout="onFocusOut" @keydown.esc="menuOpen = false">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            aria-haspopup="menu"
            :aria-expanded="menuOpen ? 'true' : 'false'"
            :aria-label="`More actions for ${profile.title || profile.name}`"
            :data-test="`profile-more-${profile.name}`"
            @click="menuOpen = !menuOpen"
          >&hellip;</button>
          <ul v-if="menuOpen" role="menu" class="absolute right-0 bottom-full mb-1 menu menu-sm bg-base-100 rounded-box border border-base-300 shadow z-[var(--z-dropdown)] w-64 p-1">
            <li role="none"><button type="button" role="menuitem" :data-test="`profile-assign-${profile.name}`" @click="pick('assign')">Assign to client&hellip;</button></li>
            <li role="none"><router-link role="menuitem" :to="{ path: '/clients', query: { tab: 'tokens', create: '1', profile: profile.name } }" :data-test="`profile-create-token-${profile.name}`">Create token with this profile</router-link></li>
            <li role="none"><button type="button" role="menuitem" class="text-error" :data-test="`profile-delete-${profile.name}`" @click="pick('delete')">Delete&hellip;</button></li>
          </ul>
        </div>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useClientsStore } from '@/stores/clients'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import { tierPhrase } from '@/utils/profiles'
import type { ProfileView } from '@/types/api'

// Spec 108-i I17 / FR-040: one profile on the Profiles page. The four links go
// through Spec 109's link map (`linkTo`), so the profile travels as the
// `profile` URL filter and the target pages read it the same way everywhere.
const props = defineProps<{ profile: ProfileView; tenant?: boolean }>()
const emit = defineEmits<{ (e: 'assign'): void; (e: 'delete'): void }>()
const scope = useScopeQuery('profiles')
const menuOpen = ref(false)
function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null
  if (!next || !(event.currentTarget as HTMLElement).contains(next)) menuOpen.value = false
}
function pick(action: 'assign' | 'delete') {
  menuOpen.value = false
  if (action === 'assign') emit('assign')
  else emit('delete')
}
const clients = useClientsStore()
const profileScopeAvailable = computed(() => isScopeParamAvailable('profile'))

function clientName(id: string): string { return clients.clients.find(client => client.id === id)?.display_name ?? id }
const usedByText = computed(() => {
  const used = props.profile.used_by
  if (!used) return ''
  const parts: string[] = []
  if (used.clients.length) parts.push(used.clients.map(client => `${clientName(client.id)}${client.mode === 'locked' ? ' \u{1F512}' : ''}`).join(', '))
  if (used.tokens.length) parts.push(`${used.tokens.length} token${used.tokens.length === 1 ? '' : 's'}`)
  if (used.anonymous_profile) parts.push('anonymous callers')
  return parts.join(' · ')
})
</script>
