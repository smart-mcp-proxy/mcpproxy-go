<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Repositories</h1>
        <p class="text-base-content/70 mt-1">Browse and discover MCP server repositories</p>
      </div>
      <button
        @click="openAddRegistry"
        class="btn btn-outline btn-sm"
        data-test="registry-add-source-button"
      >
        <svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
        Add Registry
      </button>
    </div>

    <!-- Configured registries (manage sources) — MCP-1073 -->
    <div class="card bg-base-100 shadow-md" data-test="registries-manage-section">
      <div class="card-body">
        <div>
          <h2 class="card-title text-lg">Registries</h2>
          <p class="text-sm text-base-content/70">Sources MCPProxy browses for MCP servers. Custom sources you add can be edited or removed.</p>
        </div>

        <!-- Loading -->
        <div v-if="loadingRegistries" class="flex items-center gap-2 py-4 text-base-content/70" data-test="registries-loading">
          <span class="loading loading-spinner loading-sm"></span>
          <span>Loading registries…</span>
        </div>

        <!-- Empty (rare — defaults are always present) -->
        <div v-else-if="registries.length === 0" class="text-sm text-base-content/60 py-4" data-test="registries-empty">
          No registries configured.
        </div>

        <!-- Cards grid -->
        <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3 mt-2">
          <div
            v-for="registry in registries"
            :key="registry.id"
            class="border border-base-300 rounded-lg p-3 flex flex-col gap-2"
            :data-test="`registry-card-${registry.id}`"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="font-semibold min-w-0 [overflow-wrap:anywhere]">{{ registry.name }}</div>

              <!-- Kebab (⋮) menu — custom registries only; official is read-only -->
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

            <!-- Neutral provenance badge (Official / Custom) + Built-in tag -->
            <div class="flex flex-wrap gap-1 items-center">
              <span
                class="badge badge-sm"
                :class="isCustomRegistry(registry) ? 'badge-ghost' : 'badge-outline'"
                :data-test="`registry-provenance-${registry.id}`"
              >{{ isCustomRegistry(registry) ? 'Custom' : 'Official' }}</span>
              <span
                v-if="!isCustomRegistry(registry)"
                class="badge badge-sm badge-ghost"
                :data-test="`registry-builtin-${registry.id}`"
              >Built-in</span>
            </div>

            <div v-if="registry.url" class="text-xs text-base-content/60 truncate" :title="registry.url">{{ registry.url }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- Registry Selector & Search -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex flex-col sm:flex-row gap-4">
          <!-- Registry multiselect filter (R1): search across one or more registries at once -->
          <div class="form-control flex-1">
            <label class="label">
              <span class="label-text font-semibold">Registries</span>
            </label>
            <div class="dropdown" data-test="registry-multiselect">
              <div
                tabindex="0"
                role="button"
                class="select select-bordered w-full flex items-center"
                :class="{ 'opacity-60 pointer-events-none': loadingRegistries }"
                data-test="registry-multiselect-trigger"
              >
                <span class="truncate">{{ registrySelectLabel }}</span>
              </div>
              <ul
                tabindex="0"
                class="dropdown-content menu bg-base-100 rounded-box z-10 w-full p-2 shadow-lg max-h-80 overflow-y-auto flex-nowrap mt-1 border border-base-300"
                data-test="registry-multiselect-menu"
              >
                <li v-if="registries.length > 1" class="menu-title px-2 pb-1 flex flex-row gap-3">
                  <button type="button" class="link link-primary text-xs" data-test="registry-select-all" @click="selectAllRegistries">All</button>
                  <button type="button" class="link text-xs" data-test="registry-clear-all" @click="clearRegistries">Clear</button>
                </li>
                <li v-for="registry in registries" :key="registry.id">
                  <label class="label cursor-pointer justify-start gap-3 py-2">
                    <input
                      type="checkbox"
                      class="checkbox checkbox-sm"
                      :checked="selectedRegistries.includes(registry.id)"
                      @change="toggleRegistry(registry.id)"
                      :data-test="`registry-option-${registry.id}`"
                    />
                    <span class="text-sm">{{ registry.name }}<span v-if="isCustomRegistry(registry)" class="opacity-60"> — custom</span></span>
                  </label>
                </li>
              </ul>
            </div>
          </div>

          <!-- Search Input -->
          <div class="form-control flex-1">
            <label class="label">
              <span class="label-text font-semibold">Search Servers</span>
            </label>
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search by name or description..."
              class="input input-bordered w-full"
              data-test="registry-search-input"
              @input="handleSearchInput"
              :disabled="selectedRegistries.length === 0 || loadingServers"
            />
          </div>

          <!-- Transport Filter (R3) -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Transport</span>
            </label>
            <select
              v-model="transportFilter"
              class="select select-bordered"
              data-test="registry-transport-filter"
            >
              <option value="all">All</option>
              <option value="remote">Remote</option>
              <option value="stdio">Stdio</option>
            </select>
          </div>

          <!-- Search Button -->
          <div class="form-control sm:self-end">
            <button
              @click="searchServers"
              class="btn btn-primary"
              data-test="registry-search-button"
              :disabled="selectedRegistries.length === 0 || loadingServers"
            >
              <span v-if="loadingServers" class="loading loading-spinner loading-sm"></span>
              <span v-else>Search</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loadingServers" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex flex-col items-center justify-center py-12">
          <div class="loading loading-spinner loading-lg mb-4"></div>
          <p class="text-base-content/70">Searching servers...</p>
        </div>
      </div>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
    </div>

    <!-- Server Results -->
    <div v-else-if="servers.length > 0" class="space-y-4">
      <!-- Non-fatal: some selected registries returned nothing (e.g. need a key) -->
      <div
        v-if="unavailableRegistries.length > 0"
        class="alert alert-warning py-2 text-sm"
        data-test="registry-unavailable-notice"
      >
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>Some registries returned no results: {{ unavailableRegistries.join('; ') }}</span>
      </div>

      <div class="flex justify-between items-center">
        <p class="text-sm text-base-content/70" data-test="registry-results-count">
          Found {{ filteredServers.length }} server(s)<span v-if="transportFilter !== 'all'"> of {{ servers.length }}</span>
          <span v-if="selectedRegistries.length > 1"> across {{ selectedRegistries.length }} registries</span>
        </p>
      </div>

      <!-- Server Cards with Smooth Transitions -->
      <TransitionGroup
        name="repo-card"
        tag="div"
        class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4"
      >
        <div v-for="server in filteredServers" :key="`${server.registry}-${server.id}`" :data-test="`registry-server-${server.id}`" class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow">
          <div class="card-body">
            <div class="flex justify-between items-start gap-2">
              <h3 class="card-title text-lg min-w-0 [overflow-wrap:anywhere]">{{ server.name }}</h3>
              <div
                v-if="server.registry"
                class="badge badge-ghost badge-sm shrink-0 whitespace-nowrap font-normal"
                :data-test="`registry-source-${server.id}`"
                :title="`From registry: ${server.registry}`"
              >
                {{ server.registry }}
              </div>
            </div>

            <p class="text-sm text-base-content/70 line-clamp-3">
              {{ server.description }}
            </p>

            <!-- Transport + requirements (neutral, non-colorful tags — R2) -->
            <div class="flex flex-wrap gap-2 mt-2">
              <div
                class="badge badge-outline badge-sm font-mono"
                :data-test="`registry-transport-${server.id}`"
              >
                {{ serverTransport(server) }}
              </div>
              <div
                v-if="server.required_inputs && server.required_inputs.length > 0"
                class="badge badge-outline badge-sm"
                :data-test="`registry-requires-input-${server.id}`"
                :title="`Requires: ${server.required_inputs.map(i => i.name).join(', ')}`"
              >
                requires input
              </div>
            </div>

            <!-- Install Command -->
            <div v-if="server.install_cmd" class="mt-3">
              <div class="flex items-center justify-between bg-base-200 rounded px-2 py-1">
                <code class="text-xs flex-1 overflow-x-auto">{{ server.install_cmd }}</code>
                <button
                  @click="copyToClipboard(server.install_cmd)"
                  class="btn btn-ghost btn-xs ml-2"
                  title="Copy install command"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </button>
              </div>
            </div>

            <!-- Actions -->
            <div class="card-actions justify-end mt-4">
              <button
                v-if="server.source_code_url"
                @click="openURL(server.source_code_url)"
                class="btn btn-ghost btn-sm"
              >
                <svg class="w-4 h-4 mr-1" fill="currentColor" viewBox="0 0 24 24">
                  <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/>
                </svg>
                Source
              </button>
              <button
                @click="addServer(server)"
                class="btn btn-primary btn-sm"
                :data-test="`registry-add-${server.id}`"
                :disabled="addingServerId === server.id"
              >
                <span v-if="addingServerId === server.id" class="loading loading-spinner loading-xs"></span>
                <span v-else>Add to MCP</span>
              </button>
            </div>
          </div>
        </div>
      </TransitionGroup>
    </div>

    <!-- Empty State (no search yet) -->
    <div v-else-if="selectedRegistries.length === 0" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="text-center py-12">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
          </svg>
          <h3 class="text-xl font-semibold mb-2">Select a Registry</h3>
          <p class="text-base-content/70">Choose a registry from the dropdown to start browsing MCP servers.</p>
        </div>
      </div>
    </div>

    <!-- Empty State (no results) -->
    <div v-else class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="text-center py-12">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.172 16.172a4 4 0 015.656 0M9 10h.01M15 10h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <h3 class="text-xl font-semibold mb-2">No Servers Found</h3>
          <p class="text-base-content/70">Try adjusting your search query or select a different registry.</p>
        </div>
      </div>
    </div>

    <!-- Required-Input Prompt (Spec 070 — blocks add until provided) -->
    <dialog :open="showPrompt" class="modal" data-test="registry-required-input-dialog">
      <div class="modal-box">
        <h3 class="font-bold text-lg">Add "{{ promptServer?.name }}"</h3>
        <p class="text-sm text-base-content/70 mt-1">
          This server needs the following before it can be added. Values are stored as
          environment variables on the (quarantined) server.
        </p>

        <form @submit.prevent="submitPrompt" class="mt-4 space-y-3">
          <div v-for="input in promptInputs" :key="input.name" class="form-control">
            <label class="label">
              <span class="label-text font-semibold">{{ input.name }}</span>
            </label>
            <input
              v-model="promptValues[input.name]"
              :type="input.secret ? 'password' : 'text'"
              :placeholder="input.description || input.name"
              :data-test="`registry-input-${input.name}`"
              class="input input-bordered w-full"
              autocomplete="off"
            />
            <label v-if="input.description" class="label">
              <span class="label-text-alt text-base-content/60">{{ input.description }}</span>
            </label>
          </div>

          <div v-if="error" class="alert alert-error text-sm" data-test="registry-input-error">
            <span>{{ error }}</span>
          </div>

          <div class="modal-action">
            <button type="button" class="btn btn-ghost" data-test="registry-input-cancel" @click="closePrompt">
              Cancel
            </button>
            <button
              type="submit"
              class="btn btn-primary"
              data-test="registry-input-submit"
              :disabled="!promptComplete || addingServerId !== null"
            >
              <span v-if="addingServerId !== null" class="loading loading-spinner loading-xs"></span>
              <span v-else>Add to MCP</span>
            </button>
          </div>
        </form>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button @click="closePrompt">close</button>
      </form>
    </dialog>

    <!-- Add / Edit Registry Source dialog (MCP-866 add, MCP-1073 edit) -->
    <dialog :open="showAddRegistry" class="modal" data-test="registry-add-source-dialog">
      <div class="modal-box">
        <h3 class="font-bold text-lg">{{ isEditMode ? 'Edit registry' : 'Add a registry' }}</h3>
        <p class="text-sm text-base-content/70 mt-1">
          <template v-if="isEditMode">
            Update this custom <code>modelcontextprotocol/registry</code> source. Its id is fixed.
          </template>
          <template v-else>
            Add a custom <code>modelcontextprotocol/registry</code> v0.1 source by its HTTPS URL —
            that is the only registry protocol MCPProxy speaks, so the URL is checked when you add it
            and rejected here if it turns out not to be one (a plain JSON catalog or a web page cannot be browsed).
            It is shown as a <span class="badge badge-ghost badge-xs align-middle">Custom</span> source.
          </template>
        </p>

        <form @submit.prevent="submitRegistryDialog" class="mt-4 space-y-3" data-test="registry-add-form">
          <!-- Read-only id (edit mode only) -->
          <div v-if="isEditMode" class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Registry ID</span>
            </label>
            <input
              :value="editRegistryId"
              type="text"
              data-test="registry-edit-id"
              class="input input-bordered w-full opacity-70"
              readonly
            />
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Registry URL</span>
            </label>
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
            <label class="label">
              <span class="label-text font-semibold">Protocol</span>
            </label>
            <select
              v-model="addRegistryProtocol"
              class="select select-bordered w-full"
              data-test="registry-add-protocol-select"
            >
              <option value="modelcontextprotocol/registry">modelcontextprotocol/registry (default)</option>
            </select>
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Name <span class="font-normal opacity-60">(optional)</span></span>
            </label>
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
            <button type="button" class="btn btn-ghost" data-test="registry-add-cancel" @click="closeAddRegistry">
              Cancel
            </button>
            <button
              type="submit"
              class="btn btn-primary"
              data-test="registry-add-submit"
              :disabled="!addRegistryUrl.trim() || addingRegistry"
            >
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
    <dialog :open="showDeleteRegistry" class="modal" data-test="registry-delete-dialog">
      <div class="modal-box">
        <h3 class="font-bold text-lg">Remove "{{ deleteRegistryTarget?.name }}"?</h3>
        <p class="text-sm py-2 text-base-content/80">
          Servers you already added stay; only the source is removed.
        </p>

        <div v-if="deleteRegistryError" class="alert alert-error text-sm" data-test="registry-delete-error">
          <span>{{ deleteRegistryError }}</span>
        </div>

        <div class="modal-action">
          <button type="button" class="btn btn-ghost" data-test="registry-delete-cancel" @click="closeDeleteRegistry">
            Cancel
          </button>
          <button
            type="button"
            class="btn btn-error"
            data-test="registry-delete-confirm"
            :disabled="deletingRegistry"
            @click="confirmDeleteRegistry"
          >
            <span v-if="deletingRegistry" class="loading loading-spinner loading-xs"></span>
            <span v-else>Remove</span>
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button @click="closeDeleteRegistry">close</button>
      </form>
    </dialog>

    <!-- Success Toast -->
    <div v-if="showSuccessToast" class="toast toast-end" data-test="registry-add-success">
      <div class="alert alert-success">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>{{ successMessage }}</span>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="repositoriesHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { Registry, RepositoryServer, RequiredInput } from '@/types'
import { REGISTRY_PROVENANCE_CUSTOM } from '@/types'

// State
const registries = ref<Registry[]>([])
const selectedRegistries = ref<string[]>([])
// Registries that returned no data this search (e.g. require an API key, or
// errored) — surfaced as a non-fatal notice so partial cross-registry results
// still render.
const unavailableRegistries = ref<string[]>([])
const searchQuery = ref<string>('')
const servers = ref<RepositoryServer[]>([])
const loadingRegistries = ref(false)
const loadingServers = ref(false)
const error = ref<string | null>(null)
const addingServerId = ref<string | null>(null)
const showSuccessToast = ref(false)
const successMessage = ref('')

// Required-input prompt state (Spec 070, T016)
const promptServer = ref<RepositoryServer | null>(null)
const promptInputs = ref<RequiredInput[]>([])
const promptValues = ref<Record<string, string>>({})

// Add / edit registry-source dialog state (MCP-866 add, MCP-1073 edit).
// editRegistryId is empty in add mode and the registry id in edit mode.
const showAddRegistry = ref(false)
const editRegistryId = ref<string | null>(null)
const addRegistryUrl = ref('')
const addRegistryProtocol = ref('modelcontextprotocol/registry')
const addRegistryName = ref('')
const addRegistryError = ref<string | null>(null)
const addingRegistry = ref(false)
const isEditMode = computed(() => editRegistryId.value !== null)

// Delete-custom-registry confirmation state (MCP-1073)
const showDeleteRegistry = ref(false)
const deleteRegistryTarget = ref<Registry | null>(null)
const deleteRegistryError = ref<string | null>(null)
const deletingRegistry = ref(false)

let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null

// A registry is "custom/unverified" (third-party) when its provenance says so,
// or — defensively — when trusted is explicitly false. Anything else (including
// older payloads without the field) is treated as official/trusted.
function isCustomRegistry(registry?: Registry | null): boolean {
  if (!registry) return false
  return registry.provenance === REGISTRY_PROVENANCE_CUSTOM || registry.trusted === false
}

// Transport classification (R2) + filter (R3). Derived purely from the
// install command / url already returned by the registry search API:
//   url set, no install cmd        -> remote
//   npx / npm / node               -> stdio:npm
//   uvx / uv / pip / python        -> stdio:python
//   docker                         -> stdio:docker
//   anything else with install cmd -> stdio
const transportFilter = ref<'all' | 'remote' | 'stdio'>('all')

function serverTransport(server: RepositoryServer): string {
  const cmd = (server.install_cmd || '').trim().toLowerCase()
  if (cmd) {
    if (cmd.startsWith('docker')) return 'stdio:docker'
    if (cmd.startsWith('npx') || /(^|\s)(npm|node)(\s|$)/.test(cmd)) return 'stdio:npm'
    if (cmd.startsWith('uvx') || cmd.startsWith('uv ') || /(^|\s)(pipx?|python3?)(\s|$)/.test(cmd)) return 'stdio:python'
    return 'stdio'
  }
  if (server.url) return 'remote'
  return 'stdio'
}

const filteredServers = computed(() => {
  if (transportFilter.value === 'all') return servers.value
  return servers.value.filter(s => {
    const t = serverTransport(s)
    return transportFilter.value === 'remote' ? t === 'remote' : t.startsWith('stdio')
  })
})

// Registry multiselect (R1) -------------------------------------------------
function registryName(id: string): string {
  return registries.value.find(r => r.id === id)?.name || id
}

const registrySelectLabel = computed(() => {
  const n = selectedRegistries.value.length
  if (n === 0) return 'Choose registries…'
  if (n === 1) return registryName(selectedRegistries.value[0])
  if (n === registries.value.length) return `All registries (${n})`
  return `${n} registries`
})

function toggleRegistry(id: string) {
  const i = selectedRegistries.value.indexOf(id)
  if (i === -1) selectedRegistries.value.push(id)
  else selectedRegistries.value.splice(i, 1)
  handleRegistryChange()
}

function selectAllRegistries() {
  selectedRegistries.value = registries.value.map(r => r.id)
  handleRegistryChange()
}

function clearRegistries() {
  selectedRegistries.value = []
  handleRegistryChange()
}

const showPrompt = computed(() => promptServer.value !== null)

// Add is blocked until every prompted input has a non-empty value.
const promptComplete = computed(() =>
  promptInputs.value.every(i => (promptValues.value[i.name] || '').trim() !== '')
)

const repositoriesHints = computed<Hint[]>(() => {
  return [
    {
      icon: '📦',
      title: 'Discover MCP Servers',
      description: 'Browse official and community MCP servers from multiple registries',
      sections: [
        {
          title: 'How to use',
          list: [
            'Select a registry from the dropdown menu',
            'Search for servers by name or description',
            'Click "Add to MCP" to install a server',
            'View source code and installation commands for each server'
          ]
        }
      ]
    },
    {
      icon: '🤖',
      title: 'LLM Agent Integration',
      description: 'Let AI agents help you discover and install MCP servers',
      sections: [
        {
          title: 'Example prompts',
          list: [
            'Find and add MCP servers for working with GitHub',
            'Install the best MCP server for file system operations',
            'Search for database-related MCP servers and add them',
            'Discover Slack integration servers and configure them'
          ]
        }
      ]
    },
    {
      icon: '💡',
      title: 'Installation Tips',
      description: 'Servers can be installed via npm, pip, or connected remotely',
      sections: [
        {
          title: 'Server types',
          list: [
            'NPM packages: Installed with npx command',
            'Python packages: Installed with uvx or pipx',
            'Remote servers: Connected via HTTP endpoints',
            'Docker containers: Run in isolated environments'
          ]
        }
      ]
    }
  ]
})

// Methods
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

// Cross-registry search (R1): fan out to every selected registry in parallel
// and merge the results. Each result already carries its own `registry` for
// per-card attribution. Per-registry failures (e.g. key-required, unreachable)
// are collected into a non-fatal notice so the registries that DID return keep
// rendering; we only raise a hard error when every selected registry failed.
async function searchServers() {
  const ids = selectedRegistries.value
  if (ids.length === 0) {
    servers.value = []
    return
  }

  loadingServers.value = true
  error.value = null
  unavailableRegistries.value = []

  try {
    const results = await Promise.all(
      ids.map(id =>
        api
          .searchRegistryServers(id, { query: searchQuery.value, limit: 20 })
          .then(r => ({ id, r }))
          .catch(err => ({ id, r: { success: false, error: (err as Error).message } as any }))
      )
    )

    const merged: RepositoryServer[] = []
    const seen = new Set<string>()
    const failures: string[] = []

    for (const { id, r } of results) {
      if (r.success && r.data) {
        if (r.data.unavailable) {
          failures.push(`${registryName(id)}: ${r.data.unavailable.reason || 'unavailable'}`)
        }
        for (const s of r.data.servers || []) {
          const key = `${s.registry || id}::${s.id}`
          if (seen.has(key)) continue
          seen.add(key)
          merged.push(s)
        }
      } else {
        failures.push(`${registryName(id)}: ${r.error || 'failed'}`)
      }
    }

    servers.value = merged
    unavailableRegistries.value = failures
    // Only a hard error when nothing came back AND every registry failed.
    if (merged.length === 0 && failures.length > 0 && failures.length === ids.length) {
      error.value = 'No results — ' + failures.join('; ')
    }
  } finally {
    loadingServers.value = false
  }
}

function handleRegistryChange() {
  servers.value = []
  error.value = null
  unavailableRegistries.value = []
  if (selectedRegistries.value.length > 0) {
    searchServers()
  }
}

function handleSearchInput() {
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer)
  }

  searchDebounceTimer = setTimeout(() => {
    if (selectedRegistries.value.length > 0) {
      searchServers()
    }
  }, 500)
}

