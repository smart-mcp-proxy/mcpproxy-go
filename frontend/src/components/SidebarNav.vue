<template>
  <div class="drawer-side z-[var(--z-sidebar)]">
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


      <!-- Navigation Menu -->
      <nav
        class="flex-1 overflow-y-auto overflow-x-hidden"
        :class="collapsed ? 'px-2 py-2' : 'px-3 py-3'"
      >
        <!-- Server Edition: User Menu -->
        <template v-if="authStore.isTeamsEdition">
          <ul class="menu menu-sm w-full gap-0.5 p-0">
            <li v-if="authStore.isAdmin && !collapsed" class="menu-title px-3 !py-1">
              <span class="text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40">My Workspace</span>
            </li>
            <li v-for="item in teamsUserMenu" :key="item.path">
              <router-link
                :to="item.path"
                :class="{ 'active': isActiveRoute(item.path) }"
                class="rounded-lg"
                :title="collapsed ? item.name : ''"
                :aria-label="collapsed ? item.name : undefined"
              >
                <span :class="collapsed ? 'mx-auto' : ''">{{ item.name }}</span>
              </router-link>
            </li>
          </ul>

          <template v-if="authStore.isAdmin">
            <div class="divider my-2 px-2"></div>
            <ul class="menu menu-sm w-full gap-0.5 p-0">
              <li v-if="!collapsed" class="menu-title px-3 !py-1">
                <span class="text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40">Administration</span>
              </li>
              <li v-for="item in teamsAdminMenu" :key="item.path">
                <router-link
                  :to="item.path"
                  :class="{ 'active': isActiveRoute(item.path) }"
                  class="rounded-lg"
                  :title="collapsed ? item.name : ''"
                  :aria-label="collapsed ? item.name : undefined"
                >
                  <span :class="collapsed ? 'mx-auto' : ''">{{ item.name }}</span>
                </router-link>
              </li>
            </ul>
          </template>
        </template>

        <!-- Personal Edition: Grouped Menu (Spec 109 FR-050). Names, order and
             targets come from navigation/navModel.ts; the palette and the
             "+ Add" menu read the same model. -->
        <template v-else>
          <!-- Spec 046 v2: top-pinned Setup entry (above Home).
               Always present: pulses with a badge while tabs are incomplete
               and shows a quiet checkmark once clients/servers/verify are all
               satisfied, so the wizard stays re-enterable. Click reopens the
               wizard at the first incomplete tab. -->
          <ul class="menu menu-sm w-full gap-0.5 p-0 mb-1">
            <li>
              <a
                href="#"
                class="rounded-lg font-medium relative group"
                :class="setupIncomplete
                  ? 'bg-gradient-to-r from-primary/10 to-secondary/10 hover:from-primary/15 hover:to-secondary/15'
                  : 'text-base-content/60'"
                :title="collapsed ? setupTitleAttr : ''"
                :aria-label="collapsed ? setupTitleAttr : undefined"
                data-test="sidebar-setup"
                @click.prevent="onClickSetup"
              >
                <span class="relative inline-flex items-center justify-center">
                  <IconSparkles
                    class="w-5 h-5 shrink-0"
                    :class="setupIncomplete ? 'text-primary' : 'text-base-content/40'"
                  />
                  <!-- Pulse halo when incomplete -->
                  <span
                    v-if="setupIncomplete"
                    class="absolute inline-flex h-5 w-5 rounded-full bg-primary opacity-30 animate-ping"
                    aria-hidden="true"
                  ></span>
                </span>
                <span v-show="!collapsed" class="flex-1">
                  <span v-if="setupIncomplete || !setupStateKnown">Setup</span>
                  <span v-else class="inline-flex items-center gap-1">
                    <span>Setup</span>
                    <span class="text-success text-xs">✓</span>
                  </span>
                </span>
                <span
                  v-if="setupIncomplete && setupCount > 0"
                  class="badge badge-primary badge-sm"
                  :class="collapsed ? 'absolute -top-1 -right-1 badge-xs' : ''"
                  data-test="sidebar-setup-badge"
                >{{ setupCount }}</span>
              </a>
            </li>
          </ul>

          <!-- Home (solo top row, no group label). The badge is the FR-001
               needs-attention count every other surface reads. -->
          <ul class="menu menu-sm w-full gap-0.5 p-0">
            <li>
              <router-link
                :to="SIDEBAR_HOME.path"
                :class="{ 'active': isActiveRoute(SIDEBAR_HOME.path) }"
                class="rounded-lg font-medium"
                :title="collapsed ? SIDEBAR_HOME.label : ''"
                :aria-label="collapsed ? SIDEBAR_HOME.label : undefined"
                :data-test="`sidebar-item-${SIDEBAR_HOME.id}`"
              >
                <span class="relative inline-flex">
                  <component :is="icons[SIDEBAR_HOME.icon]" class="w-5 h-5 shrink-0" />
                  <span
                    v-if="attentionStore.count > 0 && collapsed"
                    class="badge badge-warning badge-xs absolute -top-1 -right-1"
                    data-test="sidebar-home-badge-collapsed"
                  ></span>
                </span>
                <span v-show="!collapsed" class="flex-1">{{ SIDEBAR_HOME.label }}</span>
                <span
                  v-if="attentionStore.count > 0 && !collapsed"
                  class="badge badge-warning badge-sm"
                  data-test="sidebar-home-badge"
                >{{ attentionStore.count }}</span>
              </router-link>
            </li>
          </ul>

          <template v-for="group in visibleGroups" :key="group.id">
            <div
              v-if="!collapsed"
              class="mt-5 mb-1 px-3 text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40"
              :data-test="`sidebar-group-${group.id}`"
            >
              {{ group.label }}
            </div>
            <div v-else class="mt-3 mb-1 mx-auto w-6 h-px bg-base-300" :data-test="`sidebar-group-${group.id}`"></div>

            <ul class="menu menu-sm w-full gap-0.5 p-0">
              <li v-for="item in group.items" :key="item.id">
                <router-link
                  :to="item.path"
                  :class="{ 'active': isActiveRoute(item.path) }"
                  class="rounded-lg font-medium"
                  :title="collapsed ? item.label : ''"
                  :aria-label="collapsed ? item.label : undefined"
                  :data-test="`sidebar-item-${item.id}`"
                >
                  <component :is="icons[item.icon]" class="w-5 h-5 shrink-0" />
                  <span v-show="!collapsed" class="flex-1">{{ item.label }}</span>
                  <span
                    v-if="!collapsed && badgeValue(item) > 0"
                    class="badge badge-sm tabular-nums"
                    :class="item.badge === 'review' ? 'badge-warning' : 'badge-ghost'"
                    :data-test="`sidebar-${item.id}-badge`"
                  >{{ badgeValue(item) }}</span>
                </router-link>
              </li>
            </ul>
          </template>
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

      <!-- Footer: Settings · Docs · Feedback · Theme, then the version row
           (Spec 109 FR-050). The server edition keeps only Theme here — its
           Settings lives in the admin menu and it has no Feedback page. -->
      <div class="border-t border-base-300 py-2" :class="collapsed ? 'px-1' : 'px-2'" data-test="sidebar-footer">
        <div
          class="flex gap-0.5"
          :class="collapsed ? 'flex-col items-stretch' : 'flex-wrap items-center'"
        >
          <template v-if="!authStore.isTeamsEdition">
            <template v-for="item in SIDEBAR_FOOTER" :key="item.id">
              <a
                v-if="item.external"
                :href="item.path"
                target="_blank"
                rel="noopener noreferrer"
                class="btn btn-ghost btn-sm font-normal gap-1.5 px-2"
                :class="collapsed ? 'btn-square w-full' : ''"
                :title="collapsed ? item.label : ''"
                :aria-label="collapsed ? item.label : undefined"
                :data-test="`sidebar-item-${item.id}`"
              >
                <component :is="icons[item.icon]" class="w-4 h-4 shrink-0" />
                <span v-show="!collapsed" class="text-xs">{{ item.label }}</span>
              </a>
              <router-link
                v-else
                :to="item.path"
                class="btn btn-ghost btn-sm font-normal gap-1.5 px-2"
                :class="[{ 'btn-active': isActiveRoute(item.path) }, collapsed ? 'btn-square w-full' : '']"
                :title="collapsed ? item.label : ''"
                :aria-label="collapsed ? item.label : undefined"
                :data-test="`sidebar-item-${item.id}`"
              >
                <component :is="icons[item.icon]" class="w-4 h-4 shrink-0" />
                <span v-show="!collapsed" class="text-xs">{{ item.label }}</span>
              </router-link>
            </template>
          </template>

          <!-- Theme dropdown.
               Sidebar sits at the left edge, so the theme menu must open rightward
               (start-aligned). dropdown-end would anchor the menu's right edge to the
               button and push a ~288px menu off the left of the viewport. -->
          <div class="dropdown dropdown-top" :class="collapsed ? 'w-full' : ''">
            <div
              tabindex="0"
              role="button"
              class="btn btn-ghost btn-sm font-normal gap-1.5 px-2"
              :class="collapsed ? 'btn-square w-full' : ''"
              :title="collapsed ? THEME_LABEL : ''"
              :aria-label="collapsed ? THEME_LABEL : undefined"
              data-test="sidebar-item-theme"
            >
              <component :is="icons.theme" class="w-4 h-4 shrink-0" />
              <span v-show="!collapsed" class="text-xs">{{ THEME_LABEL }}</span>
            </div>
            <ul tabindex="0" class="dropdown-content z-[1] menu flex-nowrap p-2 shadow-2xl bg-base-300 rounded-box w-72 max-h-96 overflow-y-auto mb-2" aria-label="Choose theme">
              <li class="menu-title">
                <span>Choose theme</span>
              </li>
              <!-- Default: follow the OS light/dark setting (UX audit F29). -->
              <li>
                <a
                  data-test="theme-option-system"
                  :aria-current="systemStore.currentTheme === 'system' ? 'true' : undefined"
                  :class="{ 'active': systemStore.currentTheme === 'system' }"
                  @click="systemStore.setTheme('system')"
                >
                  <span :data-theme="systemStore.resolvedTheme" class="bg-base-100 rounded-badge w-4 h-4 mr-2"></span>
                  System
                  <span class="ml-auto text-xs opacity-60">
                    {{ systemStore.resolvedTheme === 'dark' ? 'dark' : 'light' }}
                  </span>
                </a>
              </li>
              <li class="menu-title pt-2">
                <span>More themes</span>
              </li>
              <li v-for="theme in explicitThemes" :key="theme.name">
                <a
                  :data-test="`theme-option-${theme.name}`"
                  :aria-current="systemStore.currentTheme === theme.name ? 'true' : undefined"
                  :class="{ 'active': systemStore.currentTheme === theme.name }"
                  @click="systemStore.setTheme(theme.name)"
                >
                  <span :data-theme="theme.name" class="bg-base-100 rounded-badge w-4 h-4 mr-2"></span>
                  {{ theme.displayName }}
                </a>
              </li>
            </ul>
          </div>
        </div>

        <!-- Version + Check for updates (expanded sidebar only). Single-line
             layout: version on the left, action on the right. In collapsed
             mode the version appears in the logo tooltip instead. -->
        <div
          v-if="!collapsed && systemStore.version"
          class="mt-2 px-1 pt-2 border-t border-base-300 flex items-center gap-2"
          data-testid="sidebar-version-block"
        >
          <span
            class="font-mono text-xs text-base-content/60 shrink-0"
            data-testid="sidebar-version"
          >
            v{{ displayVersion }}
          </span>
          <span
            v-if="systemStore.updateAvailable && !systemStore.updateNudgesSuppressed"
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
      </div>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch, type FunctionalComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingStore } from '@/stores/onboarding'
