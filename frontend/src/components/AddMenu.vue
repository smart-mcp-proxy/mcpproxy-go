<template>
  <!-- Spec 109 FR-052: the header "+ Add" menu. Personal edition: Server,
       Client, Token (and Profile once Spec 108 registers /profiles). The
       server edition has no Clients hub, so an admin gets one item, "Personal
       server". A tenant gets no menu: tenants add servers from /my/servers,
       and /add-server writes through an admin-only door (Spec 107 FR-041). -->
  <div v-if="visible" ref="root" class="relative shrink-0" @keydown="onKeydown">
    <button
      ref="trigger"
      type="button"
      class="btn btn-primary"
      aria-haspopup="menu"
      :aria-expanded="open ? 'true' : 'false'"
      aria-label="Add"
      data-test="header-add-menu"
      @click="toggle"
    >
      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
      </svg>
      <span class="hidden min-[1100px]:inline">Add</span>
      <svg class="w-3 h-3 hidden min-[1100px]:block" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
      </svg>
    </button>
    <ul
      v-if="open"
      role="menu"
      aria-label="Add"
      class="absolute right-0 top-full mt-2 min-w-44 p-1 shadow-lg bg-base-100 rounded-box border border-base-300 z-[var(--z-dropdown)]"
    >
      <li v-for="entry in entries" :key="entry.id" role="none">
        <button
          type="button"
          role="menuitem"
          tabindex="-1"
          class="w-full text-left px-3 py-2 rounded hover:bg-base-200 focus:bg-base-200 focus:outline-none text-sm whitespace-nowrap"
          :data-test="`add-menu-${entry.id}`"
          @click="select(entry)"
        >
          {{ entry.label }}
        </button>
      </li>
    </ul>
    <!-- The connect dialog mounts on first use so the header adds no work
         until someone asks to connect a client. -->
    <ClientConnectList
      v-if="connectMounted"
      :show="connectOpen"
      :focus-client="focusClient"
      @close="connectOpen = false"
      @updated="clientsStore.refreshPresence()"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import ClientConnectList from '@/components/ClientConnectList.vue'
import { ADD_MENU_ITEMS, ADD_MENU_SERVER_EDITION, CONNECT_CLIENT_EVENT, type AddMenuEntry } from '@/navigation/navModel'

const router = useRouter()
const authStore = useAuthStore()
const clientsStore = useClientsStore()

const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)
const open = ref(false)
const connectMounted = ref(false)
const connectOpen = ref(false)
// Spec 108-i I8: a Clients row's call to action names the client to focus.
const focusClient = ref('')

const visible = computed(() => authStore.principalKind !== 'tenant')

// Profile appears only when the router has that exact path (Spec 108 owns the
// route); the server edition has no Clients hub, so it gets its own single item.
const entries = computed<AddMenuEntry[]>(() => {
  if (authStore.isTeamsEdition) return ADD_MENU_SERVER_EDITION
  const paths = new Set(router.getRoutes().map((r) => r.path))
  return ADD_MENU_ITEMS.filter((item) => !item.requiresRoutePath || paths.has(item.requiresRoutePath))
})

function menuItems(): HTMLElement[] {
  return Array.from(root.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])
}

function focusItem(index: number) {
  const items = menuItems()
  if (!items.length) return
  items[(index + items.length) % items.length]?.focus()
}

async function openMenu() {
  open.value = true
  await nextTick()
  focusItem(0)
}

function closeMenu(restoreFocus = false) {
  if (!open.value) return
  open.value = false
  if (restoreFocus) trigger.value?.focus()
}

function toggle() {
  if (open.value) closeMenu()
  else void openMenu()
}

function select(entry: AddMenuEntry) {
  closeMenu()
  if (entry.id === 'client') {
    openConnect()
    return
  }
  if (entry.to) void router.push(entry.to)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    closeMenu(true)
    return
  }
  if (!open.value) {
    if (event.key === 'ArrowDown' && event.target === trigger.value) {
      event.preventDefault()
      void openMenu()
    }
    return
  }
  const items = menuItems()
  const current = items.findIndex((el) => el === document.activeElement)
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    focusItem(current + 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    focusItem(current < 0 ? items.length - 1 : current - 1)
  } else if (event.key === 'Home') {
    event.preventDefault()
    focusItem(0)
  } else if (event.key === 'End') {
    event.preventDefault()
    focusItem(items.length - 1)
  } else if (event.key === 'Tab') {
    closeMenu()
  }
}

function onOutsideMouseDown(event: MouseEvent) {
  if (root.value && !root.value.contains(event.target as Node)) closeMenu()
}

watch(open, (isOpen) => {
  if (isOpen) document.addEventListener('mousedown', onOutsideMouseDown)
  else document.removeEventListener('mousedown', onOutsideMouseDown)
})

// The command palette's "Connect client" action reuses this one dialog.
function openConnect(event?: Event) {
  if (!visible.value) return
  const detail = (event as CustomEvent<{ client?: string } | null> | undefined)?.detail
  focusClient.value = typeof detail?.client === 'string' ? detail.client : ''
  connectMounted.value = true
  connectOpen.value = true
}

onMounted(() => window.addEventListener(CONNECT_CLIENT_EVENT, openConnect))
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onOutsideMouseDown)
  window.removeEventListener(CONNECT_CLIENT_EVENT, openConnect)
})
</script>