// Add a server by reference (Spec 070, T015/T016). The server re-derives the
// config from the registry entry — no client-side install_cmd parsing. When the
// entry declares required inputs the backend returns `missing_required_input`
// with the missing names; we open a prompt, collect values, and resubmit as env.
async function addServer(server: RepositoryServer, env?: Record<string, string>) {
  if (!server.registry) {
    error.value = 'Cannot add: server is missing its registry id.'
    return
  }

  addingServerId.value = server.id
  error.value = null

  try {
    const result = await api.addServerFromRegistry(server.registry, server.id, env ? { env } : undefined)

    if (result.success) {
      closePrompt()
      const name = result.server?.name || server.name
      showToast(`Added "${name}" — quarantined. Approve it on the Servers page to enable.`)
      return
    }

    if (result.code === 'missing_required_input') {
      openPrompt(server, result.missingInputs || [])
      return
    }

    error.value = result.error || 'Failed to add server'
  } catch (err) {
    error.value = 'Failed to add server: ' + (err as Error).message
  } finally {
    addingServerId.value = null
  }
}

// Open the required-input prompt. Prefer the rich declarations carried on the
// search result (name + description + secret); fall back to bare names from the
// backend's missing_required_input error when the search response omitted them.
function openPrompt(server: RepositoryServer, missingNames: string[]) {
  const declared = server.required_inputs || []
  const inputs: RequiredInput[] = missingNames.length > 0
    ? missingNames.map(name => declared.find(d => d.name === name) || { name })
    : declared

  promptServer.value = server
  promptInputs.value = inputs
  promptValues.value = Object.fromEntries(inputs.map(i => [i.name, '']))
}

