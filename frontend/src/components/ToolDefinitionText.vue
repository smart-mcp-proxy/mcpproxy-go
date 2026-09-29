<template>
  <div class="space-y-1" data-test="tool-definition-text">
    <p class="text-xs text-base-content/60" data-test="tool-definition-untrusted">from the server, not verified</p>
    <!-- v-text is intentional: tool definitions are untrusted upstream text. -->
    <pre class="whitespace-pre-wrap break-words font-sans text-sm" data-test="tool-definition-value" v-text="visibleText"></pre>
    <button v-if="truncated" type="button" class="btn btn-link btn-xs px-0" data-test="tool-definition-toggle" @click="expanded = !expanded">
      {{ expanded ? 'Show less' : 'Show all' }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

const props = withDefaults(defineProps<{ text?: string; limit?: number }>(), { text: '', limit: 1200 })
const expanded = ref(false)
const truncated = computed(() => props.text.length > props.limit)
const visibleText = computed(() => !truncated.value || expanded.value ? props.text : `${props.text.slice(0, props.limit)}…`)
</script>
