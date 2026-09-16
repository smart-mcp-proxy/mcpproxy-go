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

  async function checkAuth() {
    loading.value = true
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
    } finally {
      loading.value = false
    }
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