import { useAttentionStore } from '@/stores/attention'
import { useClientsStore } from '@/stores/clients'
import api from '@/services/api'
import {
  SIDEBAR_FOOTER,
  SIDEBAR_GROUPS,
  SIDEBAR_HOME,
  TEAMS_ADMIN_MENU,
  TEAMS_USER_MENU,
  THEME_LABEL,
  type NavIcon,
  type NavItem,
} from '@/navigation/navModel'

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const attentionStore = useAttentionStore()
const clientsStore = useClientsStore()

// Spec 046 v2: badge count drives the sidebar Setup entry's pulse + count.
// Refetched on mount; the wizard itself drives subsequent updates while open.
//
// Pulse is also gated on `!isEngaged` so that headless / LAN-server installs
// (where HasConnectedClient and FirstMCPClientEver are structurally false —
// there's no local AI client and no GUI to install one) can quiet the badge
// by clicking "Close for now". The Setup entry itself remains in the sidebar
// so the user can re-open the wizard, but it no longer pulses or shows a
// count after engagement.
const setupCount = computed(() => onboardingStore.incompleteTabCount)
const setupIncomplete = computed(
  () => setupCount.value > 0 && !onboardingStore.isEngaged
)

// Audit F06: `incompleteTabCount` reads `state?.incomplete_tab_count ?? 0`, so
// "we were never told" and "nothing left to do" both arrive here as 0. Behind
// the auth modal (or any failed/pending fetch) that painted a green "Setup ✓"
// over an install with steps outstanding — and because nothing ever moves
// `state` off null, it stayed there rather than flashing. The store's own
// `loading` cannot stand in: it is false both before the first fetch and after
// a failed one. Unknown gets the plain label the incomplete branch already
// uses; the pulse and badge are gated on `setupIncomplete` and stay off.
const setupStateKnown = computed(() => onboardingStore.state !== null)

