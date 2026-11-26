<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex flex-wrap justify-between items-center gap-3">
      <div class="min-w-0">
        <h1 class="text-3xl font-bold">Settings</h1>
        <p class="text-base-content/70 mt-1">
          Edit a section, then press {{ SAVE_CHANGES_LABEL }} to apply it. A badge marks fields that need a restart.
          <a
            :href="`${DOCS_BASE}/configuration/config-file`"
            target="_blank"
            rel="noopener noreferrer"
            class="link link-primary"
            data-test="settings-docs-reference"
          >Full configuration reference ↗</a>
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2 w-full sm:w-auto">
        <label class="input input-bordered input-sm flex items-center gap-2 w-full sm:w-64">
          <UiIcon name="search" class="w-4 h-4 opacity-50" />
          <input v-model="search" type="text" class="grow" placeholder="Search all settings…" data-test="settings-search" />
          <button v-if="search" type="button" class="opacity-70 hover:opacity-100" aria-label="Clear search" title="Clear" @click="search = ''">✕</button>
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
        <h2 class="card-title text-lg">Search results <span class="text-sm font-normal text-base-content/60">({{ filteredFields.length }})</span></h2>
        <SettingsSection :key="`sec-${formEpoch}`" section-id="search" :fields="filteredFields" :working="state.working" :original="state.original" :locks="fieldLocks" @saved="onSectionSaved" />
      </div>
    </div>

    <div class="flex flex-col md:flex-row gap-4 md:gap-6 md:items-start">
    <!-- Sections: a list at the side, a select at phone width (FR-050). -->
    <div v-show="!search.trim()" class="md:w-52 md:shrink-0">
      <select
        class="select select-bordered w-full md:hidden min-h-11"
        aria-label="Settings section"
        :value="rovingTabId"
        data-test="settings-tab-select"
        @change="pickTab(($event.target as HTMLSelectElement).value)"
      >
        <option v-for="t in tabs" :key="t.id" :value="t.id">{{ t.label }}</option>
      </select>
      <div role="tablist" aria-orientation="vertical" class="hidden md:flex flex-col gap-1" data-test="settings-tabs">
        <button
          v-for="t in tabs"
          :key="t.id"
          type="button"
          role="tab"
          :aria-selected="activeTab === t.id ? 'true' : 'false'"
          :tabindex="rovingTabId === t.id ? 0 : -1"
          :data-tab-id="t.id"
          class="flex items-center justify-start gap-2 w-full min-h-11 text-left rounded px-3 py-2 text-sm hover:bg-base-200"
          :class="activeTab === t.id ? 'tab-active font-semibold settings-nav-active' : ''"
          :data-test="`settings-tab-${t.id}`"
          @click="pickTab(t.id)"
          @keydown="onSectionKeydown"
        >
          <component :is="t.icon" class="w-4 h-4 shrink-0" />
          {{ t.label }}
        </button>
      </div>
    </div>
    <div class="min-w-0 flex-1 space-y-6">
    <div v-if="loadError" class="alert alert-error">
      <span>{{ loadError }}</span>
    </div>

    <template v-else-if="loaded && !search.trim()">
      <!-- Access and keys (tab id `security`) -->
      <div v-show="activeTab === 'security'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg">Access and keys</h2>
          <p class="text-sm text-base-content/60 mb-2">The settings that most affect how exposed and protected your instance is.</p>
          <!-- connect-a-client helper -->
          <div class="alert bg-base-200 border-base-300 mb-3 flex-col sm:flex-row items-start sm:items-center gap-2">
            <span class="text-sm grow">
              <UiIcon name="plug" class="w-4 h-4 inline" /> Connecting an AI client (Claude, Cursor, VS Code…)? The helper registers mcpproxy with the right endpoint and API key in the client's config.
            </span>
            <button class="btn btn-sm btn-primary" data-test="settings-connect-client" @click="router.push('/clients')">
              Connect a client
            </button>
          </div>
          <!-- posture summary -->
          <div class="flex flex-wrap gap-2 mb-4" data-test="settings-posture">
            <span
              v-for="p in posture"
              :key="p.label"
              class="badge gap-1"
              :class="p.good ? 'badge-success badge-soft' : 'badge-warning badge-soft'"
              :title="p.good ? 'OK' : 'Review this'"
            >
              {{ p.label }}: {{ p.on ? 'on' : 'off' }}
            </span>
          </div>
          <SettingsSection :key="`sec-${formEpoch}`" section-id="security" :fields="securityFields" :working="state.working" :original="state.original" :locks="fieldLocks" @saved="onSectionSaved" />
          <AnonymousProfileSetting :require-mcp-auth="!!state.working.require_mcp_auth" />
          <ScannerSettings />
        </div>
      </div>

      <!-- General -->
      <div v-show="activeTab === 'general'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg">General</h2>
          <SettingsSection :key="`sec-${formEpoch}`" section-id="general" :fields="generalFields" :working="state.working" :original="state.original" :locks="fieldLocks" @saved="onSectionSaved" />
        </div>
      </div>

      <!-- Catalog sources (Spec 109 FR-062, moved from the retired Repositories.vue) -->
      <div v-show="activeTab === 'catalog'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <CatalogSourcesSettings />
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
            <SettingsSection :key="`acc-${acc.id}-${formEpoch}`" :section-id="acc.id" :fields="acc.fields" :working="state.working" :original="state.original" :locks="fieldLocks" @saved="onSectionSaved" />
          </div>
        </details>
        <div class="text-xs text-base-content/50 px-1">
          Complex lists/maps (image map, custom detection patterns, environment vars, registries) and
          <RouterLink to="/" class="link">servers</RouterLink> are managed on their own pages or in Edit as JSON.
        </div>
      </div>

      <!-- Server edition / multi-user settings (server edition only) -->
      <div v-if="hasServerEdition" v-show="activeTab === 'teams'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg" data-test="settings-server-edition-title">{{ serverEditionTitle }}</h2>
          <SettingsSection :key="`sec-${formEpoch}`" section-id="teams" :fields="serverEditionFields" :working="state.working" :original="state.original" :locks="fieldLocks" @saved="onSectionSaved" />
        </div>
      </div>

      <!-- Raw JSON (existing Monaco editor) -->
      <div v-show="activeTab === 'raw'" class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex justify-between items-center mb-2">
            <h2 class="card-title text-lg">Edit as JSON</h2>
            <div v-if="configStatus" :class="['badge', configStatus.valid ? 'badge-success' : 'badge-error']">
              {{ configStatus.valid ? 'Valid' : 'Invalid' }}
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

    </div>
    </div>

    <CollapsibleHintsPanel :hints="settingsHints" />
  </div>
