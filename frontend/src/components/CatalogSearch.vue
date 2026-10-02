<template>
  <div data-test="catalog-search">
    <div class="flex gap-2 mb-4">
      <input
        v-model="query"
        type="text"
        placeholder="Search the catalog (e.g. 'github', 'filesystem')…"
        class="input input-bordered flex-1"
        data-test="catalog-search-input"
      />
    </div>

    <div v-if="loading" class="flex items-center gap-2 py-4 text-base-content/70" data-test="catalog-loading">
      <span class="loading loading-spinner loading-sm" /> Searching the catalog…
    </div>

    <div v-else-if="error" class="alert alert-error text-sm" data-test="catalog-error">{{ error }}</div>

    <template v-else>
      <div v-if="addError && !pendingResult" class="alert alert-error text-sm mb-3" data-test="catalog-add-error">{{ addError }}</div>
      <div v-if="unavailable.length > 0" class="alert alert-warning text-sm mb-3" data-test="catalog-unavailable-notice">
        <div>
          <div v-for="line in unavailableLines" :key="line">{{ line }}</div>
        </div>
      </div>

      <template v-if="sections">
        <!-- Popular first when it has entries (popularity if available), then the
             curated Official list (Spec 109 D35, amends Spec 110 FR-005). -->
        <div v-if="sections.popular.length > 0" data-test="catalog-section-popular">
          <h4 class="font-semibold text-sm text-base-content/70 mb-2">Popular</h4>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-3 mb-6">
            <CatalogResultCard
              v-for="r in sections.popular"
              :key="catalogEntryKey(r.source, r.id)"
              :result="r"
              :keyring-available="keyringAvailable"
              :keyring-reason="keyringReason"
              :busy="addingKey === catalogEntryKey(r.source, r.id)"
              @add="handleAdd(r)"
            />
          </div>
        </div>
        <div v-if="sections.official.length > 0" data-test="catalog-section-official">
          <h4 class="font-semibold text-sm text-base-content/70 mb-2">Official</h4>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
            <CatalogResultCard
              v-for="r in sections.official"
              :key="catalogEntryKey(r.source, r.id)"
              :result="r"
              :keyring-available="keyringAvailable"
              :keyring-reason="keyringReason"
              :busy="addingKey === catalogEntryKey(r.source, r.id)"
              @add="handleAdd(r)"
            />
          </div>
        </div>
        <div v-if="sections.official.length === 0 && sections.popular.length === 0" class="text-sm text-base-content/60 py-4">
          Nothing to browse yet.
        </div>
      </template>

      <template v-else>
        <p class="text-sm text-base-content/70 mb-3" data-test="catalog-results-count">
          {{ results.length }} result{{ results.length === 1 ? '' : 's' }}
        </p>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
          <CatalogResultCard
            v-for="r in results"
            :key="catalogEntryKey(r.source, r.id)"
            :result="r"
            :keyring-available="keyringAvailable"
            :keyring-reason="keyringReason"
            :busy="addingKey === catalogEntryKey(r.source, r.id)"
            @add="handleAdd(r)"
          />
        </div>
      </template>
    </template>

    <!-- Secret-inputs panel for an entry with required_inputs (FR-065). -->
    <dialog ref="secretsDialogEl" class="modal" data-test="catalog-secrets-dialog">
      <div class="modal-box">
        <h3 class="font-bold text-lg mb-1">{{ pendingResult?.title }}</h3>
        <p class="text-sm text-base-content/70 mb-4">This server needs a few values before it can run.</p>
        <div class="space-y-3">
          <SecretToggle
            v-for="input in pendingResult?.required_inputs || []"
            :key="input.name"
            :name="input.name"
            kind="env"
            :model-value="pendingValues[input.name] || ''"
            :mode="pendingModes[input.name] || (input.secret_like ? 'secret' : 'value')"
            :keyring-available="keyringAvailable"
            :keyring-reason="keyringReason"
            @update:model-value="(v) => (pendingValues[input.name] = v)"
            @update:mode="(m) => (pendingModes[input.name] = m)"
          />
        </div>
        <div v-if="addError" class="alert alert-error text-sm mt-3" data-test="catalog-add-error">{{ addError }}</div>
        <div class="modal-action">
          <button type="button" class="btn btn-ghost" data-test="catalog-secrets-cancel" @click="closeSecretsDialog">
            Cancel
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="!allPendingValuesFilled || confirming || hasUnavailableSecret"
            data-test="catalog-secrets-confirm"
            @click="confirmAdd"
          >
            Add to MCPProxy
          </button>
        </div>
      </div>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, defineComponent, h } from 'vue'
