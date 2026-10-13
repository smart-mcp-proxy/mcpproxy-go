<!-- Compatibility entry point for extensions that imported the pre-109-h name. -->
<template><ImportServers @imported="$emit('imported', $event)" /></template>
<script setup lang="ts">
import ImportServers from './ImportServers.vue'
defineEmits<{ imported: [count: number] }>()
</script>
