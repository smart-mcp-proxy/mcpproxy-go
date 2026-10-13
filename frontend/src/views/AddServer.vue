<template>
  <div class="p-6 max-w-4xl mx-auto" data-test="add-server-page">
    <h1 class="text-2xl font-bold mb-1">Add Server</h1>
    <p class="text-sm text-base-content/70 mb-6">
      Browse the catalog, paste a URL or command, import a config, or fill in the fields yourself.
    </p>

    <div class="tabs tabs-box mb-6" role="tablist" aria-label="Add server method" data-test="add-server-tabs" @keydown="onTabKeydown">
      <button
        v-for="t in tabs"
        :id="`add-tab-${t.id}`"
        :key="t.id"
        type="button"
        role="tab"
        class="tab"
        :class="activeTab === t.id ? 'tab-active' : ''"
        :aria-selected="activeTab === t.id"
        :aria-controls="`add-panel-${t.id}`"
        :tabindex="activeTab === t.id ? 0 : -1"
        :data-test="`add-server-tab-${t.id}`"
        @click="activeTab = t.id"
      >
        {{ t.label }}
      </button>
    </div>

    <div :id="`add-panel-${activeTab}`" role="tabpanel" :aria-labelledby="`add-tab-${activeTab}`" data-test="add-server-panel">
      <CatalogSearch v-if="activeTab === 'catalog'" :source="sourceFilter" @added="handleAdded" />
      <PasteServer v-else-if="activeTab === 'paste'" @added="handleAdded" />
      <ImportServers v-else-if="activeTab === 'import'" @imported="handleImported" />
      <ManualServerForm v-else-if="activeTab === 'manual'" @added="handleAdded" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'
import PasteServer from '@/components/PasteServer.vue'
import ImportServers from '@/components/ImportServers.vue'
import ManualServerForm from '@/components/ManualServerForm.vue'
import { useSystemStore } from '@/stores/system'
import { serverDetailPath } from '@/utils/serverRoute'

// Spec 109 FR-062: Catalog (default) · Paste · Import · Manual, selected by
// ?tab=, written back with router.replace so the URL stays deep-linkable and
// other query params (notably ?source=, the catalog-source filter) survive.
type TabId = 'catalog' | 'paste' | 'import' | 'manual'
const tabs: { id: TabId; label: string }[] = [
  { id: 'catalog', label: 'Catalog' },
  { id: 'paste', label: 'Paste' },
  { id: 'import', label: 'Import' },
  { id: 'manual', label: 'Manual' },
]
const validTabs = new Set(tabs.map((t) => t.id))

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()

function tabFromRoute(): TabId {
  const q = route.query.tab
  return typeof q === 'string' && validTabs.has(q as TabId) ? (q as TabId) : 'catalog'
}

const activeTab = ref<TabId>(tabFromRoute())
// ?source= narrows the Catalog tab only — it never selects a tab (FR-062).
const sourceFilter = computed(() => (typeof route.query.source === 'string' ? route.query.source : undefined))

watch(activeTab, (tab) => {
  void router.replace({ query: { ...route.query, tab } })
})
watch(
  () => route.query.tab,
  () => {
    activeTab.value = tabFromRoute()
  }
)

// WAI-ARIA tabs with automatic activation: Arrow keys, Home and End move focus
// and selection together; only the selected tab is in the Tab order.
function onTabKeydown(event: KeyboardEvent) {
  const ids = tabs.map((t) => t.id)
  const at = ids.indexOf(activeTab.value)
  let next = -1
  switch (event.key) {
    case 'ArrowRight': next = (at + 1) % ids.length; break
    case 'ArrowLeft': next = (at - 1 + ids.length) % ids.length; break
    case 'Home': next = 0; break
    case 'End': next = ids.length - 1; break
    default: return
  }
  event.preventDefault()
  activeTab.value = ids[next]
  void nextTick(() => document.getElementById(`add-tab-${ids[next]}`)?.focus())
}

function handleAdded(name: string) {
  systemStore.addToast({ type: 'success', title: 'Server Added', message: `${name} has been added successfully` })
  void router.push(serverDetailPath(name))
}

function handleImported(count: number) {
  systemStore.addToast({ type: 'success', title: 'Servers Imported', message: `${count} server(s) imported` })
  void router.push('/servers')
}
</script>