import { useRouter } from 'vue-router'
import api from '@/services/api'
import type { CatalogResult, CatalogSections, CatalogSourceError } from '@/types'
import SecretToggle from '@/components/SecretToggle.vue'
import { resolveSecretFields, rollbackSecrets } from '@/composables/useSecretFields'
import { useDialogOpen } from '@/composables/useDialogOpen'
import { serverDetailPath } from '@/utils/serverRoute'

const props = defineProps<{ source?: string }>()
const emit = defineEmits<{ added: [name: string] }>()
const router = useRouter()

const query = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const results = ref<CatalogResult[]>([])
const sections = ref<CatalogSections | null>(null)
const unavailable = ref<CatalogSourceError[]>([])

// One line per source that failed. A source that answered from its cached
// listing says so (Spec 109 D35); the others keep the original wording.
const unavailableLines = computed(() =>
  unavailable.value.map((u) =>
    u.fallback === 'cached_listing'
      ? `${u.source}: live search unavailable (${u.reason}); showing matches from its cached list`
      : `${u.source} (${u.reason}) unavailable`
  )
)
const addingKey = ref<string | null>(null)
// FR-063: once added this page-visit, the entry's button flips to "Added ✓ ·
// Open" and stays that way (a fresh search doesn't re-fetch config, so an
// entry the user just added would otherwise flash back to "Add to MCPProxy").
const addedNames = reactive<Record<string, string>>({})

// Catalog sources and their entry IDs may both contain dashes. Use a
// length-prefixed pair rather than `${source}-${id}` so state for distinct
// entries can never collide (for example, `a-b`/`c` and `a`/`b-c`).
function catalogEntryKey(source: string, id: string): string {
  return `${source.length}:${source}${id.length}:${id}`
}

const keyringAvailable = ref(true)
const keyringReason = ref('')

let debounceTimer: ReturnType<typeof setTimeout> | null = null

async function runSearch() {
  loading.value = true
  error.value = null
  try {
    const resp = await api.catalogSearch({ q: query.value, source: props.source })
    if (!resp.success || !resp.data) {
      error.value = resp.error || 'Search failed'
      results.value = []
      sections.value = null
      return
    }
    results.value = resp.data.results
    sections.value = resp.data.sections
    unavailable.value = resp.data.unavailable || []
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Search failed'
  } finally {
    loading.value = false
  }
}

watch([query, () => props.source], () => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(runSearch, 250)
})

onMounted(async () => {
  const secretsResp = await api.getConfigSecrets()
  if (secretsResp.success && secretsResp.data) {
    keyringAvailable.value = secretsResp.data.keyring_available
    keyringReason.value = secretsResp.data.keyring_reason || ''
  }
  await runSearch()
})

// Secrets-prompt dialog state
const pendingResult = ref<CatalogResult | null>(null)
const pendingValues = reactive<Record<string, string>>({})
const pendingModes = reactive<Record<string, 'value' | 'secret'>>({})
const addError = ref<string | null>(null)
const confirming = ref(false)
const { dialogEl: secretsDialogEl } = useDialogOpen(
  () => pendingResult.value !== null,
  () => closeSecretsDialog()
)

const allPendingValuesFilled = computed(() => {
  const inputs = pendingResult.value?.required_inputs || []
  return inputs.every((i) => (pendingValues[i.name] || '').trim() !== '')
})

// Until the user-approved plain-text confirmation flow is implemented, fail
// closed: an unavailable keyring may not turn a Secret selection into raw
// configuration by accident.
const hasUnavailableSecret = computed(() =>
  !keyringAvailable.value && (pendingResult.value?.required_inputs || []).some(
    (input) => (pendingModes[input.name] || (input.secret_like ? 'secret' : 'value')) === 'secret'
  )
)