const setupTitleAttr = computed(() => {
  if (setupIncomplete.value) {
    return `Setup (${setupCount.value} step${setupCount.value === 1 ? '' : 's'} remaining)`
  }
  return setupStateKnown.value ? 'Setup ✓' : 'Setup'
})

function onClickSetup() {
  // Open the wizard via the store. If the user is on a deep route, they stay
  // there — the wizard is a modal and renders above whatever view is mounted.
  if (route.path !== '/') {
    router.push('/').then(() => onboardingStore.openWizard())
  } else {
    onboardingStore.openWizard()
  }
}

function loadBadgeCounts() {
  // Personal-edition only — the surrounding template gates this for personal users.
  if (!authStore.isTeamsEdition || authStore.canLoadCore) {
    void onboardingStore.fetchState()
    void fetchToolCount()
    void fetchReviewCount()
    void fetchSecretCount()
    // Clients badge (personal edition only: the server edition has no
    // Clients surface). Silent refresh — never touches the page's loading state.
    if (!authStore.isTeamsEdition) void clientsStore.refreshPresence()
  }
}

onMounted(() => {
  // Pull initial state so the badge is correct on first render.
  loadBadgeCounts()
  // Spec 109 FR-001/FR-003: the sidebar is global (outside the Home view),
  // so it fetches its own copy rather than depending on Home having mounted
  // first — the badge must be correct on every page, not only "/".
  attentionStore.fetchAttention()
  window.addEventListener('mcpproxy:review-changed', fetchReviewCount)
})