</template>

<script setup lang="ts">
import UiIcon from '@/components/UiIcon.vue'
import { ref, reactive, computed, watch, onMounted, onUnmounted, nextTick, h, type FunctionalComponent } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { VueMonacoEditor } from '@guolao/vue-monaco-editor'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import SettingsSection from '@/components/settings/SettingsSection.vue'
import CatalogSourcesSettings from '@/components/CatalogSourcesSettings.vue'
import ScannerSettings from '@/components/ScannerSettings.vue'
import AnonymousProfileSetting from '@/components/settings/AnonymousProfileSetting.vue'
import {
  SECURITY_FIELDS,
  GENERAL_FIELDS,
  ADVANCED_ACCORDIONS,
  SERVER_EDITION_FIELDS,
  SERVER_EDITION_TAB_LABEL,
  SERVER_EDITION_SECTION_TITLE,
  DOCS_BASE,
  docsUrl,
  isBlankInstructions,
  restartRequiredLabels,
  hydrateConfigState,
  refreshEditionDefaults,
  effectiveBool,
  SAVE_CHANGES_LABEL,
  type SettingField,
  type SettingsAccordion,
} from '@/views/settings/fields'
import api from '@/services/api'
import { useOnboardingStore } from '@/stores/onboarding'
import { lockedKeysChanged, telemetrySettingLock } from '@/utils/telemetryState'

const serversStore = useServersStore()
const systemStore = useSystemStore()
const route = useRoute()
const router = useRouter()

const securityFields = SECURITY_FIELDS
const generalFields = GENERAL_FIELDS
const serverEditionFields = SERVER_EDITION_FIELDS
const serverEditionTitle = SERVER_EDITION_SECTION_TITLE

