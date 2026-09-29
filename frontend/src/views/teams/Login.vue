<template>
  <div class="min-h-screen flex items-center justify-center bg-base-200">
    <div class="card w-96 bg-base-100 shadow-xl">
      <div class="card-body items-center text-center">
        <h1 class="card-title text-2xl font-bold">MCPProxy Server</h1>
        <p class="text-base-content/70 mb-4">Sign in to access your MCP tools</p>
        <p v-if="authStore.bootstrapError" class="text-sm text-error" data-test="auth-bootstrap-error">
          {{ authStore.bootstrapError }}
        </p>
        <div class="divider"></div>
        <button
          class="btn btn-primary w-full"
          @click="handleLogin"
        >
          Sign in with {{ providerName }}
        </button>
        <button v-if="authStore.bootstrapError" class="btn btn-ghost btn-sm w-full" @click="retry">
          Retry
        </button>
        <p class="text-sm text-base-content/50 mt-4">
          Powered by MCPProxy
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSystemStore } from '@/stores/system'

const authStore = useAuthStore()
const systemStore = useSystemStore()
const router = useRouter()
const route = useRoute()

// Spec 107 FR-030: the label comes from the public probe
// (GET /api/v1/auth/provider → {display_name}: oauth.display_name, falling
// back to the provider family name on the server). The generic fallback only
// shows while the probe is still in flight.
const providerName = computed(() => authStore.provider?.display_name || 'your organization')

function handleLogin() {
  authStore.login()
}

function retry() {
  void authStore.checkAuth({ fresh: true }).then(() => {
    // A retry is a local bootstrap recovery, never another IdP handoff. Any
    // positively settled browser principal may leave Login: a recovered cookie
    // tenant/admin or the confirmed personal edition. A local API key alone
    // remains an API credential, not a server-edition browser session: the
    // existing auth guard deliberately keeps that principal on Login.
    // The guard puts an internal fullPath in this query; reject external,
    // login-loop, and unmatched values before passing anything to the router.
    const canLeaveLogin = !authStore.isTeamsEdition || authStore.isAuthenticated
    if (!authStore.canShowShell || authStore.bootstrapError || !canLeaveLogin) return
    // A successful usable recovery is the only Login path that owns clearing
    // the modal's suppression flag. Failed and signed-out retries keep it so
    // they cannot claim to have repaired the original authentication error.
    systemStore.setAuthRequired(false)
    const redirect = route.query.redirect
    if (typeof redirect !== 'string' || !redirect.startsWith('/') || redirect.startsWith('//')) {
      void router.replace({ name: 'home' })
      return
    }
    const target = router.resolve(redirect)
    if (target.path === '/login' || target.matched.length === 0 || target.matched.some((record) => record.name === 'not-found')) {
      void router.replace({ name: 'home' })
      return
    }
    void router.replace(target.fullPath)
  })
}
</script>