onUnmounted(() => window.removeEventListener('mcpproxy:review-changed', fetchReviewCount))

// #1065: the sidebar sits outside <router-view>, so App.vue's authEpoch key
// cannot remount it. Without this, badge counts that failed while auth was
// broken keep their stale values until a full page reload.
watch(() => systemStore.authEpoch, () => {
  loadBadgeCounts()
  attentionStore.fetchAttention()
})

const collapsed = computed(() => systemStore.sidebarCollapsed)

// "System" is rendered on its own above the divider; the rest are the explicit
// theme choices grouped under "More themes" (UX audit F29).
const explicitThemes = computed(() =>
  systemStore.themes.filter((t) => t.name !== 'system'),
)

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
  return `Last checked ${formatDateTime(ts)}`
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
const IconActivity = makeIcon(
  'M4 12h3l3-8 4 16 3-8h3'
)
const IconShield = makeIcon(
  'M12 3l8 3v6c0 5-3.5 8.5-8 9-4.5-.5-8-4-8-9V6l8-3zm-3 9l2 2 4-4'
)
const IconSettings = makeIcon(
  'M10.3 3.6a1.5 1.5 0 013.4 0l.2 1.1a7 7 0 011.9.8l1-.6a1.5 1.5 0 012.1 2.1l-.6 1a7 7 0 01.8 1.9l1.1.2a1.5 1.5 0 010 3.4l-1.1.2a7 7 0 01-.8 1.9l.6 1a1.5 1.5 0 01-2.1 2.1l-1-.6a7 7 0 01-1.9.8l-.2 1.1a1.5 1.5 0 01-3.4 0l-.2-1.1a7 7 0 01-1.9-.8l-1 .6a1.5 1.5 0 01-2.1-2.1l.6-1a7 7 0 01-.8-1.9l-1.1-.2a1.5 1.5 0 010-3.4l1.1-.2a7 7 0 01.8-1.9l-.6-1a1.5 1.5 0 012.1-2.1l1 .6a7 7 0 011.9-.8l.2-1.1zM12 9a3 3 0 100 6 3 3 0 000-6z'
)
// Spec 046 v2: sparkles icon for the top-pinned Setup entry.
const IconSparkles = makeIcon(
  'M12 3l1.6 4.6L18 9l-4.4 1.4L12 15l-1.6-4.6L6 9l4.4-1.4L12 3zm6 11l.8 2.4L21 17l-2.2.6L18 20l-.8-2.4L15 17l2.2-.6L18 14zM6 14l.8 2.4L9 17l-2.2.6L6 20l-.8-2.4L3 17l2.2-.6L6 14z'
)
// Spec 050: wrench/tool icon for the global Tools nav entry.
const IconTools = makeIcon(
  'M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3-3a1 1 0 000-1.4l-1.6-1.6a1 1 0 00-1.4 0l-1 1L15 3l-5 5-1.3-1.3a1 1 0 00-1.4 0l-3 3a1 1 0 000 1.4L6 13l-3 3a1 1 0 000 1.4l2.6 2.6a1 1 0 001.4 0l3-3a1 1 0 000-1.4L8.7 14l5-5 1.6 1.6z'
)
// Spec 109-i: Clients gets its own "people" glyph (it used to borrow the key).
const IconUsers = makeIcon(
  'M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z'
)
const IconProfiles = makeIcon(
  'M10 6H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V8a2 2 0 00-2-2h-5m-4 0V5a2 2 0 114 0v1m-4 0a2 2 0 104 0m-5 8a2 2 0 100-4 2 2 0 000 4zm0 0c1.306 0 2.417.835 2.83 2M9 14a3.001 3.001 0 00-2.83 2M15 11h3m-3 4h2'
)
const IconChart = makeIcon(
  'M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z'
)
const IconBook = makeIcon(
  'M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253'
)
const IconChat = makeIcon(
  'M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 5v-5z'
)
const IconSun = makeIcon(
  'M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z'
)

