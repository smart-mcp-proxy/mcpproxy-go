<template>
  <div class="drawer-side z-40">
    <label for="sidebar-drawer" aria-label="close sidebar" class="drawer-overlay"></label>
    <aside class="bg-base-100 w-64 h-screen flex flex-col border-r border-base-300 fixed">
      <!-- Logo Section -->
      <div class="px-6 py-5 border-b border-base-300">
        <router-link to="/" class="flex items-center space-x-3">
          <img src="/src/assets/logo.svg" alt="MCPProxy Logo" class="w-10 h-10" />
          <div>
            <span class="text-xl font-bold">MCPProxy</span>
            <span v-if="authStore.isTeamsEdition" class="badge badge-xs badge-primary ml-1">Server</span>
          </div>
        </router-link>
      </div>

      <!-- Navigation Menu -->
      <nav class="flex-1 p-4 overflow-y-auto">
        <!-- Server Edition: User Menu -->
        <template v-if="authStore.isTeamsEdition">
          <ul class="menu">
            <li class="menu-title" v-if="authStore.isAdmin">
              <span>My Workspace</span>
            </li>
            <li v-for="item in teamsUserMenu" :key="item.path">
              <router-link
                :to="item.path"
                :class="{ 'active': isActiveRoute(item.path) }"
                class="flex items-center space-x-3 py-3 px-4 rounded-lg"
              >
                <span class="text-lg">{{ item.name }}</span>
              </router-link>
            </li>
          </ul>

          <!-- Admin Section -->
          <template v-if="authStore.isAdmin">
            <div class="divider my-2 px-2"></div>
            <ul class="menu">
              <li class="menu-title">
                <span>Administration</span>
              </li>
              <li v-for="item in teamsAdminMenu" :key="item.path">
                <router-link
                  :to="item.path"
                  :class="{ 'active': isActiveRoute(item.path) }"
                  class="flex items-center space-x-3 py-3 px-4 rounded-lg"
                >
                  <span class="text-lg">{{ item.name }}</span>
                </router-link>
              </li>
            </ul>
          </template>
        </template>

        <!-- Personal Edition: Original Menu -->
        <template v-else>
          <ul class="menu">
            <li v-for="item in personalMenu" :key="item.path">
              <router-link
                :to="item.path"
                :class="{ 'active': isActiveRoute(item.path) }"
                class="flex items-center space-x-3 py-3 px-4 rounded-lg"
              >
                <span class="text-lg">{{ item.name }}</span>
              </router-link>
            </li>
          </ul>
        </template>
      </nav>

      <!-- User Info (Server Edition) -->
      <div v-if="authStore.isTeamsEdition && authStore.isAuthenticated" class="px-4 py-3 border-t border-base-300">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2 min-w-0">
            <div class="avatar placeholder">
              <div class="bg-primary text-primary-content rounded-full w-8">
                <span class="text-xs">{{ userInitials }}</span>
              </div>
            </div>
            <div class="min-w-0">
              <div class="text-sm font-medium truncate">{{ authStore.displayName }}</div>
              <div v-if="authStore.user?.email" class="text-xs text-base-content/50 truncate">{{ authStore.user.email }}</div>
            </div>
          </div>
          <button @click="handleLogout" class="btn btn-ghost btn-xs" title="Sign out">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
            </svg>
          </button>
        </div>
      </div>

      <!-- Version Display -->
      <div v-if="systemStore.version" class="px-4 py-2 border-t border-base-300">
        <div class="text-xs text-base-content/60">
          <span>{{ systemStore.version }}</span>
          <span v-if="systemStore.updateAvailable" class="ml-1 badge badge-xs badge-primary">
            update available
          </span>
        </div>
      </div>

      <!-- Theme Selector at Bottom -->
      <div class="p-4 border-t border-base-300">
        <div class="dropdown dropdown-top dropdown-end w-full">
          <div tabindex="0" role="button" class="btn btn-ghost btn-sm w-full justify-start">
            <svg class="w-5 h-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
            </svg>
            <span class="flex-1 text-left">Theme</span>
          </div>
          <ul tabindex="0" class="dropdown-content z-[1] menu p-2 shadow-2xl bg-base-300 rounded-box w-64 max-h-96 overflow-y-auto mb-2">
            <li class="menu-title">
              <span>Choose theme</span>
            </li>
            <li v-for="theme in systemStore.themes" :key="theme.name">
              <a
                @click="systemStore.setTheme(theme.name)"
                :class="{ 'active': systemStore.currentTheme === theme.name }"
              >
                <span :data-theme="theme.name" class="bg-base-100 rounded-badge w-4 h-4 mr-2"></span>
                {{ theme.displayName }}
              </a>
            </li>
          </ul>
        </div>
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()
const authStore = useAuthStore()

// Personal edition menu (unchanged from original)
const personalMenu = [
  { name: 'Dashboard', path: '/' },
  { name: 'Servers', path: '/servers' },
  { name: 'Secrets', path: '/secrets' },
  { name: 'Agent Tokens', path: '/tokens' },
  { name: 'Search', path: '/search' },
  { name: 'Activity Log', path: '/activity' },
  { name: 'Security Scanners', path: '/security' },
  { name: 'Repositories', path: '/repositories' },
  { name: 'Configuration', path: '/settings' },
  { name: 'Feedback', path: '/feedback' },
]

// Server edition: items visible to all authenticated users
const teamsUserMenu = [
  { name: 'My Servers', path: '/my/servers' },
  { name: 'My Activity', path: '/my/activity' },
  { name: 'Agent Tokens', path: '/my/tokens' },
  { name: 'Diagnostics', path: '/my/diagnostics' },
  { name: 'Search', path: '/search' },
]

// Server edition: items visible only to admins
const teamsAdminMenu = [
  { name: 'Dashboard', path: '/admin/dashboard' },
  { name: 'Server Management', path: '/admin/servers' },
  { name: 'Activity (All)', path: '/activity' },
  { name: 'Users', path: '/admin/users' },
  { name: 'Sessions', path: '/sessions' },
  { name: 'Configuration', path: '/settings' },
]

// Compute user initials for avatar
const userInitials = computed(() => {
  const name = authStore.displayName
  if (!name) return '?'
  const parts = name.split(/[\s@]+/)
  if (parts.length >= 2) {
    return (parts[0][0] + parts[1][0]).toUpperCase()
  }
  return name.substring(0, 2).toUpperCase()
})

function isActiveRoute(path: string): boolean {
  if (path === '/') {
    return route.path === '/'
  }
  return route.path.startsWith(path)
}

async function handleLogout() {
  await authStore.logout()
  router.push('/login')
}
</script>