// Resolved built-in MCP `instructions` default (MCP-2175), fetched from the
// backend so the `instructions` textarea placeholder never drifts from Go.
const defaultInstructions = ref<string>('')

// Inject the live default as the `instructions` field placeholder AND as the
// resetDefault value (drives the "Reset to default" button). Until the fetch
// resolves the static catalogue placeholder is used.
const advancedAccordions = computed<SettingsAccordion[]>(() =>
  ADVANCED_ACCORDIONS.map((acc) => {
    if (!defaultInstructions.value || !acc.fields.some((f) => f.key === 'instructions')) return acc
    return {
      ...acc,
      fields: acc.fields.map((f) =>
        f.key === 'instructions'
          ? { ...f, placeholder: defaultInstructions.value, resetDefault: defaultInstructions.value }
          : f
      ),
    }
  })
)

// Prefill the instructions textarea with the built-in default when the config
// field is empty (fresh install or blank override). Only runs when BOTH the
// config AND the default are loaded; whichever resolves second triggers it.
// Never overwrites a user's saved value (non-empty instructions stay as-is).
function maybePrefillInstructions() {
  if (!defaultInstructions.value) return
  if (!loaded.value) return
  if (isBlankInstructions(state.working.instructions)) {
    state.working.instructions = defaultInstructions.value
  }
}

// ---- form state ----
const loading = ref(false)
const loaded = ref(false)
const loadError = ref('')
const activeTab = ref<string>('general')
// Spec 109 FR-016: read on mount from `?tab=` (onMounted below), then keep the
// URL in sync with every tab change, preserving other query params (e.g.
// `?focus=` from focusField).
watch(activeTab, (tab) => {
  // Review round 2, finding 8: any tab change — a click, focusField, or the
  // late-edition retry below succeeding — means the initial `?tab=` read no
  // longer needs a retry (and, for a manual click, must not be overridden
  // later by that retry).
  tabParamPending = false
  if (route.query.tab === tab) return
  void router.replace({ query: { ...route.query, tab } })
})
const state = reactive<{ working: any; original: any }>({ working: {}, original: {} })
// Bumped on every (re)hydration so each SettingsSection remounts with a fresh
// component-local `dirty` ref. Without this, a field the user edited and then
// abandoned via Reload stays in that ref, and because dirtyKeys is the UNION of
// it and the working/original comparison, Save would PATCH a key whose value
// never actually moved — writing a resolved default into a config that omitted
// the key entirely.
const formEpoch = ref(0)
// Spec 109 FR-056: gated on the RUNTIME edition (`/status`'s `edition` field,
// via the SSE-fed systemStore.status), not on whether the config happens to
// carry a `server_edition` (or legacy `teams`) key. A personal-edition
// instance can still have that key sitting in an imported/stale config, and
// showing a whole tab of settings the running binary cannot act on is worse
// than showing nothing.
const hasServerEdition = computed(() => systemStore.status?.edition === 'server')

// cross-section search: type to find any setting across all tabs
const search = ref('')
const allFields = computed<SettingField[]>(() => [
  ...securityFields,
  ...generalFields,
  ...advancedAccordions.value.flatMap((a) => a.fields),
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
  const quarantine = effectiveBool(w, 'quarantine_enabled') // nil = on, same rule as the toggle
  return [
    { label: 'Quarantine', on: quarantine, good: quarantine },
    { label: 'MCP auth', on: !!w.require_mcp_auth, good: !!w.require_mcp_auth },
    { label: 'Docker isolation', on: !!(w.docker_isolation && w.docker_isolation.enabled), good: !!(w.docker_isolation && w.docker_isolation.enabled) },
    { label: 'Secret scan', on: sdd, good: sdd },
    // on by default since v0.66.0: the sandbox honours quarantine and server
    // restrictions, so an enabled tool is not by itself something to review.
    { label: 'Code exec', on: !!w.enable_code_execution, good: true },
    { label: 'Read-only', on: !!w.read_only_mode, good: true },
    { label: 'Reveal headers', on: !!w.reveal_secret_headers, good: !w.reveal_secret_headers },
  ]
})

