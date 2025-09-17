<template>
  <div
    v-if="!systemStore.connected"
    class="fixed bottom-4 left-4 alert alert-warning shadow-lg max-w-sm z-40"
  >
    <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
    </svg>
    <div>
      <h3 class="font-bold">Connection Lost</h3>
      <div class="text-xs">Reconnecting to server...</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
</script>