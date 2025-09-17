<template>
  <div class="space-y-6">
    <div class="breadcrumbs text-sm">
      <ul>
        <li><router-link to="/">Dashboard</router-link></li>
        <li><router-link to="/servers">Servers</router-link></li>
        <li>{{ serverName }}</li>
      </ul>
    </div>

    <div class="text-center py-20">
      <h1 class="text-3xl font-bold mb-4">Server Details</h1>
      <p class="text-base-content/70 mb-4">Details for server: {{ serverName }}</p>
      <p class="text-sm text-base-content/50">This page is coming soon!</p>
      <router-link to="/servers" class="btn btn-outline mt-4">
        Back to Servers
      </router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
interface Props {
  serverName: string
}

defineProps<Props>()
</script>