// Spec 109 FR-056: line icons from the app's own SVG icon set (same outline
// style as SidebarNav's makeIcon), not emoji.
const tabIconProps = {
  fill: 'none',
  stroke: 'currentColor',
  'stroke-width': 1.6,
  'stroke-linecap': 'round' as const,
  'stroke-linejoin': 'round' as const,
  viewBox: '0 0 24 24',
}
const makeTabIcon = (d: string): FunctionalComponent =>
  (props) => h('svg', { ...tabIconProps, ...props }, [h('path', { d })])

const IconLock = makeTabIcon(
  'M12 11v3m-3-3a3 3 0 116 0m-9 3v6a1 1 0 001 1h10a1 1 0 001-1v-6a1 1 0 00-1-1H6a1 1 0 00-1 1z'
)
const IconGear = makeTabIcon(
  'M10.3 3.6a1.5 1.5 0 013.4 0l.2 1.1a7 7 0 011.9.8l1-.6a1.5 1.5 0 012.1 2.1l-.6 1a7 7 0 01.8 1.9l1.1.2a1.5 1.5 0 010 3.4l-1.1.2a7 7 0 01-.8 1.9l.6 1a1.5 1.5 0 01-2.1 2.1l-1-.6a7 7 0 01-1.9.8l-.2 1.1a1.5 1.5 0 01-3.4 0l-.2-1.1a7 7 0 01-1.9-.8l-1 .6a1.5 1.5 0 01-2.1-2.1l.6-1a7 7 0 01-.8-1.9l-1.1-.2a1.5 1.5 0 010-3.4l1.1-.2a7 7 0 01.8-1.9l-.6-1a1.5 1.5 0 012.1-2.1l1 .6a7 7 0 011.9-.8l.2-1.1zM12 9a3 3 0 100 6 3 3 0 000-6z'
)
const IconWrench = makeTabIcon(
  'M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3-3a1 1 0 000-1.4l-1.6-1.6a1 1 0 00-1.4 0l-1 1L15 3l-5 5-1.3-1.3a1 1 0 00-1.4 0l-3 3a1 1 0 000 1.4L6 13l-3 3a1 1 0 000 1.4l2.6 2.6a1 1 0 001.4 0l3-3a1 1 0 000-1.4L8.7 14l5-5 1.6 1.6z'
)
const IconUsers = makeTabIcon(
  'M17 20v-2a4 4 0 00-3-3.87M9 20v-2a4 4 0 013-3.87m0-4.13a4 4 0 100-8 4 4 0 000 8zm8 4v-2a4 4 0 00-3-3.85m-1-4.15a4 4 0 010 7.75'
)
// Spec 109 FR-062: catalog-source management moved here from the retired
// Repositories.vue.
const IconCatalog = makeTabIcon(
  'M4 6a2 2 0 012-2h3l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H6a2 2 0 01-2-2V6z'
)
const IconBraces = makeTabIcon(
  'M8 4c-2 0-3 1-3 3v3c0 1-1 2-2 2 1 0 2 1 2 2v3c0 2 1 3 3 3m8-16c2 0 3 1 3 3v3c0 1 1 2 2 2-1 0-2 1-2 2v3c0 2-1 3-3 3'
)

// The one section button that is a Tab stop: the selected one, or the first when
// the selection names a section that is not in the list.
const rovingTabId = computed(() => (tabs.value.some((t) => t.id === activeTab.value) ? activeTab.value : tabs.value[0]?.id))

const tabs = computed(() => {
  const base = [
    { id: 'general', label: 'General', icon: IconGear },
    { id: 'security', label: 'Access and keys', icon: IconLock },
    { id: 'catalog', label: 'Catalog sources', icon: IconCatalog },
    { id: 'advanced', label: 'Advanced', icon: IconWrench },
  ] as Array<{ id: string; label: string; icon: FunctionalComponent }>
  if (hasServerEdition.value) base.push({ id: 'teams', label: SERVER_EDITION_TAB_LABEL, icon: IconUsers })
  base.push({ id: 'raw', label: 'Edit as JSON', icon: IconBraces })
  return base
})

// A section change the user did not make by pressing a section button (a late
// `?tab=teams`) must not strand keyboard focus on a button that just lost its Tab stop.
watch(activeTab, () => {
  void nextTick(() => {
    const cur = document.activeElement as HTMLElement | null
    if (cur?.getAttribute('role') === 'tab' && cur.hasAttribute('data-tab-id') && cur.getAttribute('aria-selected') !== 'true') {
      ;(document.querySelector(`[data-test="settings-tab-${activeTab.value}"]`) as HTMLElement | null)?.focus()
    }
  })
})

