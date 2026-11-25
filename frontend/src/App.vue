<template>
  <div id="app" class="drawer lg:drawer-open">
    <!-- Spec 134 FR-013: first tab stop on an ordinary load. Moves focus to
         <main> (tabindex -1) so the next Tab starts inside the page. -->
    <a href="#main-content" class="skip-link" data-test="skip-link" :inert="(overlayMode && drawerOpen) || undefined" @click.prevent="skipToContent">Skip to content</a>
    <input id="sidebar-drawer" v-model="drawerOpen" type="checkbox" class="drawer-toggle" tabindex="-1" aria-hidden="true" />

    <!-- Main content area. The left padding is bound to sidebar collapsed
         state so the content fluidly reclaims space when the sidebar shrinks
         to its icon rail. -->
    <div
      class="drawer-content grid grid-rows-[auto_1fr_auto] h-screen bg-base-200 transition-[padding] duration-200 ease-out"
      :inert="(overlayMode && drawerOpen) || undefined"
      :class="systemStore.sidebarCollapsed ? 'lg:pl-14' : 'lg:pl-64'"
    >
      <!-- Top Header -->
      <TopHeader v-if="authStore.shellPresented" />

      <!-- Page content. `min-h-0` / `min-w-0`: a grid item defaults to
           `min-height:auto`, so a tall page pushed this scroll container past
           its track and the footer ended up overlapping the last row at 390px
           (UX audit F14). -->
      <main id="main-content" tabindex="-1" class="overflow-y-auto min-h-0 min-w-0 p-4 sm:p-6 outline-none">
        <!-- #1065: keyed on authEpoch so repairing a failed auth remounts the
             current view. Views hold their load errors in component-local refs
             that nothing outside can reach, so a successful sign-in used to
             leave a stale red error panel and zero rows behind the recovered
             header. Key on authEpoch ONLY -- folding in the route path would
             remount ServerDetail on every :serverName change and break the
             Dashboard's single instance across /, /usage and /overview. -->
        <router-view :key="systemStore.authEpoch" />
      </main>

      <!-- Persistent footer with project links (discussion #948) -->
      <AppFooter />
    </div>

    <!-- Sidebar -->
    <SidebarNav v-if="authStore.shellPresented" />

    <!-- Toast Notifications -->
    <ToastContainer />

    <!-- Connection Status -->
    <ConnectionStatus />

    <!-- Authentication Error Modal -->
    <AuthErrorModal
      :show="authModal.show || undefined"
      :can-close="authModal.canClose"
      :last-error="authModal.lastError"
      @close="handleAuthModalClose"
      @authenticated="handleAuthModalAuthenticated"
      @refresh="handleAuthModalRefresh"
    />
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, reactive, watch } from 'vue'
import { isNavigationFailure, NavigationFailureType, useRouter } from 'vue-router'
import SidebarNav from '@/components/SidebarNav.vue'
import TopHeader from '@/components/TopHeader.vue'
import AppFooter from '@/components/AppFooter.vue'
import ToastContainer from '@/components/ToastContainer.vue'
import ConnectionStatus from '@/components/ConnectionStatus.vue'
import AuthErrorModal from '@/components/AuthErrorModal.vue'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import { useAuthStore } from '@/stores/auth'
import api, { type APIAuthEvent } from '@/services/api'
import { useSidebarDrawer } from '@/composables/useSidebarDrawer'

const systemStore = useSystemStore()
const serversStore = useServersStore()
const authStore = useAuthStore()
const router = useRouter()
const { drawerOpen, overlayMode, watchOverlayMode } = useSidebarDrawer()
drawerOpen.value = false

function skipToContent() {
  const main = document.getElementById('main-content')
  main?.focus({ preventScroll: false })
}

// Spec 134 FR-014: Escape closes the phone overlay and returns focus to the
// menu button; opening moves focus into the sidebar so it can be walked.
function onDrawerKeydown(event: KeyboardEvent) {
  // A layer above the drawer (the palette, a dialog) handles its own Escape.
  if (event.key !== 'Escape' || !drawerOpen.value || event.defaultPrevented || document.querySelector('dialog[open]')) return
  drawerOpen.value = false
}
// The daisyUI drawer reveals the sidebar through a delayed visibility
// transition, and a hidden element cannot take focus: retry until it does.
function focusSidebarWhenVisible(attempt = 0) {
  const target = document.querySelector<HTMLElement>('[data-test="sidebar-overlay"] a[href], [data-test="sidebar-overlay"] button')
  target?.focus()
  if (target && document.activeElement !== target && attempt < 10 && drawerOpen.value) {
    setTimeout(() => focusSidebarWhenVisible(attempt + 1), 60)
  }
}
watch(drawerOpen, async (open, wasOpen) => {
  await nextTick()
  if (open) {
    focusSidebarWhenVisible()
  } else if (wasOpen) {
    document.querySelector<HTMLElement>('[data-test="header-drawer-toggle"]')?.focus()
  }
})
let stopWatchingOverlay: (() => void) | null = null

