<template>
  <button class="btn btn-primary" data-test="open-client-connect" @click="open = true">Connect client</button>
  <ConnectModal :show="open" @close="open = false" />
</template>

<script setup lang="ts">
import { ref } from 'vue'
import ConnectModal from './ConnectModal.vue'

const open = ref(false)
</script>
