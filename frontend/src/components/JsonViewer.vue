<template>
  <div class="json-viewer-container">
    <div class="flex justify-between items-start mb-2">
      <div class="text-xs text-base-content/60">
        {{ byteSize }} bytes
      </div>
      <button
        @click="copyToClipboard"
        class="btn btn-xs btn-ghost gap-1"
        :class="{ 'btn-success': copied }"
        :title="copied ? 'Copied!' : 'Copy to clipboard'"
      >
        <svg
          v-if="!copied"
          class="w-4 h-4"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"
          />
        </svg>
        <svg
          v-else
          class="w-4 h-4"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M5 13l4 4L19 7"
          />
        </svg>
        {{ copied ? 'Copied!' : 'Copy' }}
      </button>
    </div>
    <pre
      class="json-viewer bg-base-300 p-3 rounded text-xs overflow-auto w-full"
      :style="{ maxHeight: maxHeight }"
      v-html="highlightedJson"
    ></pre>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useSystemStore } from '@/stores/system'

interface Props {
  data: any
  maxHeight?: string
}

const props = withDefaults(defineProps<Props>(), {
  maxHeight: '24rem'
})

const systemStore = useSystemStore()
const copied = ref(false)
let copyTimeout: ReturnType<typeof setTimeout> | null = null

// Compute formatted JSON string
const formattedJson = computed(() => {
  try {
    return JSON.stringify(props.data, null, 2)
  } catch (error) {
    return String(props.data)
  }
})

// Compute byte size
const byteSize = computed(() => {
  return new Blob([formattedJson.value]).size.toLocaleString()
})

// Syntax highlighting for JSON
const highlightedJson = computed(() => {
  let json = formattedJson.value

  // Escape HTML entities first to prevent XSS
  json = json
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')

  // Highlight different JSON elements with colors
  json = json
    // String values (green)
    .replace(/("(?:[^"\\]|\\.)*")\s*:/g, '<span class="text-info font-semibold">$1</span>:')
    // String values after colon (emerald/green)
    .replace(/:\s*("(?:[^"\\]|\\.)*")/g, ': <span class="text-success">$1</span>')
    // Numbers (orange)
    .replace(/:\s*(-?\d+\.?\d*)/g, ': <span class="text-warning">$1</span>')
    // Booleans (purple)
    .replace(/:\s*(true|false)/g, ': <span class="text-secondary font-medium">$1</span>')
    // Null (red/error)
    .replace(/:\s*(null)/g, ': <span class="text-error">$1</span>')

  return json
})

// Copy to clipboard functionality
const copyToClipboard = async () => {
  try {
    await navigator.clipboard.writeText(formattedJson.value)
    copied.value = true

    systemStore.addToast({
      type: 'success',
      title: 'Copied!',
      message: 'JSON copied to clipboard'
    })

    // Reset copied state after 2 seconds
    if (copyTimeout) clearTimeout(copyTimeout)
    copyTimeout = setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Copy Failed',
      message: 'Failed to copy to clipboard'
    })
  }
}

// Clean up timeout on unmount
watch(
  () => props.data,
  () => {
    copied.value = false
    if (copyTimeout) clearTimeout(copyTimeout)
  }
)
</script>

<style scoped>
.json-viewer-container {
  @apply w-full;
}

.json-viewer {
  white-space: pre-wrap;
  word-wrap: break-word;
  overflow-wrap: anywhere;
  line-height: 1.5;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', 'source-code-pro', monospace;
}

/* Smooth scrollbar styling */
.json-viewer::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.json-viewer::-webkit-scrollbar-track {
  @apply bg-base-200 rounded;
}

.json-viewer::-webkit-scrollbar-thumb {
  @apply bg-base-content/20 rounded;
}

.json-viewer::-webkit-scrollbar-thumb:hover {
  @apply bg-base-content/30;
}
</style>