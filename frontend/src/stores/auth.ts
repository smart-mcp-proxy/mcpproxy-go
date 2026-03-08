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

  async function checkAuth() {
    loading.value = true
    try {
      // Check if this is teams edition using the API service (includes API key)
      const statusRes = await api.getStatus()
      isTeamsEdition.value = statusRes.data?.edition === 'teams'

      if (isTeamsEdition.value) {
        user.value = await authApi.getMe()
      }
    } catch {
      // Not authenticated or not teams edition
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
    isAuthenticated,
    isAdmin,
    displayName,
    checkAuth,
    logout,
    login,
  }
})
