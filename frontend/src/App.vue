<template>
  <div id="app" class="drawer lg:drawer-open">
    <input id="sidebar-drawer" type="checkbox" class="drawer-toggle" />

    <!-- Main content area -->
    <div class="drawer-content grid grid-rows-[auto_1fr] h-screen bg-base-200 lg:pl-64">
      <!-- Top Header -->
      <TopHeader />

      <!-- Page content -->
      <main class="overflow-y-auto p-6">
        <router-view v-slot="{ Component }">
          <transition name="page" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
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
import ToastContainer from '@/components/ToastContainer.vue'
import ConnectionStatus from '@/components/ConnectionStatus.vue'
import AuthErrorModal from '@/components/AuthErrorModal.vue'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import api, { type APIAuthEvent } from '@/services/api'

const systemStore = useSystemStore()
const serversStore = useServersStore()

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
}

function handleAuthModalAuthenticated() {
  authModal.show = false
  authModal.lastError = ''

  // Refresh data now that we're authenticated
  systemStore.connectEventSource()
  serversStore.fetchServers()
}

function handleAuthModalRefresh() {
  authModal.show = false
  authModal.lastError = ''

  // Reconnect with potentially new API key
  systemStore.connectEventSource()
  serversStore.fetchServers()
}

// Handle API authentication errors
function handleAuthError(event: APIAuthEvent) {
  console.log('Global auth error received:', event)
  authModal.lastError = event.error
  authModal.show = true
}

onMounted(() => {
  // Set up API error listener
  removeAPIListener = api.addEventListener(handleAuthError)

  // Connect to real-time updates
  systemStore.connectEventSource()

  // Initial data load
  serversStore.fetchServers()
})

onUnmounted(() => {
  systemStore.disconnectEventSource()

  // Clean up API event listener
  if (removeAPIListener) {
    removeAPIListener()
  }
})
</script>

<style scoped>
/* Page transitions */
.page-enter-active,
.page-leave-active {
  transition: all 0.3s ease;
}

.page-enter-from {
  opacity: 0;
  transform: translateX(10px);
}

.page-leave-to {
  opacity: 0;
  transform: translateX(-10px);
}
</style>