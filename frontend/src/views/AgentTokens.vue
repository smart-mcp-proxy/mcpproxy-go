<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Agent Tokens</h1>
        <p class="text-base-content/70 mt-1">Create and manage scoped API tokens for AI agents and automation</p>
      </div>
      <div class="flex gap-2">
        <button
          @click="refreshTokens"
          :disabled="loading"
          class="btn btn-outline"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          <span v-if="loading" class="loading loading-spinner loading-sm"></span>
          {{ loading ? 'Refreshing...' : 'Refresh' }}
        </button>
        <button
          @click="openCreateDialog"
          class="btn btn-primary"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
          </svg>
          Create Token
        </button>
      </div>
    </div>

    <!-- Summary Stats -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Total Tokens</div>
        <div class="stat-value">{{ tokens.length }}</div>
        <div class="stat-desc">All agent tokens</div>
      </div>
      <div class="stat">
        <div class="stat-title">Active</div>
        <div class="stat-value text-success">{{ activeCount }}</div>
        <div class="stat-desc">Currently valid</div>
      </div>
      <div class="stat">
        <div class="stat-title">Expired / Revoked</div>
        <div class="stat-value text-warning">{{ expiredOrRevokedCount }}</div>
        <div class="stat-desc">No longer usable</div>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading tokens...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load tokens</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="refreshTokens" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- Empty State -->
    <div v-else-if="tokens.length === 0" class="text-center py-12">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No agent tokens yet</h3>
      <p class="text-base-content/70 mb-4">
        Create scoped tokens for your AI agents and automated workflows.
      </p>
      <button @click="openCreateDialog" class="btn btn-primary">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
        Create Your First Token
      </button>
    </div>

    <!-- Token List Table -->
    <div v-else class="overflow-x-auto">
      <table class="table table-zebra w-full">
        <thead>
          <tr>
            <th>Name</th>
            <th>Prefix</th>
            <th>Servers</th>
            <th>Permissions</th>
            <th>Expires</th>
            <th>Last Used</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="token in tokens" :key="token.name">
            <td class="font-medium">{{ token.name }}</td>
            <td>
              <code class="text-sm bg-base-200 px-2 py-1 rounded">{{ token.token_prefix }}</code>
            </td>
            <td>
              <div class="flex flex-wrap gap-1">
                <span
                  v-for="server in token.allowed_servers"
                  :key="server"
                  class="badge badge-outline badge-sm"
                >
                  {{ server }}
                </span>
              </div>
            </td>
            <td>
              <div class="flex flex-wrap gap-1">
                <span
                  v-for="perm in token.permissions"
                  :key="perm"
                  class="badge badge-sm"
                  :class="permissionBadgeClass(perm)"
                >
                  {{ perm }}
                </span>
              </div>
            </td>
            <td>
              <span :class="{ 'text-warning': isExpiringSoon(token), 'text-error': isExpired(token) }">
                {{ formatDate(token.expires_at) }}
              </span>
            </td>
            <td>
              <span v-if="token.last_used_at" class="text-sm">
                {{ formatDate(token.last_used_at) }}
              </span>
              <span v-else class="text-base-content/40 text-sm">Never</span>
            </td>
            <td>
              <span v-if="token.revoked" class="badge badge-error badge-sm">Revoked</span>
              <span v-else-if="isExpired(token)" class="badge badge-warning badge-sm">Expired</span>
              <span v-else class="badge badge-success badge-sm">Active</span>
            </td>
            <td>
              <div class="flex gap-1">
                <button
                  @click="handleRegenerate(token.name)"
                  :disabled="token.revoked"
                  class="btn btn-xs btn-outline"
                  title="Regenerate token secret"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                  </svg>
                  Regenerate
                </button>
                <button
                  @click="handleRevoke(token.name)"
                  :disabled="token.revoked"
                  class="btn btn-xs btn-error btn-outline"
                  title="Revoke token"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
                  </svg>
                  Revoke
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Token Secret Display (shown after creation or regeneration) -->
    <div v-if="newTokenSecret" class="alert alert-warning shadow-lg">
      <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">Save this token now!</h3>
        <p class="text-sm mb-2">This token cannot be retrieved again after you dismiss this message.</p>
        <div class="flex items-center gap-2">
          <code class="text-sm bg-base-200 px-3 py-2 rounded font-mono break-all">{{ newTokenSecret }}</code>
          <button
            @click="copyToken"
            class="btn btn-sm btn-outline shrink-0"
            :class="{ 'btn-success': copied }"
          >
            {{ copied ? 'Copied!' : 'Copy' }}
          </button>
        </div>
      </div>
      <button @click="dismissTokenSecret" class="btn btn-sm btn-ghost shrink-0">Dismiss</button>
    </div>

    <!-- Create Token Dialog -->
    <dialog ref="createDialog" class="modal">
      <div class="modal-box">
        <h3 class="font-bold text-lg mb-4">Create Agent Token</h3>

        <div class="space-y-4">
          <!-- Name -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Token Name</span>
            </label>
            <input
              v-model="createForm.name"
              type="text"
              placeholder="e.g., ci-pipeline, dev-agent"
              class="input input-bordered w-full"
              :class="{ 'input-error': createFormErrors.name }"
            />
            <label class="label" v-if="createFormErrors.name">
              <span class="label-text-alt text-error">{{ createFormErrors.name }}</span>
            </label>
            <label class="label" v-else>
              <span class="label-text-alt">Alphanumeric, hyphens, and underscores only</span>
            </label>
          </div>

          <!-- Allowed Servers -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Allowed Servers</span>
            </label>
            <input
              v-model="createForm.allowedServers"
              type="text"
              placeholder="e.g., github, slack or * for all"
              class="input input-bordered w-full"
            />
            <label class="label">
              <span class="label-text-alt">Comma-separated server names, or * for all servers</span>
            </label>
          </div>

          <!-- Permissions -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Permissions</span>
            </label>
            <div class="flex flex-col gap-2">
              <label class="flex items-center gap-2 cursor-not-allowed">
                <input type="checkbox" checked disabled class="checkbox checkbox-sm checkbox-info" />
                <span class="text-sm">read</span>
                <span class="badge badge-info badge-xs">always included</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer">
                <input
                  v-model="createForm.permWrite"
                  type="checkbox"
                  class="checkbox checkbox-sm checkbox-warning"
                />
                <span class="text-sm">write</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer">
                <input
                  v-model="createForm.permDestructive"
                  type="checkbox"
                  class="checkbox checkbox-sm checkbox-error"
                />
                <span class="text-sm">destructive</span>
              </label>
            </div>
          </div>

          <!-- Expiry -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Expires In</span>
            </label>
            <select v-model="createForm.expiresIn" class="select select-bordered w-full">
              <option value="168h">7 days</option>
              <option value="720h">30 days</option>
              <option value="2160h">90 days</option>
              <option value="8760h">365 days</option>
            </select>
          </div>
        </div>

        <div class="modal-action">
          <button @click="closeCreateDialog" class="btn">Cancel</button>
          <button
            @click="handleCreate"
            :disabled="creating"
            class="btn btn-primary"
          >
            <span v-if="creating" class="loading loading-spinner loading-sm"></span>
            {{ creating ? 'Creating...' : 'Create Token' }}
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button>close</button>
      </form>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import apiClient from '@/services/api'
