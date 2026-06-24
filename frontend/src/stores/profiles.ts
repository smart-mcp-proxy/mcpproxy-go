import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { ProfileSummary } from '@/types'
import api from '@/services/api'

// Profiles v2 (MCP-3243 / T4): client state for the Web UI profile switcher.
// Backed by the REST surface shipped in MCP-3241:
//   GET  /api/v1/profiles          → configured profiles + servers + tool count
//   GET  /api/v1/profiles/active   → server-level default active profile
//   PUT  /api/v1/profiles/active   → set/clear the default active profile
//
// The active profile is a *UI/tray default* — an empty string means "all
// servers" (zero-config default). It does not override a live MCP session's
// set_profile selection.
export const useProfilesStore = defineStore('profiles', () => {
  // State
  const profiles = ref<ProfileSummary[]>([])
  const activeProfile = ref<string>('')
  const loading = ref(false)
  const error = ref<string | null>(null)
  const loaded = ref(false)

  // Getters
  const hasProfiles = computed(() => profiles.value.length > 0)
  // Human label for the current selection; empty selection = "All servers".
  const activeLabel = computed(() => activeProfile.value || 'All servers')
  // True when the active selection is the zero-config "all servers" default.
  const isAllServers = computed(() => activeProfile.value === '')

  // Actions
  async function fetchProfiles(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const [list, active] = await Promise.all([
        api.getProfiles(),
        api.getActiveProfile(),
      ])
      if (list.success && list.data) {
        profiles.value = list.data.profiles ?? []
      } else if (!list.success) {
        error.value = list.error ?? 'Failed to load profiles'
      }
      if (active.success && active.data) {
        activeProfile.value = active.data.active_profile ?? ''
      }
      loaded.value = true
    } finally {
      loading.value = false
    }
  }

  // Set (or clear, with '') the default active profile. On success the local
  // state is updated to the server-confirmed value so the UI reflects exactly
  // what the backend stored.
  async function setActive(profile: string): Promise<boolean> {
    error.value = null
    const res = await api.setActiveProfile(profile)
    if (res.success && res.data) {
      activeProfile.value = res.data.active_profile ?? ''
      return true
    }
    error.value = res.error ?? 'Failed to set active profile'
    return false
  }

  function reset(): void {
    profiles.value = []
    activeProfile.value = ''
    error.value = null
    loaded.value = false
  }

  return {
    // state
    profiles,
    activeProfile,
    loading,
    error,
    loaded,
    // getters
    hasProfiles,
    activeLabel,
    isAllServers,
    // actions
    fetchProfiles,
    setActive,
    reset,
  }
})
