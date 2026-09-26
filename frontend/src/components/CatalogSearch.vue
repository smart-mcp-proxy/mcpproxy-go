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
      <div v-if="unavailable.length > 0" class="alert alert-warning text-sm mb-3" data-test="catalog-unavailable-notice">
        <span>{{ unavailable.map((u) => `${u.source} (${u.reason})`).join(', ') }} unavailable</span>
      </div>

      <template v-if="sections">
        <div v-if="sections.official.length > 0" data-test="catalog-section-official">
          <h4 class="font-semibold text-sm text-base-content/70 mb-2">Official</h4>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-3 mb-6">
            <CatalogResultCard
              v-for="r in sections.official"
              :key="`${r.source}-${r.id}`"
              :result="r"
              :keyring-available="keyringAvailable"
              :keyring-reason="keyringReason"
              :busy="addingKey === `${r.source}-${r.id}`"
              @add="handleAdd(r)"
            />
          </div>
        </div>
        <div v-if="sections.popular.length > 0" data-test="catalog-section-popular">
          <h4 class="font-semibold text-sm text-base-content/70 mb-2">Popular</h4>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
            <CatalogResultCard
              v-for="r in sections.popular"
              :key="`${r.source}-${r.id}`"
              :result="r"
              :keyring-available="keyringAvailable"
              :keyring-reason="keyringReason"
              :busy="addingKey === `${r.source}-${r.id}`"
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
            :key="`${r.source}-${r.id}`"
            :result="r"
            :keyring-available="keyringAvailable"
            :keyring-reason="keyringReason"
            :busy="addingKey === `${r.source}-${r.id}`"
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
            :disabled="!allPendingValuesFilled || confirming"
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
import type { CatalogResult, CatalogSections } from '@/types'
import SecretToggle from '@/components/SecretToggle.vue'
import { resolveSecretFields, rollbackSecrets } from '@/composables/useSecretFields'
import { useDialogOpen } from '@/composables/useDialogOpen'
import { serverDetailPath } from '@/utils/serverRoute'

const props = defineProps<{ source?: string }>()
const emit = defineEmits<{ added: [name: string] }>()

const query = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const results = ref<CatalogResult[]>([])
const sections = ref<CatalogSections | null>(null)
const unavailable = ref<{ source: string; reason: string }[]>([])
const addingKey = ref<string | null>(null)
// FR-063: once added this page-visit, the entry's button flips to "Added ✓ ·
// Open" and stays that way (a fresh search doesn't re-fetch config, so an
// entry the user just added would otherwise flash back to "Add to MCPProxy").
const addedNames = reactive<Record<string, string>>({})

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

function handleAdd(result: CatalogResult) {
  const key = `${result.source}-${result.id}`
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
    const added = await addResult(result, `${result.source}-${result.id}`, resolved.env)
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
    const router = useRouter()
    return () => {
      const r = cardProps.result
      const key = `${r.source}-${r.id}`
      const addedName = addedNames[key]
      const added = !!addedName || r.added
      return h(
        'div',
        { class: 'card bg-base-100 shadow-md', 'data-test': `catalog-result-${r.source}-${r.id}` },
        [
          h('div', { class: 'card-body p-4' }, [
            h('div', { class: 'flex items-start justify-between gap-2' }, [
              h('div', { class: 'min-w-0' }, [
                // D19: text-only rendering of arbitrary catalog-source data —
                // no v-html, no markdown. Vue's {{ }} / textContent
                // interpolation already escapes this.
                h('h3', { class: 'font-semibold truncate', 'data-test': 'catalog-result-title' }, r.title),
                h('p', { class: 'text-xs text-base-content/60 font-mono truncate' }, r.id),
              ]),
              h('div', { class: 'flex gap-1 shrink-0' }, [
                r.official ? h('span', { class: 'badge badge-sm badge-primary' }, 'Official') : null,
                r.verified && !r.official ? h('span', { class: 'badge badge-sm badge-success' }, 'Verified') : null,
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
                  'data-test': `catalog-add-${r.source}-${r.id}`,
                  onClick: () => (added && addedName ? router.push(serverDetailPath(addedName)) : cardEmit('add')),
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
