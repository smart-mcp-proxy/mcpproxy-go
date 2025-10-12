<template>
  <div v-if="hints.length > 0" class="hints-panel-wrapper">
    <!-- Collapsed State: Single Line with Bulb Icon -->
    <div
      v-if="!isExpanded"
      @click="toggleExpanded"
      class="hints-collapsed"
    >
      <span class="bulb-icon">💡</span>
      <span class="hints-title">Hints: {{ getSummaryText() }}</span>
      <svg class="expand-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
      </svg>
    </div>

    <!-- Expanded State: Full Hints Content -->
    <div v-else class="hints-expanded">
      <!-- Header with Collapse Button -->
      <div class="hints-header" @click="toggleExpanded">
        <div class="hints-header-left">
          <span class="bulb-icon">💡</span>
          <span class="hints-title">Hints</span>
        </div>
        <svg class="collapse-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
        </svg>
      </div>

      <!-- Hints Content -->
      <div class="hints-content">
        <div
          v-for="(hint, index) in hints"
          :key="index"
          class="hint-section"
        >
          <div class="hint-section-header">
            <span class="hint-icon">{{ hint.icon }}</span>
            <h3 class="hint-section-title">{{ hint.title }}</h3>
          </div>

          <p v-if="hint.description" class="hint-description">
            {{ hint.description }}
          </p>

          <div v-for="(section, sIdx) in hint.sections" :key="sIdx" class="hint-subsection">
            <h4 v-if="section.title" class="subsection-title">{{ section.title }}</h4>
            <p v-if="section.text" class="subsection-text">{{ section.text }}</p>

            <!-- Code Block -->
            <div v-if="section.codeBlock" class="code-block-wrapper">
              <div class="code-block-header">
                <span class="code-language">{{ section.codeBlock.language || 'bash' }}</span>
                <button
                  @click.stop="copyToClipboard(section.codeBlock.code)"
                  class="copy-button"
                  title="Copy to clipboard"
                >
                  📋 Copy
                </button>
              </div>
              <pre class="code-block"><code>{{ section.codeBlock.code }}</code></pre>
            </div>

            <!-- Simple Code (deprecated, for backward compatibility) -->
            <div v-if="section.code" class="simple-code">
              <pre><code>{{ section.code }}</code></pre>
            </div>

            <!-- List -->
            <ul v-if="section.list" class="hint-list">
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
  defaultExpanded?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  defaultExpanded: false
})

const isExpanded = ref(props.defaultExpanded)

const toggleExpanded = () => {
  isExpanded.value = !isExpanded.value
}

const getSummaryText = () => {
  if (props.hints.length === 0) return ''

  // Create a summary from hint titles
  const titles = props.hints.map(h => h.title).join(', ')

  // Categorize hints
  const categories: string[] = []
  const hasLLM = props.hints.some(h => h.title.toLowerCase().includes('llm') || h.title.toLowerCase().includes('agent'))
  const hasCLI = props.hints.some(h => h.title.toLowerCase().includes('cli') || h.title.toLowerCase().includes('command'))
  const hasManage = props.hints.some(h => h.title.toLowerCase().includes('manage') || h.title.toLowerCase().includes('add'))

  if (hasManage) categories.push('Manage Servers')
  if (hasCLI) categories.push('CLI')
  if (hasLLM) categories.push('LLM')

  return categories.length > 0 ? categories.join(', ') : titles.split(',')[0]
}

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
.hints-panel-wrapper {
  margin-top: 2rem;
  z-index: 10;
}