// Once the edition is known, a selection that names a section this edition lacks
// (a `teams` pick made before status arrived) falls back to General.
watch([tabs, () => systemStore.status?.edition], ([list]) => {
  if (systemStore.status && !list.some((t) => t.id === activeTab.value)) activeTab.value = 'general'
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

// The untouched /config response of the last load; the edition-dependent
// defaults are re-resolved from it if /status arrives after the config did.
let rawConfig: any = null
watch(
  () => systemStore.status?.edition,
  (edition) => {
    if (loaded.value && rawConfig) refreshEditionDefaults(state, rawConfig, { edition })
  }
)

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
      // normalizeFieldDefaults fills the resolved default for `omitempty`
      // keys the API omits (the serialization modes: absent means "full"), so
      // their <select> shows the real default instead of an empty box. Applied
      // to both copies — otherwise the untouched field would read as dirty.
      const hydrated = hydrateConfigState(cfg, { edition: systemStore.status?.edition })
      state.working = hydrated.working
      state.original = hydrated.original
      rawConfig = hydrated.raw
      formEpoch.value++
      // hydrated.raw, not cfg: the Raw tab must show the untouched response,
      // and this makes that dependency explicit rather than relying on the
      // hydration helper's internal cloning.
      configJson.value = JSON.stringify(hydrated.raw, null, 2)
      configStatus.value = { valid: true }
      loaded.value = true
      maybePrefillInstructions()
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
    // FR-044a: the form fields are locked, but this posts the whole document.
    const lockedEdits = lockedKeysChanged(cfg, rawConfig, fieldLocks.value)
    if (lockedEdits.length > 0) {
      configErrors.value = lockedEdits.map((key) => ({
        field: key,
        message: fieldLocks.value[key].reason,
      }))
      return
    }
    const response = await api.applyConfig(cfg)
    if (response.success && response.data) {
      systemStore.addToast({
        type: response.data.requires_restart ? 'warning' : 'success',
        title: response.data.requires_restart ? 'Applied — restart required' : 'Configuration applied',
      })
      if (response.data.applied_immediately) await serversStore.fetchServers()
      await loadConfig()
      // The document may have changed telemetry.enabled; the banner and the
      // lock follow the effective state, like after a section save.
      void onboarding.loadTelemetryState(true)
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
    icon: 'lock',
    title: 'Settings',
    description: 'Manage configuration from friendly forms or raw JSON',
    sections: [
      {
        title: 'Saving',
        text: 'Each section saves only the fields you changed, so secrets like your API key are never overwritten.',
      },
      {
        // UX audit F16: derived from the badges the fields actually carry, not
        // a hand-maintained list that drifts away from them.
        title: 'Requires restart',
        list: restartRequiredLabels(),
      },
    ],
  },
])

function handleConfigSaved() {
  loadConfig()
}

// Deep-link support (MCP-2482): the telemetry consent banner links to
// /settings?focus=telemetry.enabled. Map any field key to the tab that renders
// it, switch to that tab, open the enclosing accordion if it lives under
// Advanced, then scroll to and briefly highlight the field row.
function tabForFieldKey(key: string): string {
  // Spec 108-i: the anonymous-callers control is not a fields.ts entry.
  if (key === 'anonymous_profile') return 'security'
  if (securityFields.some((f) => f.key === key)) return 'security'
  if (generalFields.some((f) => f.key === key)) return 'general'
  if (advancedAccordions.value.some((a) => a.fields.some((f) => f.key === key))) return 'advanced'
  if (serverEditionFields.some((f) => f.key === key)) return 'teams'
  return activeTab.value
}

function isKnownFieldKey(key: string): boolean {
  return key === 'anonymous_profile' || allFields.value.some((f) => f.key === key) || serverEditionFields.some((f) => f.key === key)
}