import { useSystemStore } from '@/stores/system'
import type { AgentTokenInfo } from '@/types'

const systemStore = useSystemStore()

const loading = ref(true)
const error = ref<string | null>(null)
const tokens = ref<AgentTokenInfo[]>([])
const creating = ref(false)
const newTokenSecret = ref<string | null>(null)
const copied = ref(false)

const createDialog = ref<HTMLDialogElement | null>(null)

const createForm = ref({
  name: '',
  allowedServers: '*',
  permWrite: false,
  permDestructive: false,
  expiresIn: '720h',
})

const createFormErrors = ref<{ name?: string }>({})

// Computed stats
const activeCount = computed(() => {
  return tokens.value.filter(t => !t.revoked && !isExpired(t)).length
})

const expiredOrRevokedCount = computed(() => {
  return tokens.value.filter(t => t.revoked || isExpired(t)).length
})

// Helper functions
function isExpired(token: AgentTokenInfo): boolean {
  return new Date(token.expires_at) < new Date()
}

function isExpiringSoon(token: AgentTokenInfo): boolean {
  if (token.revoked || isExpired(token)) return false
  const expiresAt = new Date(token.expires_at)
  const now = new Date()
  const hoursLeft = (expiresAt.getTime() - now.getTime()) / (1000 * 60 * 60)
  return hoursLeft < 72
}

