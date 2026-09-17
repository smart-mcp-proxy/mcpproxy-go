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
  // fully-settled result; a call issued after that run has finished starts a
  // fresh probe (reloadAfterAuth relies on this).
  let inflight: Promise<void> | null = null

  function checkAuth(): Promise<void> {
    if (inflight) return inflight
    loading.value = true
    inflight = (async () => {
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
      } finally {
        loading.value = false
        inflight = null
      }
    })()
    return inflight
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
