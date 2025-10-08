<template>
  <div v-if="hints.length > 0" class="hints-panel">
    <div
      v-for="(hint, index) in hints"
      :key="index"
      :class="['collapse', 'collapse-arrow', 'bg-base-200', 'mb-2', { 'collapse-open': openHints[index] }]"
    >
      <input
        type="checkbox"
        :id="`hint-${index}`"
        v-model="openHints[index]"
        class="peer"
      />
      <div class="collapse-title text-base font-medium flex items-center gap-2">
        <span class="text-2xl">{{ hint.icon }}</span>
        <span>{{ hint.title }}</span>
      </div>
      <div class="collapse-content">
        <div class="prose prose-sm max-w-none">
          <p v-if="hint.description" class="text-sm text-base-content/70 mb-3">
            {{ hint.description }}
          </p>

          <div v-for="(section, sIdx) in hint.sections" :key="sIdx" class="mb-4">
            <h4 v-if="section.title" class="text-sm font-semibold mb-2">{{ section.title }}</h4>
            <p v-if="section.text" class="text-sm mb-2">{{ section.text }}</p>

            <div v-if="section.code" class="mockup-code text-xs mb-2">
              <pre><code>{{ section.code }}</code></pre>
            </div>

            <div v-if="section.codeBlock" class="bg-base-300 rounded-lg p-3 mb-2">
              <div class="flex justify-between items-center mb-2">
                <span class="text-xs font-mono text-base-content/60">{{ section.codeBlock.language || 'bash' }}</span>
                <button
                  @click="copyToClipboard(section.codeBlock.code)"
                  class="btn btn-xs btn-ghost"
                  title="Copy to clipboard"
                >
                  📋 Copy
                </button>
              </div>
              <pre class="text-xs overflow-x-auto"><code>{{ section.codeBlock.code }}</code></pre>
            </div>

            <ul v-if="section.list" class="list-disc list-inside text-sm space-y-1">
              <li v-for="(item, lIdx) in section.list" :key="lIdx">{{ item }}</li>
            </ul>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

export interface HintSection {
  title?: string
  text?: string
  code?: string
  codeBlock?: {
    language?: string
    code: string
  }
  list?: string[]
}

export interface Hint {
  icon: string
  title: string
  description?: string
  sections: HintSection[]
}

interface Props {
  hints: Hint[]
  defaultOpen?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  defaultOpen: false
})

const openHints = ref<boolean[]>(
  props.hints.map(() => props.defaultOpen)
)

const copyToClipboard = async (text: string) => {
  try {
    await navigator.clipboard.writeText(text)
    // Could emit an event or show a toast notification here
  } catch (err) {
    console.error('Failed to copy:', err)
  }
}
</script>

<style scoped>
.hints-panel {
  margin: 1rem 0;
}

.collapse {
  border: 1px solid hsl(var(--bc) / 0.1);
}

.collapse-title {
  cursor: pointer;
  user-select: none;
}

.collapse-content {
  padding-top: 0.5rem;
}

pre {
  margin: 0;
  font-family: 'Courier New', Courier, monospace;
  white-space: pre-wrap;
  word-wrap: break-word;
}

code {
  font-family: 'Courier New', Courier, monospace;
  font-size: 0.875rem;
}

.mockup-code {
  padding: 1rem;
  background: hsl(var(--b3));
  border-radius: 0.5rem;
}

.prose {
  color: hsl(var(--bc));
}

.prose h4 {
  margin: 0.5rem 0;
  color: hsl(var(--bc));
}

.prose p {
  margin: 0.5rem 0;
}
</style>