function submitPrompt() {
  if (!promptServer.value || !promptComplete.value) return
  // Trim values; resubmit through the same add path with the collected env.
  const env: Record<string, string> = {}
  for (const input of promptInputs.value) {
    env[input.name] = (promptValues.value[input.name] || '').trim()
  }
  addServer(promptServer.value, env)
}

function closePrompt() {
  promptServer.value = null
  promptInputs.value = []
  promptValues.value = {}
}

// --- Add / edit / delete registry source (MCP-866 add, MCP-1073 edit+delete) ---

// Open the dialog in add mode (blank, editRegistryId = null).
function openAddRegistry() {
  editRegistryId.value = null
  addRegistryUrl.value = ''
  addRegistryProtocol.value = 'modelcontextprotocol/registry'
  addRegistryName.value = ''
  addRegistryError.value = null
  showAddRegistry.value = true
}

// Open the dialog in edit mode, pre-filled from a custom registry (MCP-1073).
// The id is fixed and shown read-only; only name/url are editable.
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

// Map the backend's stable error codes to actionable messages. Shared across
// add / edit / delete since all three surface the same code set.
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

// Single submit path for both add and edit modes.
function submitRegistryDialog() {
  if (!addRegistryUrl.value.trim() || addingRegistry.value) return
  if (isEditMode.value) doEditRegistry()
  else doAddRegistry()
}

