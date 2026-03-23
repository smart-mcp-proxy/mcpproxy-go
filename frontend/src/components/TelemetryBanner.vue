<template>
  <div v-if="visible" class="alert alert-info">
    <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
    <div class="flex-1">
      <span>MCPProxy sends anonymous usage statistics to help improve the product. No personal data is collected. </span>
      <a
        href="https://mcpproxy.app/telemetry"
        target="_blank"
        rel="noopener noreferrer"
        class="link link-hover underline"
      >Learn more</a>
    </div>
    <button class="btn btn-sm btn-ghost" @click="dismiss" aria-label="Dismiss">
      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'

const STORAGE_KEY = 'telemetry-banner-dismissed'
const visible = ref(false)

onMounted(() => {
  visible.value = !localStorage.getItem(STORAGE_KEY)
})

function dismiss() {
  visible.value = false
  localStorage.setItem(STORAGE_KEY, 'true')
}
</script>
