<template>
  <footer
    class="shrink-0 border-t border-base-300 bg-base-100 px-4 sm:px-6 py-2 text-xs text-base-content/60"
  >
    <!-- 768 px and wider: the links stay in one row. -->
    <div
      class="hidden md:flex flex-wrap items-center justify-center gap-x-2 gap-y-1"
      data-test="footer-links-wide"
    >
      <span class="font-medium text-base-content/70">
        MCPProxy<span v-if="displayVersion">&nbsp;{{ displayVersion }}</span>
      </span>
      <template v-for="link in linkList" :key="link.label">
        <span aria-hidden="true">·</span>
        <a :href="link.href" target="_blank" rel="noopener noreferrer" class="link link-hover">{{ link.label }}</a>
      </template>
    </div>

    <!-- Spec 134 FR-014: below 768 px the same links and the version stay
         reachable in a compact, keyboard-operable disclosure. -->
    <details class="md:hidden" data-test="footer-links-compact">
      <summary class="cursor-pointer min-h-11 flex items-center justify-center gap-2 font-medium text-base-content/70">
        <span>MCPProxy<span v-if="displayVersion">&nbsp;{{ displayVersion }}</span></span>
        <span class="font-normal">· Project links</span>
      </summary>
      <div class="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 pb-2">
        <a
          v-for="link in linkList"
          :key="link.label"
          :href="link.href"
          target="_blank"
          rel="noopener noreferrer"
          class="link link-hover inline-flex items-center min-h-11"
        >{{ link.label }}</a>
      </div>
    </details>
  </footer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useSystemStore } from '@/stores/system'
import { PROJECT_LINKS as links } from '@/config/links'

const systemStore = useSystemStore()
const linkList = [
  { label: 'Homepage', href: links.homepage },
  { label: 'GitHub', href: links.github },
  { label: 'Docs', href: links.docs },
  { label: 'Discussions', href: links.discussions },
]
// The store version already carries a leading "v" (e.g. "v0.58.1"); normalise to
// a single "v" so the footer never shows "vv0.58.1".
const displayVersion = computed(() => {
  const v = systemStore.version
  if (!v) return ''
  return v.startsWith('v') ? v : `v${v}`
})
</script>