async function doAddRegistry() {
  addingRegistry.value = true
  addRegistryError.value = null

  try {
    const result = await api.addRegistrySource(addRegistryUrl.value.trim(), {
      protocol: addRegistryProtocol.value || undefined,
      name: addRegistryName.value.trim() || undefined
    })

    if (result.success) {
      const added = result.registry
      showAddRegistry.value = false
      // Refresh the list so the new custom entry appears, then select it for
      // immediate browsing.
      await loadRegistries()
      if (added?.id) {
        if (!selectedRegistries.value.includes(added.id)) selectedRegistries.value.push(added.id)
        handleRegistryChange()
      }
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
      url: addRegistryUrl.value.trim() || undefined
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

// Delete-custom-registry confirmation flow (MCP-1073). The kebab Delete only
// opens the modal; the API call happens on explicit confirm.
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

async function confirmDeleteRegistry() {
  const target = deleteRegistryTarget.value
  if (!target) return
  deletingRegistry.value = true
  deleteRegistryError.value = null

  try {
    const result = await api.removeRegistrySource(target.id)
    if (result.success) {
      // Drop it from any active selection so the search filter stays consistent.
      const i = selectedRegistries.value.indexOf(target.id)
      if (i !== -1) {
        selectedRegistries.value.splice(i, 1)
        handleRegistryChange()
      }
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

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
  showToast('Installation command copied to clipboard!')
}

function openURL(url: string) {
  window.open(url, '_blank')
}

function showToast(message: string) {
  successMessage.value = message
  showSuccessToast.value = true
  setTimeout(() => {
    showSuccessToast.value = false
  }, 3000)
}

// Lifecycle
onMounted(() => {
  loadRegistries()
})
</script>
