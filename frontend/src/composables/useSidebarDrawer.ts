import { computed, ref } from 'vue'

// Spec 134 FR-014: the sidebar is an overlay below `lg` (1024 px, which covers
// every phone width). The overlay's open state is shared by App (owner of the
// checkbox the daisyUI drawer reads), the header's menu button and the sidebar
// (which is `inert` while closed, so nothing inside it is a tab stop).
const drawerOpen = ref(false)
const overlayMode = ref(false)

const sidebarInert = computed(() => overlayMode.value && !drawerOpen.value)

let query: MediaQueryList | null = null
function sync() {
  overlayMode.value = !!query?.matches
}

/** Called once by App on mount; returns a disposer. */
function watchOverlayMode(): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    overlayMode.value = false
    return () => {}
  }
  query = window.matchMedia('(max-width: 1023.98px)')
  sync()
  query.addEventListener?.('change', sync)
  return () => {
    query?.removeEventListener?.('change', sync)
    query = null
  }
}

export function useSidebarDrawer() {
  return { drawerOpen, overlayMode, sidebarInert, watchOverlayMode }
}
