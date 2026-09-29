<template>
  <div data-test="catalog-sources-settings">
    <div class="flex justify-between items-center mb-3">
      <div>
        <h2 class="card-title text-lg">Catalog sources</h2>
        <p class="text-sm text-base-content/70">
          Sources the Catalog tab on <router-link to="/add-server" class="link">Add Server</router-link> searches.
          Custom sources you add can be edited or removed.
        </p>
      </div>
      <button type="button" class="btn btn-outline btn-sm" data-test="registry-add-source-button" @click="openAddRegistry">
        <svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
        Add Registry
      </button>
    </div>

    <div v-if="loadingRegistries" class="flex items-center gap-2 py-4 text-base-content/70" data-test="registries-loading">
      <span class="loading loading-spinner loading-sm"></span>
      <span>Loading registries…</span>
    </div>

    <div v-else-if="registries.length === 0" class="text-sm text-base-content/60 py-4" data-test="registries-empty">
      No registries configured.
    </div>

    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
      <div
        v-for="registry in registries"
        :key="registry.id"
        class="border rounded-lg p-3 flex flex-col gap-2 border-base-300"
        :data-test="`registry-card-${registry.id}`"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="font-semibold min-w-0 [overflow-wrap:anywhere]">{{ registry.name }}</div>
          <div v-if="isCustomRegistry(registry)" class="dropdown dropdown-end shrink-0">
            <div
              tabindex="0"
              role="button"
              class="btn btn-ghost btn-xs btn-square"
              :data-test="`registry-kebab-${registry.id}`"
              :aria-label="`Manage ${registry.name}`"
            >
              <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                <path d="M12 8a2 2 0 100-4 2 2 0 000 4zm0 2a2 2 0 100 4 2 2 0 000-4zm0 6a2 2 0 100 4 2 2 0 000-4z" />
              </svg>
            </div>
            <ul tabindex="0" class="dropdown-content menu bg-base-100 rounded-box z-10 w-32 p-1 shadow-lg border border-base-300">
              <li>
                <button type="button" :data-test="`registry-edit-${registry.id}`" @click="openEditRegistry(registry)">Edit</button>
              </li>
              <li>
                <button type="button" class="text-error" :data-test="`registry-delete-${registry.id}`" @click="openDeleteRegistry(registry)">Delete</button>
              </li>
            </ul>
          </div>
        </div>

        <div class="flex flex-wrap gap-1 items-center">
          <span
            class="badge badge-sm"
            :class="isCustomRegistry(registry) ? 'badge-ghost' : 'badge-outline'"
            :data-test="`registry-provenance-${registry.id}`"
          >{{ isCustomRegistry(registry) ? 'Custom' : 'Official' }}</span>
          <span v-if="!isCustomRegistry(registry)" class="badge badge-sm badge-ghost" :data-test="`registry-builtin-${registry.id}`">
            Built-in
          </span>
        </div>

        <div v-if="registry.url" class="text-xs text-base-content/60 truncate" :title="registry.url">{{ registry.url }}</div>
      </div>
    </div>

    <!-- Add / edit registry-source dialog (MCP-866 add, MCP-1073 edit) -->
    <dialog ref="addRegistryDialogEl" class="modal" data-test="registry-add-source-dialog">
      <div class="modal-box">
        <h3 class="font-bold text-lg">{{ isEditMode ? 'Edit registry' : 'Add a registry' }}</h3>
        <p class="text-sm text-base-content/70 mt-1">
          <template v-if="isEditMode">
            Update this custom <code>modelcontextprotocol/registry</code> source. Its id is fixed.
          </template>
          <template v-else>
            Add a custom <code>modelcontextprotocol/registry</code> v0.1 source by its HTTPS URL —
            that is the only registry protocol MCPProxy speaks, so the URL is checked when you add it
            and rejected here if it turns out not to be one. It is shown as a
            <span class="badge badge-ghost badge-xs align-middle">Custom</span> source.
          </template>
        </p>

        <form @submit.prevent="submitRegistryDialog" class="mt-4 space-y-3" data-test="registry-add-form">
          <div v-if="isEditMode" class="form-control">
            <label class="label"><span class="label-text font-semibold">Registry ID</span></label>
            <input :value="editRegistryId" type="text" data-test="registry-edit-id" class="input input-bordered w-full opacity-70" readonly />
          </div>

          <div class="form-control">
            <label class="label"><span class="label-text font-semibold">Registry URL</span></label>
            <input
              v-model="addRegistryUrl"
              type="url"
              placeholder="https://registry.example.com/"
              data-test="registry-add-url-input"
              class="input input-bordered w-full"
              autocomplete="off"
              required
            />
          </div>

          <div v-if="!isEditMode" class="form-control">
            <label class="label"><span class="label-text font-semibold">Protocol</span></label>
            <select v-model="addRegistryProtocol" class="select select-bordered w-full" data-test="registry-add-protocol-select">
              <option value="modelcontextprotocol/registry">modelcontextprotocol/registry (default)</option>
            </select>
          </div>

          <div class="form-control">
            <label class="label"><span class="label-text font-semibold">Name <span class="font-normal opacity-60">(optional)</span></span></label>
            <input
              v-model="addRegistryName"
              type="text"
              placeholder="Derived from the URL host when empty"
              data-test="registry-add-name-input"
              class="input input-bordered w-full"
              autocomplete="off"
            />
          </div>

          <div v-if="addRegistryError" class="alert alert-error text-sm" data-test="registry-add-error">
            <span>{{ addRegistryError }}</span>
          </div>

          <div class="modal-action">
            <button type="button" class="btn btn-ghost" data-test="registry-add-cancel" :disabled="addingRegistry" @click="closeAddRegistry">
              Cancel
            </button>
            <button type="submit" class="btn btn-primary" data-test="registry-add-submit" :disabled="!addRegistryUrl.trim() || addingRegistry">
              <span v-if="addingRegistry" class="loading loading-spinner loading-xs"></span>
              <span v-else>{{ isEditMode ? 'Save changes' : 'Add Registry' }}</span>
            </button>
          </div>
        </form>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button @click="closeAddRegistry">close</button>
      </form>
    </dialog>

    <!-- Delete custom registry confirmation (MCP-1073, destructive) -->
    <dialog ref="deleteRegistryDialogEl" class="modal" data-test="registry-delete-dialog">
      <div class="modal-box">
        <h3 class="font-bold text-lg">Remove "{{ deleteRegistryTarget?.name }}"?</h3>
        <p class="text-sm py-2 text-base-content/80">Servers you already added stay; only the source is removed.</p>

        <div v-if="deleteRegistryError" class="alert alert-error text-sm" data-test="registry-delete-error">
          <span>{{ deleteRegistryError }}</span>
        </div>

        <div class="modal-action">
          <button type="button" class="btn btn-ghost" data-test="registry-delete-cancel" :disabled="deletingRegistry" @click="closeDeleteRegistry">
            Cancel
          </button>
          <button type="button" class="btn btn-error" data-test="registry-delete-confirm" :disabled="deletingRegistry" @click="confirmDeleteRegistry">
            <span v-if="deletingRegistry" class="loading loading-spinner loading-xs"></span>
            <span v-else>Remove</span>
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button @click="closeDeleteRegistry">close</button>
      </form>
    </dialog>

    <div v-if="showSuccessToast" class="toast toast-end" data-test="registry-add-success">
      <div class="alert alert-success">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>{{ successMessage }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// Catalog-source management (Spec 109 FR-062: moved out of the retired
// Repositories.vue into Settings). Server BROWSING moved to
// components/CatalogSearch.vue (the Add Server page's Catalog tab); this
// component only manages the SOURCES that browse searches.
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'
import type { Registry } from '@/types'
import { REGISTRY_PROVENANCE_CUSTOM } from '@/types'
import { useDialogOpen } from '@/composables/useDialogOpen'

const registries = ref<Registry[]>([])
const loadingRegistries = ref(false)
const error = ref<string | null>(null)
const showSuccessToast = ref(false)
const successMessage = ref('')

const showAddRegistry = ref(false)
const editRegistryId = ref<string | null>(null)
const addRegistryUrl = ref('')
const addRegistryProtocol = ref('modelcontextprotocol/registry')
const addRegistryName = ref('')
const addRegistryError = ref<string | null>(null)
const addingRegistry = ref(false)
const isEditMode = computed(() => editRegistryId.value !== null)
const { dialogEl: addRegistryDialogEl } = useDialogOpen(() => showAddRegistry.value, () => handleAddRegistryNativeClose())

const showDeleteRegistry = ref(false)
const deleteRegistryTarget = ref<Registry | null>(null)
const deleteRegistryError = ref<string | null>(null)
const deletingRegistry = ref(false)
const { dialogEl: deleteRegistryDialogEl } = useDialogOpen(() => showDeleteRegistry.value, () => handleDeleteRegistryNativeClose())

function isCustomRegistry(registry?: Registry | null): boolean {
  if (!registry) return false
  return registry.provenance === REGISTRY_PROVENANCE_CUSTOM || registry.trusted === false
}

async function loadRegistries() {
  loadingRegistries.value = true
  error.value = null
  try {
    const response = await api.listRegistries()
    if (response.success && response.data) {
      registries.value = response.data.registries
    } else {
      error.value = response.error || 'Failed to load registries'
    }
  } catch (err) {
    error.value = 'Failed to load registries: ' + (err as Error).message
  } finally {
    loadingRegistries.value = false
  }
}

function openAddRegistry() {
  editRegistryId.value = null
  addRegistryUrl.value = ''
  addRegistryProtocol.value = 'modelcontextprotocol/registry'
  addRegistryName.value = ''
  addRegistryError.value = null
  showAddRegistry.value = true
}

function openEditRegistry(registry: Registry) {
  editRegistryId.value = registry.id
  addRegistryUrl.value = registry.url || registry.servers_url || ''
  addRegistryName.value = registry.name || ''
  addRegistryError.value = null
  showAddRegistry.value = true
}

function closeAddRegistry() {
  if (addingRegistry.value) return
  showAddRegistry.value = false
  editRegistryId.value = null
}

function handleAddRegistryNativeClose() {
  showAddRegistry.value = false
  editRegistryId.value = null
}

function registryErrorMessage(code: string | undefined, fallback: string | undefined, verb: string): string {
  switch (code) {
    case 'invalid_registry_url':
      return fallback || 'That URL is not a valid HTTPS registry endpoint.'
    case 'registries_locked':
      return `${verb} registries is locked by an administrator on this instance.`
    case 'registry_shadows_builtin':
      return 'That id/host collides with a built-in registry.'
    case 'registry_not_found':
      return 'That registry no longer exists. It may have already been removed.'
    case 'duplicate_registry':
      return 'A registry with that id is already configured.'
    default:
      return fallback || `Failed to ${verb.toLowerCase()} registry.`
  }
}

function submitRegistryDialog() {
  if (!addRegistryUrl.value.trim() || addingRegistry.value) return
  if (isEditMode.value) void doEditRegistry()
  else void doAddRegistry()
}

async function doAddRegistry() {
  addingRegistry.value = true
  addRegistryError.value = null
  try {
    const result = await api.addRegistrySource(addRegistryUrl.value.trim(), {
      protocol: addRegistryProtocol.value || undefined,
      name: addRegistryName.value.trim() || undefined,
    })
    if (result.success) {
      const added = result.registry
      showAddRegistry.value = false
      await loadRegistries()
      showToast(`Added registry "${added?.name || added?.id || addRegistryUrl.value}".`)
      return
    }
    addRegistryError.value = registryErrorMessage(result.code, result.error, 'Adding')
  } catch (err) {
    addRegistryError.value = 'Failed to add registry: ' + (err as Error).message
  } finally {
    addingRegistry.value = false
  }
}

async function doEditRegistry() {
  const id = editRegistryId.value
  if (!id) return
  addingRegistry.value = true
  addRegistryError.value = null
  try {
    const result = await api.editRegistrySource(id, {
      name: addRegistryName.value.trim() || undefined,
      url: addRegistryUrl.value.trim() || undefined,
    })
    if (result.success) {
      showAddRegistry.value = false
      editRegistryId.value = null
      await loadRegistries()
      const updated = result.registry
      showToast(`Updated registry "${updated?.name || updated?.id || id}".`)
      return
    }
    addRegistryError.value = registryErrorMessage(result.code, result.error, 'Editing')
  } catch (err) {
    addRegistryError.value = 'Failed to edit registry: ' + (err as Error).message
  } finally {
    addingRegistry.value = false
  }
}

function openDeleteRegistry(registry: Registry) {
  deleteRegistryTarget.value = registry
  deleteRegistryError.value = null
  showDeleteRegistry.value = true
}

function closeDeleteRegistry() {
  if (deletingRegistry.value) return
  showDeleteRegistry.value = false
  deleteRegistryTarget.value = null
}

function handleDeleteRegistryNativeClose() {
  showDeleteRegistry.value = false
  deleteRegistryTarget.value = null
}

async function confirmDeleteRegistry() {
  const target = deleteRegistryTarget.value
  if (!target) return
  deletingRegistry.value = true
  deleteRegistryError.value = null
  try {
    const result = await api.removeRegistrySource(target.id)
    if (result.success) {
      showDeleteRegistry.value = false
      deleteRegistryTarget.value = null
      await loadRegistries()
      showToast(`Removed registry "${target.name || target.id}".`)
      return
    }
    deleteRegistryError.value = registryErrorMessage(result.code, result.error, 'Removing')
  } catch (err) {
    deleteRegistryError.value = 'Failed to remove registry: ' + (err as Error).message
  } finally {
    deletingRegistry.value = false
  }
}

function showToast(message: string) {
  successMessage.value = message
  showSuccessToast.value = true
  setTimeout(() => {
    showSuccessToast.value = false
  }, 3000)
}

onMounted(() => {
  void loadRegistries()
})
</script>
