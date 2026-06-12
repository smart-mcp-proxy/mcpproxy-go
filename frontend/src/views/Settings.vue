<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Configuration</h1>
        <p class="text-base-content/70 mt-1">
          Manage mcpproxy settings. Changes save instantly; a badge marks fields that need a restart.
          <a
            :href="`${DOCS_BASE}/configuration/config-file`"
            target="_blank"
            rel="noopener noreferrer"
            class="link link-primary"
            data-test="settings-docs-reference"
          >Full configuration reference ↗</a>
        </p>
      </div>
      <div class="flex items-center gap-2">
        <label class="input input-bordered input-sm flex items-center gap-2 w-64">
          <span class="opacity-50">🔍</span>
          <input v-model="search" type="text" class="grow" placeholder="Search all settings…" data-test="settings-search" />
          <button v-if="search" class="opacity-50 hover:opacity-100" @click="search = ''" title="Clear">✕</button>
        </label>
        <button class="btn btn-sm btn-outline" :disabled="loading" @click="loadConfig" data-test="settings-reload">
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          <span v-else>Reload</span>
        </button>
      </div>
    </div>

    <!-- Search results (across all sections) -->
    <div v-if="loaded && search.trim()" class="card bg-base-100 shadow-md" data-test="settings-search-results">
      <div class="card-body">
        <h2 class="card-title text-lg">🔍 Search results <span class="text-sm font-normal text-base-content/60">({{ filteredFields.length }})</span></h2>
        <SettingsSection section-id="search" :fields="filteredFields" :working="state.working" :original="state.original" />
      </div>
    </div>

    <!-- Tabs -->
    <div v-show="!search.trim()" role="tablist" class="tabs tabs-bordered" data-test="settings-tabs">
      <button
        v-for="t in tabs"
        :key="t.id"
        role="tab"
        class="tab gap-2"
        :class="{ 'tab-active font-semibold': activeTab === t.id }"
        :data-test="`settings-tab-${t.id}`"
        @click="activeTab = t.id"
      >
        <span>{{ t.icon }}</span> {{ t.label }}
      </button>
    </div>

    <div v-if="loadError" class="alert alert-error">
      <span>{{ loadError }}</span>
    </div>

    <template v-else-if="loaded && !search.trim()">
      <!-- Security & Access -->
      <div v-show="activeTab === 'security'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg">🔒 Security &amp; Access</h2>
          <p class="text-sm text-base-content/60 mb-2">The settings that most affect how exposed and protected your instance is.</p>
          <!-- connect-a-client helper -->
          <div class="alert bg-base-200 border-base-300 mb-3 flex-col sm:flex-row items-start sm:items-center gap-2">
            <span class="text-sm grow">
              🔌 Connecting an AI client (Claude, Cursor, VS Code…)? The helper registers mcpproxy with the right endpoint and API key in the client's config.
            </span>
            <button class="btn btn-sm btn-primary" data-test="settings-connect-client" @click="showConnect = true">
              Connect a client
            </button>
          </div>
          <!-- posture summary -->
          <div class="flex flex-wrap gap-2 mb-4" data-test="settings-posture">
            <span
              v-for="p in posture"
              :key="p.label"
              class="badge gap-1"
              :class="p.good ? 'badge-success badge-outline' : 'badge-warning'"
              :title="p.good ? 'OK' : 'Review this'"
            >
              {{ p.label }}: {{ p.on ? 'on' : 'off' }}
            </span>
          </div>
          <SettingsSection section-id="security" :fields="securityFields" :working="state.working" :original="state.original" />
        </div>
      </div>

      <!-- General -->
      <div v-show="activeTab === 'general'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg">⚙️ General</h2>
          <SettingsSection section-id="general" :fields="generalFields" :working="state.working" :original="state.original" />
        </div>
      </div>

      <!-- Advanced -->
      <div v-show="activeTab === 'advanced'" class="space-y-3">
        <details v-for="acc in advancedAccordions" :key="acc.id" class="collapse collapse-arrow bg-base-100 shadow-md">
          <summary class="collapse-title font-medium" :data-test="`settings-accordion-${acc.id}`">{{ acc.title }}</summary>
          <div class="collapse-content">
            <p v-if="acc.description || acc.docs" class="text-xs text-base-content/60 mb-2">
              {{ acc.description }}
              <a
                v-if="acc.docs"
                :href="docsUrl(acc.docs)"
                target="_blank"
                rel="noopener noreferrer"
                class="link link-primary"
                :data-test="`settings-accordion-docs-${acc.id}`"
              >Learn more ↗</a>
            </p>
            <SettingsSection :section-id="acc.id" :fields="acc.fields" :working="state.working" :original="state.original" />
          </div>
        </details>
        <div class="text-xs text-base-content/50 px-1">
          Complex lists/maps (image map, custom detection patterns, environment vars, registries) and
          <RouterLink to="/" class="link">servers</RouterLink> are managed on their own pages or the Raw JSON tab.
        </div>
      </div>

      <!-- Server edition / multi-user settings (server edition only) -->
      <div v-if="hasServerEdition" v-show="activeTab === 'teams'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg" data-test="settings-server-edition-title">{{ serverEditionTitle }}</h2>
          <SettingsSection section-id="teams" :fields="serverEditionFields" :working="state.working" :original="state.original" />
        </div>
      </div>

      <!-- Raw JSON (existing Monaco editor) -->
      <div v-show="activeTab === 'raw'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex justify-between items-center mb-2">
            <h2 class="card-title text-lg">{ } Raw JSON</h2>
            <div v-if="configStatus" :class="['badge', configStatus.valid ? 'badge-success' : 'badge-error']">
              {{ configStatus.valid ? '✓ Valid' : '✗ Invalid' }}
            </div>
          </div>
          <p class="text-sm text-base-content/60 mb-2">Full configuration editor. Edits here apply the entire document.</p>
          <div class="border border-base-300 rounded-lg overflow-hidden" style="height: 560px;">
            <vue-monaco-editor
              v-model:value="configJson"
              language="json"
              theme="vs-dark"
              :options="editorOptions"
              @change="handleConfigChange"
            />
          </div>
          <div v-if="configErrors.length > 0" class="alert alert-error mt-4">
            <div>
              <h3 class="font-bold">Validation Errors</h3>
              <ul class="list-disc list-inside text-sm">
                <li v-for="(err, i) in configErrors" :key="i"><span class="font-mono">{{ err.field }}</span>: {{ err.message }}</li>
              </ul>
            </div>
          </div>
          <div class="flex justify-end gap-2 mt-4">
            <button class="btn btn-outline btn-sm" :disabled="validatingConfig || !configJson" @click="validateConfig" data-test="settings-raw-validate">
              <span v-if="validatingConfig" class="loading loading-spinner loading-xs"></span> Validate
            </button>
            <button class="btn btn-primary btn-sm" :disabled="applyingConfig || configErrors.length > 0 || !configJson" @click="applyConfig" data-test="settings-raw-apply">
              <span v-if="applyingConfig" class="loading loading-spinner loading-xs"></span> Apply
            </button>
          </div>
        </div>
      </div>
    </template>

    <CollapsibleHintsPanel :hints="settingsHints" />

    <!-- Connect-a-client helper (shared with Dashboard) -->
    <ConnectModal :show="showConnect" @close="showConnect = false" />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { RouterLink } from 'vue-router'
