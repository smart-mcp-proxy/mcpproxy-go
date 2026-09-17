import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi, type ProviderInfo, type UserProfile } from '@/services/auth-api'
import api from '@/services/api'

/**
 * Spec 107 PR-C, T088 (data-model.md "Frontend surfaces").
 *
 * Which principal issued the current session, for gating admin-only fetches
 * (FR-041):
 *   - 'tenant'  a server-edition session user, role !== 'admin', no local
 *               API key.
 *   - 'admin'   a server-edition session user, role === 'admin'.
 *   - 'api_key' a local API key is present (personal edition, or any
 *               server-edition credential carried alongside/instead of the
 *               session cookie) — today's behaviour, unchanged.
 * A local API key always wins: it is how admin tooling and CI talk to a
 * server-edition instance without a browser session, and it must keep full
 * access regardless of the session's own role.
 */
export type PrincipalKind = 'tenant' | 'admin' | 'api_key'

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

  const principalKind = computed<PrincipalKind>(() => {
    // Defensive: some unit tests stub '@/services/api' with a partial
    // default export that predates this method. Treat that the same as "no
    // local key" rather than throwing out of an unrelated store's computed.
    const hasKey = typeof api.hasAPIKey === 'function' && api.hasAPIKey()
    if (hasKey) return 'api_key'
    if (isTeamsEdition.value && user.value) {
      return isAdmin.value ? 'admin' : 'tenant'
    }
    return 'api_key'
  })

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
    principalKind,
    displayName,
    checkAuth,
    logout,
    login,
  }
})
