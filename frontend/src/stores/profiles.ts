import { defineStore } from 'pinia'
import { ref, computed, onScopeDispose } from 'vue'
import type { ProfileView } from '@/types'
import api from '@/services/api'

// Profiles v3 (Spec 108-i). The list behind the Profiles page, the Viewing chip
// in the header, every profile picker and the anonymous-callers setting.
//
// `profiles.changed` is an invalidation, not a payload: it carries no profile
// data, so the store refetches GET /profiles (debounced, because one config
// write can emit several). Scoped SSE subscribers never receive the event and
// nothing here depends on it for correctness - a page that just mutated a
// profile refetches itself.
export const PROFILES_CHANGED_EVENT = 'mcpproxy:profiles.changed'
const REFETCH_DEBOUNCE_MS = 250

export const useProfilesStore = defineStore('profiles', () => {
  const profiles = ref<ProfileView[]>([])
  const anonymousProfile = ref('')
  const loading = ref(false)
  const error = ref<string | null>(null)
  const errorStatus = ref(0)
  const loaded = ref(false)

  const hasProfiles = computed(() => profiles.value.length > 0)
  const byName = computed(() => new Map(profiles.value.map(profile => [profile.name, profile])))

  // The human label of a profile name; unknown names (a dangling pin) fall back
  // to the slug so nothing renders empty.
  function titleFor(name: string | undefined): string {
    if (!name) return 'All servers'
    const profile = byName.value.get(name)
    return profile?.title || name
  }

  // Every fetch takes a ticket and only the latest applies: a slow answer that
  // started before a mutation or an invalidation must not overwrite a newer one.
  let fetchTicket = 0
  async function fetchProfiles(): Promise<void> {
    const ticket = ++fetchTicket
    loading.value = true
    error.value = null
    errorStatus.value = 0
    try {
      const list = await api.getProfiles()
      if (ticket !== fetchTicket) return
      profiles.value = list?.profiles ?? []
      anonymousProfile.value = list?.anonymous_profile ?? ''
      loaded.value = true
    } catch (err) {
      if (ticket !== fetchTicket) return
      error.value = err instanceof Error ? err.message : 'Failed to load profiles'
      errorStatus.value = (err as { status?: number })?.status ?? 0
    } finally {
      if (ticket === fetchTicket) loading.value = false
    }
  }

  let refetchTimer: ReturnType<typeof setTimeout> | null = null
  function invalidate(): void {
    if (refetchTimer) clearTimeout(refetchTimer)
    refetchTimer = setTimeout(() => {
      refetchTimer = null
      void fetchProfiles()
    }, REFETCH_DEBOUNCE_MS)
  }
  // used_by (clients and tokens per profile) changes when a credential is
  // issued, revoked or rebound: refetch on those invalidations too (Spec 115
  // UI-004). The names are spelled here to avoid a store import cycle.
  const USED_BY_EVENTS = ['mcpproxy:credentials.changed', 'mcpproxy:client.binding_changed']
  if (typeof window !== 'undefined') {
    window.addEventListener(PROFILES_CHANGED_EVENT, invalidate)
    for (const name of USED_BY_EVENTS) window.addEventListener(name, invalidate)
    onScopeDispose(() => {
      window.removeEventListener(PROFILES_CHANGED_EVENT, invalidate)
      for (const name of USED_BY_EVENTS) window.removeEventListener(name, invalidate)
      if (refetchTimer) clearTimeout(refetchTimer)
    })
  }

  function reset(): void {
    profiles.value = []
    anonymousProfile.value = ''
    error.value = null
    errorStatus.value = 0
    loaded.value = false
  }

  return {
    profiles,
    anonymousProfile,
    loading,
    error,
    errorStatus,
    loaded,
    hasProfiles,
    byName,
    titleFor,
    fetchProfiles,
    reset,
  }
})
