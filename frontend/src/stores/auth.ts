import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi, type ProviderInfo, type UserProfile } from '@/services/auth-api'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<UserProfile | null>(null)
  const loading = ref(true)
  const isTeamsEdition = ref(false)
  // Spec 107 FR-030: the operator-chosen login label from the public probe;
  // null on the personal edition. Login.vue renders it.
  const provider = ref<ProviderInfo | null>(null)

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
      // Spec 107 FR-030 / FR-041: learn the edition from the PUBLIC probe
      // before any authenticated call. The previous detection went through
      // GET /api/v1/status with the API key, which a tenant never holds — so
      // every tenant read as "personal edition" and was bounced off /login.
      const probe = await authApi.getProvider()
      provider.value = probe
      isTeamsEdition.value = probe != null

      user.value = isTeamsEdition.value ? await authApi.getMe() : null
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
    provider,
    isAuthenticated,
    isAdmin,
    displayName,
    checkAuth,
    logout,
    login,
  }
})
