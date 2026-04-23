<template>
  <div class="drawer-side z-40">
    <label for="sidebar-drawer" aria-label="close sidebar" class="drawer-overlay"></label>
    <aside
      class="bg-base-100 h-screen flex flex-col border-r border-base-300 fixed transition-[width] duration-200 ease-out"
      :class="collapsed ? 'w-14' : 'w-64'"
    >
      <!-- Logo + collapse toggle -->
      <div
        class="border-b border-base-300 flex items-center"
        :class="collapsed ? 'px-2 py-4 justify-center' : 'px-4 py-4 justify-between'"
      >
        <router-link to="/" class="flex items-center gap-2 min-w-0" :title="logoTitle">
          <img src="/src/assets/logo.svg" alt="MCPProxy Logo" class="w-8 h-8 shrink-0" />
          <div v-show="!collapsed" class="min-w-0">
            <span class="text-lg font-bold truncate block leading-tight">MCPProxy</span>
            <span v-if="authStore.isTeamsEdition" class="badge badge-xs badge-primary">Server</span>
          </div>
        </router-link>
        <button
          v-show="!collapsed"
          @click="systemStore.toggleSidebar"
          class="btn btn-ghost btn-xs btn-square text-base-content/40 hover:text-base-content"
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 19l-7-7 7-7m8 14l-7-7 7-7" />
          </svg>
        </button>
      </div>
      <!-- Expand button: only visible in collapsed state, rendered as a separate row -->
      <button
        v-if="collapsed"
        @click="systemStore.toggleSidebar"
        class="mx-auto mt-2 mb-1 btn btn-ghost btn-xs btn-square text-base-content/40 hover:text-base-content"
        aria-label="Expand sidebar"
        title="Expand sidebar"
      >
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 5l7 7-7 7M5 5l7 7-7 7" />
        </svg>
      </button>

      <!-- Version + Check for updates (expanded sidebar only). Single-line layout:
           version on the left, action on the right. In collapsed mode the
           version appears in the logo tooltip instead. -->
      <div
        v-if="!collapsed && systemStore.version"
        class="px-3 py-2 border-b border-base-300 flex items-center gap-2"
        data-testid="sidebar-version-block"
      >
        <span
          class="font-mono text-xs text-base-content/60 shrink-0"
          data-testid="sidebar-version"
        >
          v{{ displayVersion }}
        </span>
        <span
          v-if="systemStore.updateAvailable"
          class="badge badge-xs badge-primary shrink-0"
          :title="latestVersionTitle"
        >
          update
        </span>
        <button
          type="button"
          @click="handleCheckForUpdates"
          :disabled="systemStore.checkingForUpdates"
          class="btn btn-ghost btn-xs ml-auto gap-1 px-1.5 font-normal text-[11px] text-base-content/70 hover:text-base-content"
          data-testid="sidebar-check-updates"
          :title="updateStatusTitle"
          :aria-label="updateButtonLabel"
        >
          <svg
            v-if="!systemStore.checkingForUpdates"
            class="w-3.5 h-3.5"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0A8.003 8.003 0 014.582 15H9" />
          </svg>
          <span v-else class="loading loading-spinner loading-xs"></span>
          <span class="truncate">{{ updateCompactLabel }}</span>
        </button>
      </div>

      <!-- Navigation Menu -->
      <nav
        class="flex-1 overflow-y-auto overflow-x-hidden"
        :class="collapsed ? 'px-2 py-2' : 'px-3 py-3'"
      >
        <!-- Server Edition: User Menu -->
        <template v-if="authStore.isTeamsEdition">
          <ul class="menu menu-sm gap-0.5 p-0">
            <li v-if="authStore.isAdmin && !collapsed" class="menu-title px-3 !py-1">
              <span class="text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40">My Workspace</span>
            </li>
            <li v-for="item in teamsUserMenu" :key="item.path">
              <router-link
                :to="item.path"
                :class="{ 'active': isActiveRoute(item.path) }"
                class="rounded-lg"
                :title="collapsed ? item.name : ''"
              >
                <span :class="collapsed ? 'mx-auto' : ''">{{ item.name }}</span>
              </router-link>
            </li>
          </ul>

          <template v-if="authStore.isAdmin">
            <div class="divider my-2 px-2"></div>
            <ul class="menu menu-sm gap-0.5 p-0">
              <li v-if="!collapsed" class="menu-title px-3 !py-1">
                <span class="text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40">Administration</span>
              </li>
              <li v-for="item in teamsAdminMenu" :key="item.path">
                <router-link
                  :to="item.path"
                  :class="{ 'active': isActiveRoute(item.path) }"
                  class="rounded-lg"
                  :title="collapsed ? item.name : ''"
                >
                  <span :class="collapsed ? 'mx-auto' : ''">{{ item.name }}</span>
                </router-link>
              </li>
            </ul>
          </template>
        </template>

        <!-- Personal Edition: Grouped Menu -->
        <template v-else>
          <!-- Dashboard (solo top row, no section label) -->
          <ul class="menu menu-sm gap-0.5 p-0">
            <li>
              <router-link
                to="/"
                :class="{ 'active': isActiveRoute('/') }"
                class="rounded-lg font-medium"
                :title="collapsed ? 'Dashboard' : ''"
              >
                <IconDashboard class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed">Dashboard</span>
              </router-link>
            </li>
          </ul>

          <!-- Section: Workspace -->
          <div
            v-if="!collapsed"
            class="mt-5 mb-1 px-3 text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40"
          >
            Workspace
          </div>
          <div v-else class="mt-3 mb-1 mx-auto w-6 h-px bg-base-300"></div>

          <ul class="menu menu-sm gap-0.5 p-0">
            <li>
              <router-link
                to="/servers"
                :class="{ 'active': isActiveRoute('/servers') }"
                class="rounded-lg font-medium"
                :title="collapsed ? 'Servers' : ''"
              >
                <IconServers class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed">Servers</span>
              </router-link>
            </li>
            <li>
              <router-link
                to="/secrets"
                :class="{ 'active': isActiveRoute('/secrets') }"
                class="rounded-lg font-medium"
                :title="collapsed ? 'Secrets' : ''"
              >
                <IconSecrets class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed">Secrets</span>
              </router-link>
            </li>
            <!-- Sub-item: Agent Tokens nested under Secrets.
                 In expanded mode: indented with a left bracket.
                 In collapsed mode: shown as a normal icon row. -->
            <li>
              <router-link
                to="/tokens"
                :class="[
                  { 'active': isActiveRoute('/tokens') },
                  collapsed ? 'rounded-lg' : 'rounded-lg !pl-7 text-[13px] text-base-content/75',
                ]"
                :title="collapsed ? 'Agent Tokens' : ''"
              >
                <IconTokens class="w-4 h-4 shrink-0" :class="collapsed ? 'w-5 h-5' : ''" />
                <span v-show="!collapsed">Agent Tokens</span>
              </router-link>
            </li>
            <li>
              <router-link
                to="/activity"
                :class="{ 'active': isActiveRoute('/activity') }"
                class="rounded-lg font-medium"
                :title="collapsed ? 'Activity Log' : ''"
              >
                <IconActivity class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed">Activity Log</span>
              </router-link>
            </li>
            <li>
              <router-link
                to="/security"
                :class="{ 'active': isActiveRoute('/security') }"
                class="rounded-lg font-medium"
                :title="collapsed ? 'Security Scanners' : ''"
              >
                <IconShield class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed">Security Scanners</span>
              </router-link>
            </li>
          </ul>

          <!-- Section: System -->
          <div
            v-if="!collapsed"
            class="mt-5 mb-1 px-3 text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40"
          >
            System
          </div>
          <div v-else class="mt-3 mb-1 mx-auto w-6 h-px bg-base-300"></div>

          <ul class="menu menu-sm gap-0.5 p-0">
            <li>
              <router-link
                to="/repositories"
                :class="{ 'active': isActiveRoute('/repositories') }"
                class="rounded-lg text-base-content/70"
                :title="collapsed ? 'Repositories' : ''"
              >
                <IconRepo class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed" class="text-[13px]">Repositories</span>
              </router-link>
            </li>
            <li>
              <router-link
                to="/settings"
                :class="{ 'active': isActiveRoute('/settings') }"
                class="rounded-lg text-base-content/70"
                :title="collapsed ? 'Configuration' : ''"
              >
                <IconSettings class="w-5 h-5 shrink-0" />
                <span v-show="!collapsed" class="text-[13px]">Configuration</span>
              </router-link>
            </li>
          </ul>
        </template>
      </nav>

      <!-- User Info (Server Edition) -->
      <div v-if="authStore.isTeamsEdition && authStore.isAuthenticated && !collapsed" class="px-4 py-3 border-t border-base-300">
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

      <!-- Footer: theme + feedback (version is shown under the logo at the top) -->
      <div class="border-t border-base-300 py-2" :class="collapsed ? 'px-1' : 'px-3'">
        <!-- Action row: Theme + Feedback -->
        <div
          class="flex items-stretch gap-1"
          :class="collapsed ? 'flex-col' : ''"
        >
          <!-- Theme dropdown -->
          <div
            class="dropdown dropdown-top"
            :class="collapsed ? '' : 'dropdown-end flex-1'"
          >
            <div
              tabindex="0"
              role="button"
              class="btn btn-ghost btn-sm font-normal"
              :class="collapsed ? 'btn-square w-full' : 'w-full justify-start gap-2 px-2'"
              :title="collapsed ? 'Theme' : ''"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
              </svg>
              <span v-show="!collapsed">Theme</span>
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

          <!-- Feedback icon button.
               Uses inline flex centering (not btn-square) because btn-square's
               fixed aspect ratio fights with w-full in collapsed mode, and a
               plain btn would left-align its content. -->
          <router-link
            v-if="!authStore.isTeamsEdition"
            to="/feedback"
            class="btn btn-ghost btn-sm !h-9 !min-h-[2.25rem] px-0 flex items-center justify-center"
            :class="[
              { 'btn-active': isActiveRoute('/feedback') },
              collapsed ? 'w-full' : 'w-9',
            ]"
            title="Send feedback"
            aria-label="Send feedback"
          >
            <svg class="w-5 h-5" fill="none" stroke="currentColor" stroke-width="1.8" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" d="M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 5v-5z" />
            </svg>
          </router-link>
        </div>
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, h, type FunctionalComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()
const authStore = useAuthStore()