function formatDate(dateStr: string): string {
  const date = new Date(dateStr)
  return date.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function permissionBadgeClass(perm: string): string {
  switch (perm) {
    case 'read': return 'badge-info'
    case 'write': return 'badge-warning'
    case 'destructive': return 'badge-error'
    default: return 'badge-ghost'
  }
}

// Data loading
async function loadTokens() {
  loading.value = true
  error.value = null

  try {
    const response = await apiClient.listAgentTokens()
    if (response.success && response.data) {
      tokens.value = response.data.tokens || []
    } else {
      error.value = response.error || 'Failed to load tokens'
    }
  } catch (err: any) {
    error.value = err.message || 'Failed to load tokens'
    console.error('Failed to load tokens:', err)
  } finally {
    loading.value = false
  }
}

const refreshTokens = loadTokens

// Create token
function openCreateDialog() {
  createForm.value = {
    name: '',
    allowedServers: '*',
    permWrite: false,
    permDestructive: false,
    expiresIn: '720h',
  }
  createFormErrors.value = {}
  createDialog.value?.showModal()
}

function closeCreateDialog() {
  createDialog.value?.close()
}

async function handleCreate() {
  // Validate
  createFormErrors.value = {}
  const name = createForm.value.name.trim()

  if (!name) {
    createFormErrors.value.name = 'Token name is required'
    return
  }

  if (!/^[a-zA-Z0-9_-]+$/.test(name)) {
    createFormErrors.value.name = 'Only alphanumeric characters, hyphens, and underscores allowed'
    return
  }

  creating.value = true

  try {
    const allowedServers = createForm.value.allowedServers
      .split(',')
      .map(s => s.trim())
      .filter(s => s.length > 0)

    const permissions: string[] = ['read']
    if (createForm.value.permWrite) permissions.push('write')
    if (createForm.value.permDestructive) permissions.push('destructive')

    const response = await apiClient.createAgentToken({
      name,
      allowed_servers: allowedServers,
      permissions,
      expires_in: createForm.value.expiresIn,
    })

    if (response.success && response.data) {
      newTokenSecret.value = response.data.token
      copied.value = false
      closeCreateDialog()
      await loadTokens()

      systemStore.addToast({
        type: 'success',
        title: 'Token Created',
        message: `Agent token "${name}" created successfully`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Create Failed',
        message: response.error || 'Failed to create token',
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Create Failed',
      message: err.message || 'Failed to create token',
    })
  } finally {
    creating.value = false
  }
}

// Regenerate token
async function handleRegenerate(name: string) {
  if (!confirm(`Regenerate the secret for token "${name}"? The old secret will stop working immediately.`)) {
    return
  }

  try {
    const response = await apiClient.regenerateAgentToken(name)
    if (response.success && response.data) {
      newTokenSecret.value = response.data.token
      copied.value = false

      systemStore.addToast({
        type: 'success',
        title: 'Token Regenerated',
        message: `Token "${name}" has been regenerated. Save the new secret now.`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Regenerate Failed',
        message: response.error || 'Failed to regenerate token',
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Regenerate Failed',
      message: err.message || 'Failed to regenerate token',
    })
  }
}

// Revoke token
async function handleRevoke(name: string) {
  if (!confirm(`Revoke token "${name}"? This action cannot be undone.`)) {
    return
  }

  try {
    const response = await apiClient.revokeAgentToken(name)
    if (response.success || !response.error) {
      await loadTokens()

      systemStore.addToast({
        type: 'success',
        title: 'Token Revoked',
        message: `Token "${name}" has been revoked`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Revoke Failed',
        message: response.error || 'Failed to revoke token',
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Revoke Failed',
      message: err.message || 'Failed to revoke token',
    })
  }
}

// Clipboard
async function copyToken() {
  if (!newTokenSecret.value) return
  try {
    await navigator.clipboard.writeText(newTokenSecret.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // Fallback for non-HTTPS contexts
    const textarea = document.createElement('textarea')
    textarea.value = newTokenSecret.value
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}

function dismissTokenSecret() {
  newTokenSecret.value = null
  copied.value = false
}

onMounted(async () => {
  await new Promise(resolve => setTimeout(resolve, 100))
  loadTokens()
})
</script>
