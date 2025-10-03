<template>
  <div class="drawer-side z-40">
    <label for="sidebar-drawer" aria-label="close sidebar" class="drawer-overlay"></label>
    <aside class="bg-base-100 w-64 min-h-screen flex flex-col border-r border-base-300">
      <!-- Logo Section -->
      <div class="p-6 border-b border-base-300">
        <router-link to="/" class="flex items-center space-x-3">
          <img src="/src/assets/logo.svg" alt="MCPProxy Logo" class="w-10 h-10" />
          <span class="text-xl font-bold">MCPProxy</span>
        </router-link>
      </div>

      <!-- Navigation Menu -->
      <nav class="flex-1 p-4">
        <ul class="menu">
          <li v-for="item in menuItems" :key="item.path">
            <router-link
              :to="item.path"
              :class="{ 'active': isActiveRoute(item.path) }"
              class="flex items-center space-x-3 py-3 px-4 rounded-lg"
            >
              <span class="text-lg">{{ item.name }}</span>
            </router-link>
          </li>
        </ul>
      </nav>

      <!-- Theme Selector at Bottom -->
      <div class="p-4 border-t border-base-300">
        <div class="dropdown dropdown-top dropdown-end w-full">
          <div tabindex="0" role="button" class="btn btn-ghost btn-sm w-full justify-start">
            <svg class="w-5 h-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
            </svg>
            <span class="flex-1 text-left">Theme</span>
          </div>
          <ul tabindex="0" class="dropdown-content z-[1] menu p-2 shadow-2xl bg-base-300 rounded-box w-52 max-h-96 overflow-y-auto mb-2">
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
import { useRoute } from 'vue-router'
import { useSystemStore } from '@/stores/system'

const route = useRoute()
const systemStore = useSystemStore()

const menuItems = [
  { name: 'Dashboard', path: '/' },
  { name: 'Servers', path: '/servers' },
  { name: 'Tools', path: '/search' },
  { name: 'Records', path: '/tool-calls' },
  { name: 'Repositories', path: '/repositories' },
  { name: 'Settings', path: '/settings' },
]

function isActiveRoute(path: string): boolean {
  if (path === '/') {
    return route.path === '/'
  }
  return route.path.startsWith(path)
}
</script>
