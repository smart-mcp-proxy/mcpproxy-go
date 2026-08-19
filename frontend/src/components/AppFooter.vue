<!--
  Persistent footer shown on every page (discussion #948). Gives a running-app
  user the way back to the project — homepage, source, docs, discussions — plus
  the running version. Previously the only outbound links were buried on the
  settings page.
-->
<template>
  <footer
    class="shrink-0 border-t border-base-300 bg-base-100 px-6 py-2 text-xs text-base-content/60"
  >
    <div class="flex flex-wrap items-center justify-center gap-x-2 gap-y-1">
      <span class="font-medium text-base-content/70">
        MCPProxy<span v-if="displayVersion">&nbsp;{{ displayVersion }}</span>
      </span>
      <span aria-hidden="true">·</span>
      <a :href="links.homepage" target="_blank" rel="noopener noreferrer" class="link link-hover">Homepage</a>
      <span aria-hidden="true">·</span>
      <a :href="links.github" target="_blank" rel="noopener noreferrer" class="link link-hover">GitHub</a>
      <span aria-hidden="true">·</span>
      <a :href="links.docs" target="_blank" rel="noopener noreferrer" class="link link-hover">Docs</a>
      <span aria-hidden="true">·</span>
      <a :href="links.discussions" target="_blank" rel="noopener noreferrer" class="link link-hover">Discussions</a>
    </div>
  </footer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useSystemStore } from '@/stores/system'
import { PROJECT_LINKS as links } from '@/config/links'

const systemStore = useSystemStore()
// The store version already carries a leading "v" (e.g. "v0.58.1"); normalise to
// a single "v" so the footer never shows "vv0.58.1".
const displayVersion = computed(() => {
  const v = systemStore.version
  if (!v) return ''
  return v.startsWith('v') ? v : `v${v}`
})
</script>