// The checkbox controls the mobile overlay only; desktop collapse state stays
// in the system store. Keep the destination visible after navigation, including
// selecting the already-active link, but leave the menu open on a rejected move.
const removeDrawerNavigationHook = router.afterEach((_to, _from, failure) => {
  if (!failure || isNavigationFailure(failure, NavigationFailureType.duplicated)) {
    drawerOpen.value = false
  }
})

// Authentication modal state
const authModal = reactive({
  show: false,
  canClose: true, // Allow closing by default (users can continue without API key for now)
  lastError: ''
})

// API event listener cleanup function
let removeAPIListener: (() => void) | null = null

// Authentication modal handlers
function handleAuthModalClose() {
  authModal.show = false
  authModal.lastError = ''
  systemStore.setAuthRequired(false)
}

// These five doors are full-administrator reads. Keep them together so a
// single positive, settled core-capability transition owns both initial load
// and recovery. In particular, do not call this from an auth-key repair before
// its fresh probe has identified the new principal.
function loadCore() {
  systemStore.connectEventSource()
  serversStore.fetchServers()
  systemStore.fetchInfo() // TopHeader version / update state
  systemStore.fetchRouting() // TopHeader routing chip
  systemStore.fetchScopeFilterFeatures()
}

// Register during setup, before the mounted bootstrap begins. `canLoadCore`
// is false while every probe is pending, and rises only after it has settled.
// Thus this has no competing onMounted load path and exactly one owner for an
// initial eligible principal or each recovered eligible principal. Dropping
// eligibility closes a prior admin stream before a tenant or signed-out
// principal can inherit it.
watch(() => authStore.canLoadCore, (canLoad, wasAbleToLoad) => {
  if (canLoad && !wasAbleToLoad) loadCore()
  if (!canLoad && wasAbleToLoad) systemStore.disconnectEventSource()
})

async function recoverAfterAuth() {
  // This must finish before either the core watcher or authEpoch can react to
  // the repaired credential. A failed probe leaves the protected UI closed;
  // Login exposes its retry path for session-only recovery.
  await authStore.checkAuth({ fresh: true })
  if (!authStore.canShowShell || authStore.bootstrapError) {
    // The auth modal can be opened over any protected route. Once its fresh
    // probe fails, the shell correctly disappears, so move to Login rather
    // than stranding the user on a page with no visible retry affordance.
    // router.currentRoute is router-owned internal state; retain only a
    // single-slash path before putting it into Login's validated redirect.
    const current = router.currentRoute.value.fullPath
    const redirect = current.startsWith('/') && !current.startsWith('//') ? current : '/'
    if (router.currentRoute.value.path !== '/login') {
      await router.replace({ name: 'login', query: { redirect } })
    }
    return
  }
  systemStore.markAuthRecovered()
}

function handleAuthModalAuthenticated() {
  authModal.show = false
  authModal.lastError = ''
  // markAuthRecovered, not setAuthRequired(false): this path validated the key,
  // so it is safe to invalidate every view that failed while auth was broken.
  void recoverAfterAuth()
}

function handleAuthModalRefresh(verified: boolean) {
  if (!verified) {
    // The reloaded key did not authenticate. Leave the modal and the
    // auth-required flag in place rather than remounting every view onto a
    // key that is still 401.
    return
  }

  authModal.show = false
  authModal.lastError = ''
  // The modal verified the reloaded key, so this is a real recovery and the
  // views holding stale auth errors must be invalidated too (#1065).
  void recoverAfterAuth()
}

// Handle API authentication errors
function handleAuthError(event: APIAuthEvent) {
  console.log('Global auth error received:', event)

  // Spec 107 FR-041 / T088: a session principal (tenant or admin, no local
  // API key) never holds an API key to type into the modal below — a 401
  // there means the cookie/JWT went stale, and the fix is to sign in again
  // via the IdP, not to prompt for a credential that does not exist.
  if (authStore.isTeamsEdition && !api.hasAPIKey()) {
    void router.push('/login')
    return
  }

  authModal.lastError = event.error
  authModal.show = true
  // Audit F28: one cause, one message. The modal now suppresses the reconnect
  // toast and the inline load errors it would otherwise compete with.
  systemStore.setAuthRequired(true)
}

onMounted(async () => {
  stopWatchingOverlay = watchOverlayMode()
  window.addEventListener('keydown', onDrawerKeydown)
  // Set up API error listener
  removeAPIListener = api.addEventListener(handleAuthError)

  // The watcher above is already listening when this probe settles.
  await authStore.checkAuth()
})

onUnmounted(() => {
  stopWatchingOverlay?.()
  window.removeEventListener('keydown', onDrawerKeydown)
  removeDrawerNavigationHook()
  systemStore.disconnectEventSource()

  // Clean up API event listener
  if (removeAPIListener) {
    removeAPIListener()
  }
})
</script>

<!-- Page transitions removed: caused CSS transition deadlock blocking SPA navigation (QA 2026-03-29) -->