async function focusField(key: string) {
  // Clear any active search so the tabbed layout (not search results) renders.
  search.value = ''
  // A field of a section this edition does not show (or an unknown key) must not
  // select a section that is not in the list; the tab then stays as it is.
  const resolved = tabForFieldKey(key)
  if (tabs.value.some((t) => t.id === resolved) || (resolved === 'teams' && !systemStore.status)) {
    activeTab.value = resolved
    // The focus link picked the section: a still-pending `?tab=` retry must not undo it.
    if (isKnownFieldKey(key)) tabParamPending = false
  }
  // Name the section in the URL (General, the default, is otherwise never
  // written), so following the same bare focus link again is a real navigation.
  if (route.query.tab === undefined && isKnownFieldKey(key) && tabs.value.some((t) => t.id === activeTab.value)) await router.replace({ query: { ...route.query, tab: activeTab.value } })
  await nextTick()

  // Advanced settings live inside collapsed <details>; open the one holding it.
  const acc = advancedAccordions.value.find((a) => a.fields.some((f) => f.key === key))
  if (acc) {
    const summary = document.querySelector(`[data-test="settings-accordion-${acc.id}"]`)
    const det = summary?.closest('details') as HTMLDetailsElement | null
    if (det) det.open = true
    await nextTick()
  }

  const row = document.querySelector(`[data-test="setting-row-${key}"]`) as HTMLElement | null
  if (!row) return
  row.scrollIntoView({ behavior: 'smooth', block: 'center' })
  const highlight = ['ring-2', 'ring-primary', 'ring-offset-2']
  row.classList.add(...highlight)
  setTimeout(() => row.classList.remove(...highlight), 2500)
}

// Spec 109 FR-044a: an environment opt-out (MCPPROXY_TELEMETRY=false,
// DO_NOT_TRACK, CI) forces telemetry off regardless of the config file. The
// config document stays the stored value (it is a GET-then-POST-back document),
// so the effective state read off /status locks the toggle instead.
const onboarding = useOnboardingStore()
const fieldLocks = computed<Record<string, { reason: string; value?: unknown }>>(() => {
  const locks: Record<string, { reason: string; value?: unknown }> = {}
  const lock = telemetrySettingLock(onboarding.telemetryState)
  if (lock) locks['telemetry.enabled'] = lock
  return locks
})

// After a save that touched telemetry.enabled, refresh the effective state so
// the banner and the lock follow.
function onSectionSaved(changed: string[]) {
  if (changed.includes('telemetry.enabled')) void onboarding.loadTelemetryState(true)
}

// Fetch the resolved built-in MCP instructions default for the textarea
// placeholder (MCP-2175) and the effective telemetry state (FR-044a).
// Non-fatal: a failure or an older core just leaves the static catalogue
// placeholder in place and the telemetry toggle unlocked.
async function loadStatusExtras() {
  try {
    const resp = await api.getStatus()
    if (resp.success) onboarding.setTelemetryState(resp.data?.telemetry ?? null)
    if (resp.success && typeof resp.data?.default_instructions === 'string') {
      defaultInstructions.value = resp.data.default_instructions
      // In case loadConfig already ran and set loaded=true, prefill now.
      maybePrefillInstructions()
    }
  } catch {
    /* leave placeholder as the static fallback */
  }
}

// Also trigger prefill if defaultInstructions resolves after config is loaded
// (handles the common case where /api/v1/status is slower than /api/v1/config).
watch(defaultInstructions, () => {
  maybePrefillInstructions()
})