function handleAdd(result: CatalogResult) {
  const key = catalogEntryKey(result.source, result.id)
  if (result.required_inputs && result.required_inputs.length > 0) {
    pendingResult.value = result
    for (const key of Object.keys(pendingValues)) delete pendingValues[key]
    for (const key of Object.keys(pendingModes)) delete pendingModes[key]
    for (const input of result.required_inputs) {
      pendingModes[input.name] = input.secret_like ? 'secret' : 'value'
    }
    addError.value = null
    return
  }
  void addResult(result, key, {})
}

function closeSecretsDialog() {
  pendingResult.value = null
  addError.value = null
}

async function confirmAdd() {
  if (!pendingResult.value) return
  confirming.value = true
  addError.value = null
  // Tracked outside the try so the catch block (a keyring-write failure
  // inside resolveSecretFields) doesn't attempt a second rollback of refs
  // that resolveSecretFields already rolled back itself; it's only
  // populated once resolveSecretFields has returned successfully.
  let writtenRefs: string[] = []
  try {
    const result = pendingResult.value
    const fields = (result.required_inputs || []).map((i) => ({
      kind: 'env' as const,
      name: i.name,
      value: pendingValues[i.name] || '',
      mode: pendingModes[i.name] || 'value',
    }))
    const resolved = await resolveSecretFields(result.title || result.id, fields)
    writtenRefs = resolved.writtenRefs
    const added = await addResult(result, catalogEntryKey(result.source, result.id), resolved.env)
    if (added) {
      closeSecretsDialog()
    } else if (writtenRefs.length > 0) {
      // The secret write succeeded but the add-server call failed (e.g. a
      // duplicate name) — the secret this attempt just wrote must not be
      // orphaned in the keyring, and a retry must be able to reuse its ref
      // name rather than computing a new -2-suffixed one.
      await rollbackSecrets(writtenRefs)
    }
  } catch (e) {
    addError.value = e instanceof Error ? e.message : 'Failed to add server'
  } finally {
    confirming.value = false
  }
}

// addResult returns whether the add succeeded. api.ts's request() always
// resolves {success:false} rather than throwing, so addResult reports
// failure via its own return value (and addError) instead of throwing —
// callers MUST check the return value rather than assuming a resolved
// promise means success.
async function addResult(result: CatalogResult, key: string, env: Record<string, string>): Promise<boolean> {
  addingKey.value = key
  try {
    const res = await api.addServerFromRegistry(result.source, result.id, { env })
    if (res.success && res.server?.name) {
      addedNames[key] = res.server.name
      emit('added', res.server.name)
      return true
    }
    addError.value = res.error || 'Failed to add server'
    return false
  } finally {
    addingKey.value = null
  }
}

function installTarget(result: CatalogResult): string {
  if (result.install.url) return `url:${result.install.url}`
  return `stdio:${result.install.command || ''}\u0000${(result.install.args || []).join('\u0000')}`
}

function serverTarget(server: { url?: string; command?: string; args?: string[] }): string {
  if (server.url) return `url:${server.url}`
  return `stdio:${server.command || ''}\u0000${(server.args || []).join('\u0000')}`
}

async function openPreviouslyAdded(result: CatalogResult): Promise<void> {
  // GET /servers redacts credential-bearing URL query values and argv, so an
  // exact install-target comparison can fail even though the server-authoritative
  // catalog response already established a unique visible match.
  if (result.added_server_name) {
    await router.push(serverDetailPath(result.added_server_name))
    return
  }
  const response = await api.getServers()
  if (!response.success || !response.data) {
    addError.value = response.error || 'Could not resolve the installed server. Refresh and try again.'
    return
  }
  const target = installTarget(result)
  const matches = response.data.servers.filter((server) =>
    serverTarget(server) === target &&
    (server.source_registry_id === result.source || !server.source_registry_id)
  )
  if (matches.length === 1) {
    await router.push(serverDetailPath(matches[0].name))
    return
  }
  addError.value = matches.length === 0
    ? 'This catalog entry is marked added, but MCPProxy could not identify one visible installed server. Open it from Servers.'
    : 'More than one installed server matches this catalog entry. Open the intended server from Servers.'
}