import { VueMonacoEditor } from '@guolao/vue-monaco-editor'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import ConnectModal from '@/components/ConnectModal.vue'
import SettingsSection from '@/components/settings/SettingsSection.vue'
import {
  SECURITY_FIELDS,
  GENERAL_FIELDS,
  ADVANCED_ACCORDIONS,
  SERVER_EDITION_FIELDS,
  SERVER_EDITION_TAB_LABEL,
  SERVER_EDITION_SECTION_TITLE,
  DOCS_BASE,
  docsUrl,
  type SettingField,
} from '@/views/settings/fields'
import api from '@/services/api'

const serversStore = useServersStore()
const systemStore = useSystemStore()

const securityFields = SECURITY_FIELDS
const generalFields = GENERAL_FIELDS
const advancedAccordions = ADVANCED_ACCORDIONS
const serverEditionFields = SERVER_EDITION_FIELDS
const serverEditionTitle = SERVER_EDITION_SECTION_TITLE

// ---- form state ----
const loading = ref(false)
const loaded = ref(false)
const loadError = ref('')
const activeTab = ref<string>('security')
const showConnect = ref(false)
const state = reactive<{ working: any; original: any }>({ working: {}, original: {} })
// Server-edition (multi-user) config lives under `server_edition` (MCP-1086).
// Gate on the canonical key, falling back to the legacy `teams` key so a config
// written before the rename still surfaces the Server Edition tab.
const hasServerEdition = computed(
  () => state.working && (state.working.server_edition != null || state.working.teams != null)
)

// cross-section search: type to find any setting across all tabs
const search = ref('')
const allFields = computed<SettingField[]>(() => [
  ...securityFields,
  ...generalFields,
  ...advancedAccordions.flatMap((a) => a.fields),
  ...(hasServerEdition.value ? serverEditionFields : []),
])
const filteredFields = computed<SettingField[]>(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return []
  return allFields.value.filter(
    (f) =>
      f.label.toLowerCase().includes(q) ||
      f.key.toLowerCase().includes(q) ||
      (f.help?.toLowerCase().includes(q) ?? false)
  )
})

// at-a-glance security posture (computed from the working config)
const posture = computed(() => {
  const w: any = state.working || {}
  const sdd = w.sensitive_data_detection?.enabled !== false
  const quarantine = w.quarantine_enabled !== false // default-on
  return [
    { label: 'Quarantine', on: quarantine, good: quarantine },
    { label: 'MCP auth', on: !!w.require_mcp_auth, good: !!w.require_mcp_auth },
    { label: 'Docker isolation', on: !!(w.docker_isolation && w.docker_isolation.enabled), good: !!(w.docker_isolation && w.docker_isolation.enabled) },
    { label: 'Secret scan', on: sdd, good: sdd },
    { label: 'Code exec', on: !!w.enable_code_execution, good: !w.enable_code_execution },
    { label: 'Read-only', on: !!w.read_only_mode, good: true },
    { label: 'Reveal headers', on: !!w.reveal_secret_headers, good: !w.reveal_secret_headers },
  ]
})