// Spec 109 FR-016 (review round 2, finding 8): `hasServerEdition` depends on
// systemStore.status, which App.vue populates asynchronously (SSE, its own
// onMounted). On a fresh reload of /settings?tab=teams that status frame can
// still be in flight when this component mounts, so the membership check
// below silently drops `teams` and nothing ever retries once status arrives.
// Re-run the same check once edition resolves, but only while the user
// hasn't since picked a different tab themselves (`tabParamPending` is
// cleared the moment either happens).
let tabParamPending = typeof route.query.tab === 'string'
// A section the user picks wins over a still-pending `?tab=` deep link, even
// when it is the section already showing (the activeTab watcher would not fire).
function pickTab(id: string) {
  tabParamPending = false
  activeTab.value = id
  // Re-picking the section already showing does not fire the activeTab watcher,
  // but a stale `?tab=` must not outlive the choice.
  if (route.query.tab !== id) void router.replace({ query: { ...route.query, tab: id } })
}
// Roving tabindex + Up/Down/Home/End on the vertical section list.
function onSectionKeydown(event: KeyboardEvent) {
  if (event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return // browser/OS shortcuts keep their meaning
  const ids = tabs.value.map((t) => t.id)
  const i = ids.indexOf((event.currentTarget as HTMLElement).getAttribute('data-tab-id') ?? '')
  let next = -1
  if (event.key === 'ArrowDown') next = (i + 1) % ids.length
  else if (event.key === 'ArrowUp') next = (i - 1 + ids.length) % ids.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = ids.length - 1
  if (next < 0) return
  event.preventDefault()
  pickTab(ids[next])
  void nextTick(() => (document.querySelector(`[data-test="settings-tab-${ids[next]}"]`) as HTMLElement | null)?.focus())
}
function applyTabParamIfValid(): boolean {
  const tabParam = route.query.tab
  if (typeof tabParam === 'string' && tabs.value.some((t) => t.id === tabParam)) {
    activeTab.value = tabParam
    return true
  }
  return false
}

// Round-9 fix: both the `?tab=` handling above and the `?focus=` deep link
// below were applied only in onMounted. The header's ModeSwitcher renders on
// every page, including /settings, with a RouterLink to
// `/settings?focus=routing_mode`; clicking it while already on /settings is a
// query-only navigation to the same route component, which Vue Router (and
// App.vue's <router-view>, keyed on the auth epoch rather than the route)
// reuses rather than remounts — onMounted never reruns, so the click did
// nothing visible. Same class of bug this same PR fixed for ServerDetail.vue
// (round-8 finding) via a route-query watcher; extend it here.
watch(
  () => [route.query.tab, route.query.focus] as const,
  ([tab, focus], prev) => {
    const [prevTab, prevFocus] = prev ?? [undefined, undefined]
    if (typeof tab === 'string' && tab !== prevTab) {
      tabParamPending = false
      // An unknown section selects nothing: General, as on a direct load. (While
      // the edition is still unknown `teams` may become valid, so wait.)
      if (!applyTabParamIfValid() && (tab !== 'teams' || systemStore.status)) {
        activeTab.value = 'general'
        // A valid, unchanged focus target is not forgotten with the bad tab.
        if (typeof focus === 'string' && focus) void focusField(focus)
      }
    }
    // A bare /settings (e.g. the sidebar link while on ?tab=security) falls
    // back to General; a focus link picks its own section below.
    if (typeof tab !== 'string' && (typeof prevTab === 'string' || typeof prevFocus === 'string') && !(typeof focus === 'string' && focus && isKnownFieldKey(focus))) {
      activeTab.value = 'general'
    }
    if (typeof focus === 'string' && focus) {
      // zcode review: comparing only `focus !== prevFocus` left a repeat
      // click of the SAME ModeSwitcher link a dead click once the user had
      // manually switched to a different tab in between. ModeSwitcher's
      // RouterLink is a bare `/settings?focus=routing_mode` with no `tab`,
      // so following it always replaces the whole query — `tab` reverting
      // from a real string to undefined is exactly that pattern, distinct
      // from watch(activeTab)'s own `{ ...route.query, tab }` writeback
      // below, which never unsets `tab`. Re-run focusField whenever either
      // the field changed or a bare focus link like that was just followed
      // again, even onto the same field.
      const focusChanged = focus !== prevFocus
      const droppedTabViaBareFocusLink = typeof tab !== 'string' && typeof prevTab === 'string'
      if (focusChanged || droppedTabViaBareFocusLink) void focusField(focus)
    }
  }
)

onMounted(async () => {
  if (applyTabParamIfValid()) tabParamPending = false
  if (tabParamPending) {
    const stopEditionRetry = watch(hasServerEdition, () => {
      stopEditionRetry()
      // The user may have picked a different tab in the meantime — that
      // must win over a now-stale `?tab=` retry.
      if (!tabParamPending) return
      applyTabParamIfValid()
      tabParamPending = false
    })
  }
  loadStatusExtras()
  window.addEventListener('mcpproxy:config-saved', handleConfigSaved)
  await loadConfig()
  const focus = route.query.focus
  if (typeof focus === 'string' && focus) {
    await nextTick()
    await focusField(focus)
  }
})
onUnmounted(() => {
  window.removeEventListener('mcpproxy:config-saved', handleConfigSaved)
})
</script>