const collapsed = computed(() => systemStore.sidebarCollapsed)

// Strip a leading "v" so the template can format consistently as `v<version>`.
const displayVersion = computed(() => systemStore.version.replace(/^v/i, ''))

// Tooltip shown on hover over the logo/home link. In collapsed sidebar mode
// this is the only surface that communicates the running version.
const logoTitle = computed(() => {
  const v = systemStore.version
  return v ? `MCPProxy ${v}` : 'MCPProxy'
})

const latestVersionTitle = computed(() => {
  const latest = systemStore.latestVersion
  return latest ? `Latest release: ${latest}` : 'Update available'
})

// Full button label, used as aria-label and full tooltip state.
const updateButtonLabel = computed(() =>
  systemStore.updateAvailable ? 'Update available — view release' : 'Check for updates'
)

// Compact label used in the sidebar row so the button fits on the same line
// as the version string.
const updateCompactLabel = computed(() => {
  if (systemStore.checkingForUpdates) return 'Checking…'
  return systemStore.updateAvailable ? 'View release' : 'Check'
})

const updateStatusTitle = computed(() => {
  const ts = systemStore.updateCheckedAt
  if (!ts) return 'Check for updates on GitHub'
  const d = new Date(ts)
  return `Last checked ${d.toLocaleString()}`
})

