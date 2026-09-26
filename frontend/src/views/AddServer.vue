<template>
  <div class="p-6 max-w-4xl mx-auto" data-test="add-server-page">
    <h1 class="text-2xl font-bold mb-1">Add Server</h1>
    <p class="text-sm text-base-content/70 mb-6">
      Browse the catalog, paste a URL or command, import a config, or fill in the fields yourself.
    </p>

    <div class="tabs tabs-box mb-6" data-test="add-server-tabs">
      <a
        v-for="t in tabs"
        :key="t.id"
        class="tab"
        :class="activeTab === t.id ? 'tab-active' : ''"
        :data-test="`add-server-tab-${t.id}`"
        @click="activeTab = t.id"
      >
        {{ t.label }}
      </a>
    </div>

    <CatalogSearch v-if="activeTab === 'catalog'" :source="sourceFilter" @added="handleAdded" />
    <PasteServer v-else-if="activeTab === 'paste'" @added="handleAdded" />
    <ImportServersPanel v-else-if="activeTab === 'import'" @imported="handleImported" />
    <ManualServerForm v-else-if="activeTab === 'manual'" @added="handleAdded" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'
import PasteServer from '@/components/PasteServer.vue'
import ImportServersPanel from '@/components/ImportServersPanel.vue'
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

function handleAdded(name: string) {
  systemStore.addToast({ type: 'success', title: 'Server Added', message: `${name} has been added successfully` })
  void router.push(serverDetailPath(name))
}

function handleImported(count: number) {
  systemStore.addToast({ type: 'success', title: 'Servers Imported', message: `${count} server(s) imported` })
  void router.push('/servers')
}
</script>
