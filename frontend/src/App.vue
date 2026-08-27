<template>
  <div id="app" class="drawer lg:drawer-open">
    <input id="sidebar-drawer" type="checkbox" class="drawer-toggle" />

    <!-- Main content area. The left padding is bound to sidebar collapsed
         state so the content fluidly reclaims space when the sidebar shrinks
         to its icon rail. -->
    <div
      class="drawer-content grid grid-rows-[auto_1fr_auto] h-screen bg-base-200 transition-[padding] duration-200 ease-out"
      :class="systemStore.sidebarCollapsed ? 'lg:pl-14' : 'lg:pl-64'"
    >
      <!-- Top Header -->
      <TopHeader />

      <!-- Page content. `min-h-0` / `min-w-0`: a grid item defaults to
           `min-height:auto`, so a tall page pushed this scroll container past
           its track and the footer ended up overlapping the last row at 390px
           (UX audit F14). -->
      <main class="overflow-y-auto min-h-0 min-w-0 p-4 sm:p-6">
        <!-- #1065: keyed on authEpoch so repairing a failed auth remounts the
             current view. Views hold their load errors in component-local refs
             that nothing outside can reach, so a successful sign-in used to
             leave a stale red error panel and zero rows behind the recovered
             header. Key on authEpoch ONLY -- folding in the route path would
             remount ServerDetail on every :serverName change and break the
             Dashboard's single instance across /, /usage and /overview. -->
        <router-view :key="systemStore.authEpoch" />
      </main>

      <!-- Persistent footer with project links (discussion #948) -->
      <AppFooter />
    </div>

    <!-- Sidebar -->
    <SidebarNav />

    <!-- Toast Notifications -->
    <ToastContainer />

    <!-- Connection Status -->
    <ConnectionStatus />

    <!-- Authentication Error Modal -->
    <AuthErrorModal
      :show="authModal.show"
      :can-close="authModal.canClose"
      :last-error="authModal.lastError"
      @close="handleAuthModalClose"
      @authenticated="handleAuthModalAuthenticated"
      @refresh="handleAuthModalRefresh"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import SidebarNav from '@/components/SidebarNav.vue'
import TopHeader from '@/components/TopHeader.vue'
import AppFooter from '@/components/AppFooter.vue'
import ToastContainer from '@/components/ToastContainer.vue'
import ConnectionStatus from '@/components/ConnectionStatus.vue'
import AuthErrorModal from '@/components/AuthErrorModal.vue'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import { useAuthStore } from '@/stores/auth'
import api, { type APIAuthEvent } from '@/services/api'

const systemStore = useSystemStore()
const serversStore = useServersStore()
const authStore = useAuthStore()

// Authentication modal state
const authModal = reactive({
  show: false,
  canClose: true, // Allow closing by default (users can continue without API key for now)
  lastError: ''
})

// API event listener cleanup function
let removeAPIListener: (() => void) | null = null

// Authentication modal handlers
function handleAuthModalClose() {
  authModal.show = false
  authModal.lastError = ''
  systemStore.setAuthRequired(false)
}

// Re-prime everything that only loads at mount. The <router-view> key covers
// the routed view, but these surfaces live outside it and would otherwise keep
// their failed state until a full page reload (#1065).
async function reloadAfterAuth() {
  systemStore.connectEventSource()
  serversStore.fetchServers()
  systemStore.fetchInfo() // TopHeader version / update state
  systemStore.fetchRouting() // TopHeader routing chip
  // Server-edition role-based nav. The router guard does not run for a
  // key-driven remount, so re-check here.
  await authStore.checkAuth()
}

function handleAuthModalAuthenticated() {
  authModal.show = false
  authModal.lastError = ''
  // markAuthRecovered, not setAuthRequired(false): this path validated the key,
  // so it is safe to invalidate every view that failed while auth was broken.
  systemStore.markAuthRecovered()

  void reloadAfterAuth()
}

function handleAuthModalRefresh(verified: boolean) {
  if (!verified) {
    // The reloaded key did not authenticate. Leave the modal and the
    // auth-required flag in place rather than remounting every view onto a
    // key that is still 401.
    return
  }

  authModal.show = false
  authModal.lastError = ''
  // The modal verified the reloaded key, so this is a real recovery and the
  // views holding stale auth errors must be invalidated too (#1065).
  systemStore.markAuthRecovered()

  void reloadAfterAuth()
}

// Handle API authentication errors
function handleAuthError(event: APIAuthEvent) {
  console.log('Global auth error received:', event)
  authModal.lastError = event.error
  authModal.show = true
  // Audit F28: one cause, one message. The modal now suppresses the reconnect
  // toast and the inline load errors it would otherwise compete with.
  systemStore.setAuthRequired(true)
}

onMounted(async () => {
  // Initialize auth state (needed for server edition role-based nav)
  await authStore.checkAuth()

  // Set up API error listener
  removeAPIListener = api.addEventListener(handleAuthError)

  // Connect to real-time updates
  systemStore.connectEventSource()

  // Initial data load
  serversStore.fetchServers()

  // Fetch version info
  systemStore.fetchInfo()

  // Fetch routing mode info
  systemStore.fetchRouting()
})

onUnmounted(() => {
  systemStore.disconnectEventSource()

  // Clean up API event listener
  if (removeAPIListener) {
    removeAPIListener()
  }
})
</script>

<!-- Page transitions removed: caused CSS transition deadlock blocking SPA navigation (QA 2026-03-29) -->
