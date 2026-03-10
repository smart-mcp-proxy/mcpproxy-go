<template>
  <div class="space-y-6 max-w-6xl mx-auto">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-2xl font-bold">Users</h1>
        <p class="text-base-content/70 mt-1">Manage team members and their access</p>
      </div>
      <button @click="loadUsers" class="btn btn-sm btn-ghost" :disabled="loading">
        <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        </svg>
        Refresh
      </button>
    </div>

    <!-- Summary Stats -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Total Users</div>
        <div class="stat-value">{{ users.length }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Active</div>
        <div class="stat-value text-success">{{ activeCount }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Disabled</div>
        <div class="stat-value text-base-content/40">{{ disabledCount }}</div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading && users.length === 0" class="flex justify-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
      <button class="btn btn-sm" @click="loadUsers">Try Again</button>
    </div>

    <!-- Empty -->
    <div v-else-if="users.length === 0" class="text-center py-12 text-base-content/60">
      <p class="text-lg font-medium">No users found</p>
    </div>

    <!-- Users Table -->
    <div v-else class="card bg-base-100 shadow-sm">
      <!-- Search -->
      <div class="p-4 border-b border-base-300">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Search by email or name..."
          class="input input-bordered input-sm w-full max-w-xs"
        />
      </div>

      <div class="overflow-x-auto">
        <table class="table">
          <thead>
            <tr>
              <th>User</th>
              <th>Provider</th>
              <th>Last Login</th>
              <th>Status</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="user in filteredUsers" :key="user.id" class="hover">
              <td>
                <div>
                  <div class="font-medium">{{ user.display_name || '-' }}</div>
                  <div class="text-sm text-base-content/60">{{ user.email }}</div>
                </div>
              </td>
              <td>
                <span class="badge badge-sm badge-outline">{{ user.provider }}</span>
              </td>
              <td>
                <span v-if="user.last_login_at" class="text-sm" :title="user.last_login_at">
                  {{ formatRelativeTime(user.last_login_at) }}
                </span>
                <span v-else class="text-sm text-base-content/40">Never</span>
              </td>
              <td>
                <span class="badge badge-sm" :class="user.disabled ? 'badge-error' : 'badge-success'">
                  {{ user.disabled ? 'Disabled' : 'Active' }}
                </span>
              </td>
              <td>
                <div class="flex gap-2">
                  <button
                    class="btn btn-ghost btn-xs"
                    @click="toggleUserStatus(user)"
                    :disabled="togglingUser === user.id"
                    :title="user.disabled ? 'Enable user' : 'Disable user'"
                  >
                    <span v-if="togglingUser === user.id" class="loading loading-spinner loading-xs"></span>
                    {{ user.disabled ? 'Enable' : 'Disable' }}
                  </button>
                  <router-link
                    :to="{ path: '/activity', query: { user_id: user.id } }"
                    class="btn btn-ghost btn-xs"
                    title="View user's activity"
                  >
                    Activity
                  </router-link>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="filteredUsers.length === 0 && searchQuery" class="p-8 text-center text-base-content/60">
        No users match "{{ searchQuery }}"
      </div>
    </div>

    <!-- Action Error -->
    <div v-if="actionError" class="alert alert-error">
      <span>{{ actionError }}</span>
      <button class="btn btn-ghost btn-xs" @click="actionError = ''">Dismiss</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

interface TeamUser {
  id: string
  email: string
  display_name: string
  provider: string
  created_at: string
  last_login_at: string
  disabled: boolean
}

const loading = ref(false)
const error = ref('')
const actionError = ref('')
const users = ref<TeamUser[]>([])
const searchQuery = ref('')
const togglingUser = ref('')

const activeCount = computed(() => users.value.filter(u => !u.disabled).length)
const disabledCount = computed(() => users.value.filter(u => u.disabled).length)

const filteredUsers = computed(() => {
  if (!searchQuery.value) return users.value
  const q = searchQuery.value.toLowerCase()
  return users.value.filter(u =>
    u.email.toLowerCase().includes(q) ||
    (u.display_name && u.display_name.toLowerCase().includes(q))
  )
})

function formatRelativeTime(timestamp: string): string {
  const now = Date.now()
  const time = new Date(timestamp).getTime()
  const diff = now - time
  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

async function loadUsers() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/admin/users', { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()
    users.value = Array.isArray(data) ? data : []
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load users'
  } finally {
    loading.value = false
  }
}

async function toggleUserStatus(user: TeamUser) {
  togglingUser.value = user.id
  actionError.value = ''
  try {
    const action = user.disabled ? 'enable' : 'disable'
    const res = await fetch(`/api/v1/admin/users/${encodeURIComponent(user.id)}/${action}`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.error || `HTTP ${res.status}`)
    }
    await loadUsers()
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : 'Failed to update user'
  } finally {
    togglingUser.value = ''
  }
}

onMounted(() => {
  loadUsers()
})
</script>
