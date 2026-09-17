import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi, type UserProfile } from '@/services/auth-api'
import api from '@/services/api'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<UserProfile | null>(null)
  const loading = ref(true)
  const isTeamsEdition = ref(false)

  const isAuthenticated = computed(() => !!user.value)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const displayName = computed(() => user.value?.display_name || user.value?.email || '')

  // One probe at a time. On a hard reload two callers race for checkAuth():
  // App.vue's onMounted and the router guard for the initial navigation.
  // Without sharing, each ran its own /status + /auth/me pair, and the guard
  // could decide `isTeamsEdition` off whichever run happened to settle first.
  // Concurrent callers now await the same run, so every reader sees one
  // fully-settled result. A caller that must observe the CURRENT credentials
  // (reloadAfterAuth, after the API key was repaired) passes `fresh: true`:
  // that queues a new probe behind the in-flight one instead of joining a run
  // that may have been issued with the stale key.
  let inflight: Promise<void> | null = null

  async function probe() {
    try {
      // Check if this is server edition using the API service (includes API key)
      const statusRes = await api.getStatus()
      isTeamsEdition.value = statusRes.data?.edition === 'server'

      if (isTeamsEdition.value) {
        user.value = await authApi.getMe()
      }
    } catch {
      // Not authenticated or not server edition
      user.value = null
    }
  }

  function checkAuth(opts: { fresh?: boolean } = {}): Promise<void> {
    if (inflight && !opts.fresh) return inflight
    loading.value = true
    // probe() never rejects, so the chain never breaks and every queued run
    // still executes.
    const run: Promise<void> = (inflight ?? Promise.resolve()).then(probe).finally(() => {
      // Only the newest run clears the flags; a superseded one must not
      // report "settled" while a fresh probe is still queued behind it.
      if (inflight === run) {
        inflight = null
        loading.value = false
      }
    })
    inflight = run
    return run
  }

  async function logout() {
    await authApi.logout()
    user.value = null
  }

  function login() {
    window.location.href = authApi.getLoginUrl(window.location.pathname)
  }

  return {
    user,
    loading,
    isTeamsEdition,
    isAuthenticated,
    isAdmin,
    displayName,
    checkAuth,
    logout,
    login,
  }
})
