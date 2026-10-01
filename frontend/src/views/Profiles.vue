<template>
  <div class="space-y-6" data-test="profiles-page">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-3xl font-bold">Profiles</h1>
        <p class="text-base-content/70 mt-1">Profiles limit which servers and tool tiers a client or token can use.</p>
      </div>
      <button v-if="!tenant" type="button" class="btn btn-primary" data-test="profiles-add" @click="createOpen = true">Add profile</button>
    </div>

    <div v-if="store.error" role="alert" class="alert alert-error" data-test="profiles-error">
      <span>{{ errorText }}</span>
      <button type="button" class="btn btn-sm" data-test="profiles-retry" @click="store.fetchProfiles()">Retry</button>
    </div>

    <div v-else-if="store.loading && !store.loaded" class="grid gap-4 grid-cols-1 min-[768px]:grid-cols-2 min-[1280px]:grid-cols-3" aria-busy="true" data-test="profiles-skeleton">
      <div v-for="n in 3" :key="n" class="skeleton h-44 w-full" />
    </div>

    <div v-else class="grid gap-4 grid-cols-1 min-[768px]:grid-cols-2 min-[1280px]:grid-cols-3" data-test="profiles-grid">
      <!-- The All servers card is the default everything falls back to. -->
      <article class="card bg-base-100 border border-base-300" data-test="profile-card-all-servers" aria-labelledby="profile-card-title-all-servers">
        <div class="card-body p-4 space-y-2">
          <h2 id="profile-card-title-all-servers" class="card-title text-base">All servers</h2>
          <p class="text-sm">Clients and agents with no profile, and anonymous callers when no anonymous profile is set, reach every enabled server.</p>
          <p class="text-sm" data-test="profile-anonymous-status">
            <span class="opacity-70">Anonymous callers</span>
            {{ store.anonymousProfile ? profileTitle(store.anonymousProfile) : 'unconfined' }}
            <router-link v-if="!tenant" class="link ml-1" :to="{ path: '/settings', query: { tab: 'security', focus: 'anonymous_profile' } }" data-test="profile-anonymous-link">Change in Settings</router-link>
          </p>
        </div>
      </article>

      <ProfileCard
        v-for="profile in store.profiles"
        :key="profile.name"
        :profile="profile"
        :tenant="tenant"
        @assign="assignTo = profile.name"
        @delete="deleting = profile"
      />

      <div v-if="!store.profiles.length" class="card bg-base-100 border border-dashed border-base-300" data-test="profiles-empty">
        <div class="card-body p-4 space-y-2">
          <p class="text-sm">No profiles yet. Profiles limit which servers and tool tiers a client or token can use.</p>
          <button v-if="!tenant" type="button" class="btn btn-primary btn-sm self-start" data-test="profiles-create-empty" @click="createOpen = true">Create a profile</button>
        </div>
      </div>
    </div>

    <ProfileCreateDialog :open="createOpen" @close="createOpen = false" @created="onCreated" />
    <AssignClientDialog :open="!!assignTo" :profile-name="assignTo" @close="assignTo = ''" @assigned="store.fetchProfiles()" />
    <ProfileDeleteDialog v-if="deleting" :open="true" :profile="deleting" @close="deleting = null" @deleted="onDeleted" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ProfileCard from '@/components/profiles/ProfileCard.vue'
import ProfileCreateDialog from '@/components/profiles/ProfileCreateDialog.vue'
import ProfileDeleteDialog from '@/components/profiles/ProfileDeleteDialog.vue'
import AssignClientDialog from '@/components/profiles/AssignClientDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { useProfilesStore } from '@/stores/profiles'
import { useClientsStore } from '@/stores/clients'
import type { ProfileView } from '@/types/api'

// Spec 108-i I17 / FR-040: the Profiles page. A server-edition tenant sees the
// profiles it can reach, read only and without `used_by`.
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const store = useProfilesStore()
const clients = useClientsStore()
const tenant = computed(() => auth.principalKind === 'tenant')
const createOpen = ref(false)
const assignTo = ref('')
const deleting = ref<ProfileView | null>(null)

const errorText = computed(() => {
  if (store.errorStatus === 503) return 'Profile evaluation is unavailable; retry in a moment'
  return store.error ?? 'Failed to load profiles'
})

function profileTitle(name: string): string { return store.titleFor(name) }

// "+ Add -> Profile" (and the palette) arrive as /profiles?create=1. Open the
// dialog, then drop `create` so a reload or Back does not reopen it - the same
// pattern as the token dialog.
function consumeCreateParam() {
  if (route.query.create !== '1') return
  if (!tenant.value) createOpen.value = true
  const { create: _create, ...rest } = route.query
  void router.replace({ query: rest })
}
watch(() => route.query.create, consumeCreateParam)

function onCreated(name: string) {
  createOpen.value = false
  void store.fetchProfiles()
  void router.push({ name: 'profile-editor', params: { name } })
}

function onDeleted() {
  deleting.value = null
  void store.fetchProfiles()
  void clients.refreshPresence()
}

onMounted(() => {
  consumeCreateParam()
  void store.fetchProfiles()
  // Who uses a profile (client names) comes from the clients store.
  if (!tenant.value && !clients.clients.length) void clients.refreshPresence()
})
</script>
