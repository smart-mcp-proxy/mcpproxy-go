<template>
  <div id="app" class="min-h-screen bg-base-200">
    <!-- Navigation -->
    <NavBar />

    <!-- Main Content -->
    <main class="container mx-auto px-4 py-6">
      <router-view v-slot="{ Component }">
        <transition name="page" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </main>

    <!-- Toast Notifications -->
    <ToastContainer />

    <!-- Connection Status -->
    <ConnectionStatus />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import NavBar from '@/components/NavBar.vue'
import ToastContainer from '@/components/ToastContainer.vue'
import ConnectionStatus from '@/components/ConnectionStatus.vue'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'

const systemStore = useSystemStore()
const serversStore = useServersStore()

onMounted(() => {
  // Connect to real-time updates
  systemStore.connectEventSource()

  // Initial data load
  serversStore.fetchServers()
})

onUnmounted(() => {
  systemStore.disconnectEventSource()
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