<template>
  <div class="navbar bg-base-100 shadow-md">
    <div class="navbar-start">
      <!-- Mobile menu -->
      <div class="dropdown">
        <div tabindex="0" role="button" class="btn btn-ghost lg:hidden">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h8m-8 6h16" />
          </svg>
        </div>
        <ul tabindex="0" class="menu menu-sm dropdown-content mt-3 z-[1] p-2 shadow bg-base-100 rounded-box w-52">
          <li v-for="item in menuItems" :key="item.path">
            <router-link :to="item.path" :class="{ 'active': isActiveRoute(item.path) }">
              <component :is="item.icon" v-if="item.icon" class="w-5 h-5" />
              {{ item.name }}
            </router-link>
          </li>
        </ul>
      </div>

      <!-- Logo and brand -->
      <router-link to="/" class="btn btn-ghost text-xl">
        <div class="flex items-center space-x-2">
          <img src="/src/assets/logo.svg" alt="MCPProxy Logo" class="w-8 h-8" />
          <span class="hidden sm:inline">MCPProxy</span>
        </div>
      </router-link>
    </div>

    <!-- Desktop menu -->
    <div class="navbar-center hidden lg:flex">
      <ul class="menu menu-horizontal px-1">
        <li v-for="item in menuItems" :key="item.path">
          <router-link :to="item.path" :class="{ 'active': isActiveRoute(item.path) }">
            <component :is="item.icon" v-if="item.icon" class="w-5 h-5" />
            {{ item.name }}
          </router-link>
        </li>
      </ul>
    </div>

    <!-- Right side -->
    <div class="navbar-end">
      <!-- Status indicator -->
      <div class="flex items-center space-x-4 mr-4">
        <div class="flex items-center space-x-2">
          <div
            :class="[
              'w-2 h-2 rounded-full',
              systemStore.isRunning ? 'bg-success animate-pulse' : 'bg-error'
            ]"
          />
          <span class="text-sm hidden sm:inline">
            {{ systemStore.isRunning ? 'Running' : 'Stopped' }}
          </span>
        </div>

        <!-- Server stats -->
        <div class="flex items-center space-x-1 text-sm">
          <span class="hidden md:inline">{{ systemStore.upstreamStats.connected_servers }}/{{ systemStore.upstreamStats.total_servers }}</span>
          <span class="text-xs opacity-60 hidden md:inline">servers</span>
        </div>
      </div>

      <!-- Theme selector -->
      <div class="dropdown dropdown-end">
        <div tabindex="0" role="button" class="btn btn-ghost btn-circle">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
          </svg>
        </div>
        <ul tabindex="0" class="dropdown-content z-[1] menu p-2 shadow-2xl bg-base-300 rounded-box w-52 max-h-96 overflow-y-auto">
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
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useSystemStore } from '@/stores/system'

// Icons (we'll use SVG icons directly for simplicity)
const DashboardIcon = 'svg'
const ServersIcon = 'svg'
const ToolsIcon = 'svg'
const SearchIcon = 'svg'
const SettingsIcon = 'svg'

const route = useRoute()
const systemStore = useSystemStore()

const menuItems = [
  { name: 'Dashboard', path: '/', icon: DashboardIcon },
  { name: 'Servers', path: '/servers', icon: ServersIcon },
  { name: 'Tools', path: '/tools', icon: ToolsIcon },
  { name: 'Search', path: '/search', icon: SearchIcon },
  { name: 'Settings', path: '/settings', icon: SettingsIcon },
]

function isActiveRoute(path: string): boolean {
  if (path === '/') {
    return route.path === '/'
  }
  return route.path.startsWith(path)
}
</script>