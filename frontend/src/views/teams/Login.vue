<template>
  <div class="min-h-screen flex items-center justify-center bg-base-200">
    <div class="card w-96 bg-base-100 shadow-xl">
      <div class="card-body items-center text-center">
        <h1 class="card-title text-2xl font-bold">MCPProxy Teams</h1>
        <p class="text-base-content/70 mb-4">Sign in to access your MCP tools</p>
        <div class="divider"></div>
        <button
          class="btn btn-primary w-full"
          @click="handleLogin"
        >
          Sign in with {{ providerName }}
        </button>
        <p class="text-sm text-base-content/50 mt-4">
          Powered by MCPProxy
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useAuthStore } from '@/stores/auth'

const authStore = useAuthStore()

// Provider name will come from status API in future; for now show generic
const providerName = 'your organization'

function handleLogin() {
  authStore.login()
}
</script>