/* Collapsed State */
.hints-collapsed {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1.25rem;
  background: hsl(var(--b2));
  border: 1px solid hsl(var(--bc) / 0.15);
  border-radius: 0.5rem;
  cursor: pointer;
  transition: all 0.2s ease;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.hints-collapsed:hover {
  background: hsl(var(--b3));
  border-color: hsl(var(--bc) / 0.25);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}

.bulb-icon {
  font-size: 1.25rem;
  flex-shrink: 0;
}

.hints-title {
  font-weight: 600;
  font-size: 0.95rem;
  flex: 1;
  color: hsl(var(--bc) / 0.85);
}

.expand-icon,
.collapse-icon {
  width: 1.25rem;
  height: 1.25rem;
  flex-shrink: 0;
  color: hsl(var(--bc) / 0.6);
  transition: transform 0.2s ease;
}

/* Expanded State */
.hints-expanded {
  background: hsl(var(--b2));
  border: 1px solid hsl(var(--bc) / 0.15);
  border-radius: 0.5rem;
  overflow: hidden;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  animation: expandHints 0.3s ease;
}

@keyframes expandHints {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.hints-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1.25rem;
  cursor: pointer;
  border-bottom: 1px solid hsl(var(--bc) / 0.1);
  background: hsl(var(--b3));
}

.hints-header:hover {
  background: hsl(var(--b2));
}

.hints-header-left {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.hints-content {
  padding: 1.5rem;
  max-height: 70vh;
  overflow-y: auto;
}

/* Hint Sections */
.hint-section {
  margin-bottom: 2rem;
  padding-bottom: 2rem;
  border-bottom: 1px solid hsl(var(--bc) / 0.1);
}

.hint-section:last-child {
  margin-bottom: 0;
  padding-bottom: 0;
  border-bottom: none;
}

.hint-section-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
}

.hint-icon {
  font-size: 1.5rem;
  flex-shrink: 0;
}

.hint-section-title {
  font-size: 1.1rem;
  font-weight: 600;
  color: hsl(var(--bc));
  margin: 0;
}

.hint-description {
  margin: 0 0 1rem 0;
  color: hsl(var(--bc) / 0.7);
  font-size: 0.9rem;
  line-height: 1.5;
}

/* Subsections */
.hint-subsection {
  margin-bottom: 1.25rem;
}

.hint-subsection:last-child {
  margin-bottom: 0;
}

.subsection-title {
  font-size: 0.95rem;
  font-weight: 600;
  color: hsl(var(--bc) / 0.9);
  margin: 0 0 0.5rem 0;
}

.subsection-text {
  margin: 0 0 0.75rem 0;
  color: hsl(var(--bc) / 0.7);
  font-size: 0.875rem;
  line-height: 1.5;
}

/* Code Blocks */
.code-block-wrapper {
  background: hsl(var(--b3));
  border: 1px solid hsl(var(--bc) / 0.1);
  border-radius: 0.5rem;
  overflow: hidden;
  margin: 0.5rem 0;
}

.code-block-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  background: hsl(var(--b2));
  border-bottom: 1px solid hsl(var(--bc) / 0.1);
}

.code-language {
  font-size: 0.75rem;
  font-family: 'Courier New', Courier, monospace;
  color: hsl(var(--bc) / 0.6);
  text-transform: uppercase;
}

.copy-button {
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  background: transparent;
  border: 1px solid hsl(var(--bc) / 0.2);
  border-radius: 0.25rem;
  cursor: pointer;
  color: hsl(var(--bc) / 0.7);
  transition: all 0.2s ease;
}

.copy-button:hover {
  background: hsl(var(--bc) / 0.1);
  border-color: hsl(var(--bc) / 0.3);
  color: hsl(var(--bc));
}

.code-block {
  padding: 0.75rem;
  margin: 0;
  overflow-x: auto;
  font-family: 'Courier New', Courier, monospace;
  font-size: 0.8rem;
  line-height: 1.5;
  color: hsl(var(--bc));
  background: hsl(var(--b3));
}

.code-block code {
  font-family: inherit;
  white-space: pre;
}

/* Simple Code (backward compatibility) */
.simple-code {
  background: hsl(var(--b3));
  border: 1px solid hsl(var(--bc) / 0.1);
  border-radius: 0.5rem;
  padding: 0.75rem;
  margin: 0.5rem 0;
}

.simple-code pre {
  margin: 0;
  font-family: 'Courier New', Courier, monospace;
  font-size: 0.8rem;
  overflow-x: auto;
}

/* Lists */
.hint-list {
  margin: 0.5rem 0;
  padding-left: 1.5rem;
  color: hsl(var(--bc) / 0.8);
  font-size: 0.875rem;
  line-height: 1.6;
}

.hint-list li {
  margin-bottom: 0.5rem;
}

.hint-list li:last-child {
  margin-bottom: 0;
}

/* Scrollbar styling for hints content */
.hints-content::-webkit-scrollbar {
  width: 8px;
}

.hints-content::-webkit-scrollbar-track {
  background: hsl(var(--b3));
  border-radius: 0.25rem;
}

.hints-content::-webkit-scrollbar-thumb {
  background: hsl(var(--bc) / 0.3);
  border-radius: 0.25rem;
}

.hints-content::-webkit-scrollbar-thumb:hover {
  background: hsl(var(--bc) / 0.5);
}
</style>
