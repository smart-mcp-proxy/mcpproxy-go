<template>
  <!-- Profiles v2 (MCP-3243 / T4): default active-profile switcher. Mirrors the
       MCP-endpoints dropdown affordance in the header. -->
  <div class="relative" data-test="profile-switcher">
    <button
      type="button"
      data-test="profile-switcher-button"
      class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg cursor-pointer hover:bg-base-300 transition-colors text-sm"
      :title="buttonTitle"
      @click="toggle"
    >
      <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
      </svg>
      <span class="text-xs opacity-60">Profile:</span>
      <span class="font-medium truncate max-w-[10rem]" data-test="profile-switcher-active">{{ activeLabel }}</span>
      <svg
        class="w-3 h-3 opacity-60 transition-transform"
        :class="{ 'rotate-180': open }"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
      >
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
      </svg>
    </button>

    <div
      v-if="open"
      class="absolute right-0 top-full mt-2 p-2 shadow-lg bg-base-100 rounded-box w-80 border border-base-300 z-50"
      data-test="profile-switcher-menu"
    >
      <div class="text-xs font-semibold opacity-60 mb-1 px-2 pt-1">Active profile</div>

      <!-- "All servers" / zero-config default — always selectable to clear. -->
      <button
        type="button"
        data-test="profile-option-all"
        class="w-full text-left px-2 py-2 rounded hover:bg-base-200 flex items-center justify-between group"
        :class="{ 'bg-base-200': isAllServers }"
        @click="select('')"
      >
        <div class="min-w-0">
          <div class="flex items-center space-x-2">
            <span class="font-medium">All servers</span>
            <span v-if="isAllServers" class="badge badge-xs badge-primary" data-test="profile-active-badge">active</span>
          </div>
          <div class="text-xs opacity-50 mt-0.5">No profile filter — every enabled server is in scope</div>
        </div>
        <svg v-if="isAllServers" class="w-4 h-4 text-success shrink-0 ml-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
        </svg>
      </button>

      <div v-if="profilesStore.hasProfiles" class="divider my-1 opacity-40" />

      <!-- Empty / zero-profile state. -->
      <div
        v-if="!profilesStore.hasProfiles && !profilesStore.loading"
        class="px-2 py-3 text-xs opacity-60"
        data-test="profile-switcher-empty"
      >
        No profiles configured. Add a <code class="font-mono">profiles</code> block to your config to scope tool
        discovery to a subset of servers.
      </div>

      <div v-if="profilesStore.loading && !profilesStore.loaded" class="px-2 py-3 text-xs opacity-60" data-test="profile-switcher-loading">
        Loading profiles…
      </div>

      <!-- Configured profiles. -->
      <button
        v-for="p in profilesStore.profiles"
        :key="p.name"
        type="button"
        :data-test="`profile-option-${p.name}`"
        class="w-full text-left px-2 py-2 rounded hover:bg-base-200 flex items-center justify-between group"
        :class="{ 'bg-base-200': p.name === profilesStore.activeProfile }"
        @click="select(p.name)"
      >
        <div class="min-w-0">
          <div class="flex items-center space-x-2">
            <span class="font-medium truncate">{{ p.name }}</span>
            <span
              v-if="p.name === profilesStore.activeProfile"
              class="badge badge-xs badge-primary"
              :data-test="`profile-active-badge-${p.name}`"
            >active</span>
          </div>
          <div class="text-xs opacity-50 mt-0.5">
            {{ p.servers.length }} {{ p.servers.length === 1 ? 'server' : 'servers' }}
            · {{ p.tool_count }} {{ p.tool_count === 1 ? 'tool' : 'tools' }}
          </div>
        </div>
        <svg
          v-if="p.name === profilesStore.activeProfile"
          class="w-4 h-4 text-success shrink-0 ml-2"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
        </svg>
      </button>

      <div
        v-if="profilesStore.error"
        class="px-2 py-2 text-xs text-error"
        data-test="profile-switcher-error"
      >
        {{ profilesStore.error }}
      </div>
    </div>

    <!-- Click-outside overlay -->
    <div v-if="open" class="fixed inset-0 z-40" @click="open = false" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useProfilesStore } from '@/stores/profiles'

const profilesStore = useProfilesStore()

const open = ref(false)
const activeLabel = computed(() => profilesStore.activeLabel)
const isAllServers = computed(() => profilesStore.isAllServers)
const buttonTitle = computed(() =>
  isAllServers.value
    ? 'Active profile: All servers (no filter)'
    : `Active profile: ${profilesStore.activeProfile}`
)

function toggle() {
  open.value = !open.value
  // Refresh on open so tool counts / membership reflect the latest index.
  if (open.value) {
    void profilesStore.fetchProfiles()
  }
}

async function select(name: string) {
  // No-op if already selected — just close.
  if (name === profilesStore.activeProfile) {
    open.value = false
    return
  }
  await profilesStore.setActive(name)
  open.value = false
}

onMounted(() => {
  void profilesStore.fetchProfiles()
})
</script>
