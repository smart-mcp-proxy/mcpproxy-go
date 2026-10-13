<template>
  <div class="text-sm space-y-1" data-test="profile-impact">
    <p class="font-medium">{{ heading }}</p>
    <ul class="list-disc list-inside" data-test="profile-impact-list">
      <li v-for="client in usedBy.clients" :key="`c-${client.id}`" :data-test="`impact-client-${client.id}`">
        Client {{ clientName(client.id) }} ({{ client.mode }})
      </li>
      <li v-for="token in usedBy.tokens" :key="`t-${token}`" :data-test="`impact-token-${token}`">Token {{ token }}</li>
      <li v-if="usedBy.anonymous_profile" data-test="impact-anonymous">Anonymous callers</li>
      <li v-for="other in referencedBy" :key="`s-${other}`" :data-test="`impact-switchable-${other}`">Switchable list of profile {{ profiles.titleFor(other) }}</li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import type { ProfileUsedBy } from '@/types/api'

// Spec 108-i I19: what a rename or a delete touches, from the profile's used_by
// (administrators only) and from the other profiles' switchable_to lists.
const props = defineProps<{ profileName: string; usedBy: ProfileUsedBy; heading: string }>()
const profiles = useProfilesStore()
const clients = useClientsStore()
const referencedBy = computed(() => profiles.profiles
  .filter(profile => profile.name !== props.profileName && profile.switchable_to?.includes(props.profileName))
  .map(profile => profile.name))
function clientName(id: string): string { return clients.clients.find(client => client.id === id)?.display_name ?? id }
</script>