// Icon key (navModel.ts) -> component.
const icons: Record<NavIcon, FunctionalComponent> = {
  home: IconDashboard,
  clients: IconUsers,
  profiles: IconProfiles,
  servers: IconServers,
  tools: IconTools,
  review: IconShield,
  secrets: IconSecrets,
  activity: IconActivity,
  usage: IconChart,
  settings: IconSettings,
  docs: IconBook,
  feedback: IconChat,
  theme: IconSun,
}

// An item with `requiresRoutePath` shows only when the router has that exact
// path (Profiles, until Spec 108 registers /profiles). By path, not name, so
// the owning spec may name its route freely.
function hasRoutePath(path: string): boolean {
  return router.getRoutes().some((r) => r.path === path)
}

const visibleGroups = computed(() =>
  SIDEBAR_GROUPS.map((group) => ({
    ...group,
    items: group.items.filter((item: NavItem) => !item.requiresRoutePath || hasRoutePath(item.requiresRoutePath)),
  })),
)

// Spec 050: live tool count for the sidebar badge.
const toolCount = ref(0)
const reviewCount = ref(0)
async function fetchReviewCount() {
  try {
    const resp = await api.getReviewQueue()
    if (resp.success) reviewCount.value = resp.data?.count ?? 0
  } catch {
    // A badge must not make the sidebar fail when a core is older or offline.
  }
}

async function fetchToolCount() {
  try {
    const resp = await api.getGlobalTools()
    if (resp.success && resp.data) {
      toolCount.value = resp.data.stats.total
    }
  } catch {
    // Silently ignore — badge is non-critical
  }
}

// Sidebar badge parity with Tools: show total server + secret counts so the
// WORKSPACE section reads consistently. Server count is already reactive via
// the status SSE; secrets need a one-shot fetch on mount.
const serverCount = computed(() => systemStore.upstreamStats.total_servers ?? 0)
const secretCount = ref(0)

// Per-item badge value (0 hides it). Home keeps its own template because it
// also renders a collapsed-mode dot.
function badgeValue(item: NavItem): number {
  switch (item.badge) {
    case 'clients': return clientsStore.liveCount
    case 'servers': return serverCount.value
    case 'tools': return toolCount.value
    case 'review': return reviewCount.value
    case 'secrets': return secretCount.value
    case 'attention': return attentionStore.count
    default: return 0
  }
}

async function fetchSecretCount() {
  try {
    // Use the same source as the Secrets page (total_secrets from
    // /secrets/config) so the badge matches what the user sees there.
    // /secrets/refs is NOT equivalent — it counts every config reference
    // including ${env:...} placeholders (TERM_SESSION_ID etc.), not stored
    // keyring secrets.
    const resp = await api.getConfigSecrets()
    if (resp.success && resp.data) {
      secretCount.value = resp.data.total_secrets ?? 0
    }
  } catch {
    // Silently ignore — badge is non-critical
  }
}

// Server edition menus (unchanged behavior)
const teamsUserMenu = TEAMS_USER_MENU

const teamsAdminMenu = TEAMS_ADMIN_MENU

const userInitials = computed(() => {
  const name = authStore.displayName
  if (!name) return '?'
  const parts = name.split(/[\s@]+/)
  if (parts.length >= 2) {
    return (parts[0][0] + parts[1][0]).toUpperCase()
  }
  return name.substring(0, 2).toUpperCase()
})

// /overview redirects to / (Spec 109 FR-051), so both paths keep the "Home"
// entry highlighted — the redirect target is what the route ends up on, but
// this stays correct even mid-navigation.
const HOME_PATHS = ['/', '/overview']

function isActiveRoute(path: string): boolean {
  if (path === '/') {
    return HOME_PATHS.includes(route.path)
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
