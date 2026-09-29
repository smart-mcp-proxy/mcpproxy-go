<template><ClientConnectList :show="show" @close="$emit('close')" /></template>

<script setup lang="ts">
import ClientConnectList from './ClientConnectList.vue'
defineProps<{ show: boolean }>()
defineEmits<{ close: [] }>()
</script>