const tabs = computed(() => {
  const base = [
    { id: 'security', label: 'Security & Access', icon: '🔒' },
    { id: 'general', label: 'General', icon: '⚙️' },
    { id: 'advanced', label: 'Advanced', icon: '🧰' },
  ] as Array<{ id: string; label: string; icon: string }>
  if (hasServerEdition.value) base.push({ id: 'teams', label: SERVER_EDITION_TAB_LABEL, icon: '👥' })
  base.push({ id: 'raw', label: 'Raw JSON', icon: '{ }' })
  return base
})

// ---- Raw JSON (Monaco) state, preserved from the previous editor ----
const configJson = ref('')
const validatingConfig = ref(false)
const applyingConfig = ref(false)
const configStatus = ref<{ valid: boolean } | null>(null)
const configErrors = ref<Array<{ field: string; message: string }>>([])
const editorOptions = {
  automaticLayout: true,
  formatOnType: true,
  formatOnPaste: true,
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  fontSize: 14,
  tabSize: 2,
  wordWrap: 'on' as const,
  lineNumbers: 'on' as const,
}

function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v))
}

// Back-compat for the teams -> server_edition rename (MCP-1086): if a config
// only carries the legacy `teams` key, mirror it onto `server_edition` so the
// form (which binds to `server_edition.*`) hydrates. Mutates and returns cfg.
function aliasServerEdition(cfg: any): any {
  if (cfg && cfg.server_edition == null && cfg.teams != null) {
    cfg.server_edition = cfg.teams
  }
  return cfg
}

async function loadConfig() {
  loading.value = true
  loadError.value = ''
  try {
    const response = await api.getConfig()
    if (response.success && response.data) {
      const cfg = response.data.config
      // The server-edition form binds to `server_edition.*` (MCP-1086). The
      // backend loader already normalizes a legacy `teams` key to
      // `server_edition`, but alias it here too so a config still carrying the
      // old key hydrates the form (edits always save under `server_edition`).
      state.working = aliasServerEdition(clone(cfg))
      state.original = aliasServerEdition(clone(cfg))
      configJson.value = JSON.stringify(cfg, null, 2)
      configStatus.value = { valid: true }
      loaded.value = true
    } else {
      loadError.value = response.error || 'Failed to load configuration'
    }
  } catch (e: any) {
    loadError.value = e?.message || 'Failed to load configuration'
  } finally {
    loading.value = false
  }
}

function handleConfigChange() {
  configErrors.value = []
  try {
    JSON.parse(configJson.value)
    configStatus.value = { valid: true }
  } catch {
    configStatus.value = { valid: false }
  }
}

async function validateConfig() {
  validatingConfig.value = true
  configErrors.value = []
  try {
    const cfg = JSON.parse(configJson.value)
    const response = await api.validateConfig(cfg)
    if (response.success && response.data) {
      configErrors.value = response.data.errors || []
      configStatus.value = { valid: response.data.valid }
    } else {
      configErrors.value = [{ field: 'general', message: response.error || 'Validation failed' }]
      configStatus.value = { valid: false }
    }
  } catch (e: any) {
    configErrors.value = [{ field: 'json', message: e?.message || 'Invalid JSON syntax' }]
    configStatus.value = { valid: false }
  } finally {
    validatingConfig.value = false
  }
}

async function applyConfig() {
  applyingConfig.value = true
  configErrors.value = []
  try {
    const cfg = JSON.parse(configJson.value)
    const response = await api.applyConfig(cfg)
    if (response.success && response.data) {
      systemStore.addToast({
        type: response.data.requires_restart ? 'warning' : 'success',
        title: response.data.requires_restart ? 'Applied — restart required' : 'Configuration applied',
      })
      if (response.data.applied_immediately) await serversStore.fetchServers()
      await loadConfig()
    } else {
      configErrors.value = [{ field: 'apply', message: response.error || 'Failed to apply configuration' }]
    }
  } catch (e: any) {
    configErrors.value = [{ field: 'apply', message: e?.message || 'Failed to apply configuration' }]
  } finally {
    applyingConfig.value = false
  }
}

const settingsHints = computed<Hint[]>(() => [
  {
    icon: '🔒',
    title: 'Settings',
    description: 'Manage configuration from friendly forms or raw JSON',
    sections: [
      {
        title: 'Saving',
        text: 'Each section saves only the fields you changed, so secrets like your API key are never overwritten.',
      },
      {
        title: 'Requires restart',
        list: ['Listen address', 'Data directory', 'API key', 'TLS/HTTPS settings'],
      },
    ],
  },
])

function handleConfigSaved() {
  loadConfig()
}

onMounted(() => {
  loadConfig()
  window.addEventListener('mcpproxy:config-saved', handleConfigSaved)
})
onUnmounted(() => {
  window.removeEventListener('mcpproxy:config-saved', handleConfigSaved)
})
</script>