async function handleCheckForUpdates() {
  // If an update is already known, open the release page instead of re-checking.
  const releaseUrl = systemStore.info?.update?.release_url
  if (systemStore.updateAvailable && releaseUrl) {
    window.open(releaseUrl, '_blank', 'noopener,noreferrer')
    return
  }
  await systemStore.checkForUpdates()
}

// --- Inline SVG icon components (Heroicons outline style, stroke=currentColor) ---
// Kept local to this file so the sidebar remains self-contained. Each icon is a
// functional component rendering a single <svg>.
const iconProps = {
  fill: 'none',
  stroke: 'currentColor',
  'stroke-width': 1.6,
  'stroke-linecap': 'round' as const,
  'stroke-linejoin': 'round' as const,
  viewBox: '0 0 24 24',
}

const makeIcon = (d: string): FunctionalComponent =>
  (props) => h('svg', { ...iconProps, ...props }, [h('path', { d })])

const IconDashboard = makeIcon(
  'M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z'
)
const IconServers = makeIcon(
  'M4 7a2 2 0 012-2h12a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V7zm0 8a2 2 0 012-2h12a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zm4-6h.01M8 17h.01'
)
const IconSecrets = makeIcon(
  'M12 11v3m-3-3a3 3 0 116 0m-9 3v6a1 1 0 001 1h10a1 1 0 001-1v-6a1 1 0 00-1-1H6a1 1 0 00-1 1z'
)
const IconTokens = makeIcon(
  'M15 7a4 4 0 11-8 0 4 4 0 018 0zM15 7l6 6m-3-3l3 3-2 2m-4-4l2-2'
)
const IconActivity = makeIcon(
  'M4 12h3l3-8 4 16 3-8h3'
)
const IconShield = makeIcon(
  'M12 3l8 3v6c0 5-3.5 8.5-8 9-4.5-.5-8-4-8-9V6l8-3zm-3 9l2 2 4-4'
)
const IconRepo = makeIcon(
  'M4 4.5A2.5 2.5 0 016.5 2H19v16H6.5a2.5 2.5 0 000 5H19v2H6.5A2.5 2.5 0 014 22.5v-18z'
)
const IconSettings = makeIcon(
  'M10.3 3.6a1.5 1.5 0 013.4 0l.2 1.1a7 7 0 011.9.8l1-.6a1.5 1.5 0 012.1 2.1l-.6 1a7 7 0 01.8 1.9l1.1.2a1.5 1.5 0 010 3.4l-1.1.2a7 7 0 01-.8 1.9l.6 1a1.5 1.5 0 01-2.1 2.1l-1-.6a7 7 0 01-1.9.8l-.2 1.1a1.5 1.5 0 01-3.4 0l-.2-1.1a7 7 0 01-1.9-.8l-1 .6a1.5 1.5 0 01-2.1-2.1l.6-1a7 7 0 01-.8-1.9l-1.1-.2a1.5 1.5 0 010-3.4l1.1-.2a7 7 0 01.8-1.9l-.6-1a1.5 1.5 0 012.1-2.1l1 .6a7 7 0 011.9-.8l.2-1.1zM12 9a3 3 0 100 6 3 3 0 000-6z'
)

// Server edition menus (unchanged behavior)
const teamsUserMenu = [
  { name: 'My Servers', path: '/my/servers' },
  { name: 'My Activity', path: '/my/activity' },
  { name: 'Agent Tokens', path: '/my/tokens' },
  { name: 'Diagnostics', path: '/my/diagnostics' },
  { name: 'Search', path: '/search' },
]

const teamsAdminMenu = [
  { name: 'Dashboard', path: '/admin/dashboard' },
  { name: 'Server Management', path: '/admin/servers' },
  { name: 'Activity (All)', path: '/activity' },
  { name: 'Users', path: '/admin/users' },
  { name: 'Sessions', path: '/sessions' },
  { name: 'Configuration', path: '/settings' },
]

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

<style scoped>
/* Tighten DaisyUI menu padding when collapsed so icons center cleanly */
nav :deep(.menu li > a),
nav :deep(.menu li > .router-link-active),
nav :deep(.menu li > a.router-link-active) {
  transition: padding 0.15s ease;
}
</style>