// formatCount renders a popularity count compactly: 950, 1.2k, 21k, 1.2M.
function formatCount(n: number): string {
  if (n < 1000) return String(n)
  const compact = (v: number, suffix: string) => `${v >= 10 ? Math.round(v) : Math.round(v * 10) / 10}${suffix}`
  if (Math.round(n / 1000) < 1000) return compact(n / 1000, 'k')
  return compact(n / 1_000_000, 'M')
}

// popularityLabel is the card's popularity signal (FR-061): GitHub stars when
// known ("★ 21k"), else source-native installs ("1.2M installs"), else nothing.
function popularityLabel(r: CatalogResult): string {
  const p = r.popularity
  if (!p) return ''
  if (p.stars && p.stars > 0) return `★ ${formatCount(p.stars)}`
  if (p.installs && p.installs > 0) return `${formatCount(p.installs)} installs`
  return ''
}

// CatalogResultCard is a small local functional-ish component (kept in this
// file rather than a separate SFC: it is presentational-only and has no
// reason to be reused outside CatalogSearch).
const CatalogResultCard = defineComponent({
  props: {
    result: { type: Object as () => CatalogResult, required: true },
    keyringAvailable: { type: Boolean, required: true },
    keyringReason: { type: String, default: '' },
    busy: { type: Boolean, default: false },
  },
  emits: ['add'],
  setup(cardProps, { emit: cardEmit }) {
    return () => {
      const r = cardProps.result
      const key = catalogEntryKey(r.source, r.id)
      const addedName = addedNames[key]
      const added = !!addedName || r.added
      return h(
        'div',
        { class: 'card bg-base-100 shadow-md', 'data-test': `catalog-result-${key}` },
        [
          h('div', { class: 'card-body p-4' }, [
            h('div', { class: 'flex items-start justify-between gap-2' }, [
              h('div', { class: 'min-w-0' }, [
                // D19: text-only rendering of arbitrary catalog-source data —
                // no v-html, no markdown. Vue's {{ }} / textContent
                // interpolation already escapes this.
                h('h3', { class: 'font-semibold truncate', 'data-test': 'catalog-result-title' }, r.title),
                h('p', { class: 'text-xs text-base-content/60 font-mono truncate' }, r.id),
                r.publisher || popularityLabel(r)
                  ? h('p', { class: 'text-xs text-base-content/60 mt-0.5 flex gap-2' }, [
                      r.publisher ? h('span', { class: 'truncate', 'data-test': 'catalog-result-publisher' }, `by ${r.publisher}`) : null,
                      popularityLabel(r) ? h('span', { class: 'shrink-0', 'data-test': 'catalog-result-popularity' }, popularityLabel(r)) : null,
                    ])
                  : null,
              ]),
              h('div', { class: 'flex gap-1 shrink-0' }, [
                // Spec 109 D36.6: no per-card "Official" badge. Every default
                // source is official, so it carried no signal; the Official
                // section heading says it once. Verified means the publisher
                // owns the source repository (D36.5).
                r.verified ? h('span', { class: 'badge badge-sm badge-success' }, 'Verified') : null,
                r.from_cache
                  ? h('span', { class: 'badge badge-sm badge-warning badge-outline', title: 'The source\u2019s live search is unavailable; this entry is from its cached list.', 'data-test': `catalog-from-cache-${r.source}-${r.id}` }, 'From cached list')
                  : null,
                h('span', { class: 'badge badge-sm badge-ghost' }, r.transport),
              ]),
            ]),
            r.description ? h('p', { class: 'text-sm text-base-content/70 mt-1 line-clamp-2' }, r.description) : null,
            h('div', { class: 'card-actions justify-end mt-2' }, [
              h(
                'button',
                {
                  type: 'button',
                  class: `btn btn-sm ${added ? 'btn-success' : 'btn-primary'}`,
                  disabled: cardProps.busy,
                  'data-test': `catalog-add-${key}`,
                  onClick: () => {
                    if (!added) return cardEmit('add')
                    if (addedName) return router.push(serverDetailPath(addedName))
                    void openPreviouslyAdded(r)
                  },
                },
                added ? 'Added ✓ · Open' : cardProps.busy ? 'Adding…' : 'Add to MCPProxy'
              ),
            ]),
          ]),
        ]
      )
    }
  },
})
</script>
