<template>
  <!-- Spec 109 FR-054: the command palette. Cmd/Ctrl+K or "/" opens it; the
       header search field opens it on focus. One <dialog> driven through
       useDialogOpen (showModal → top layer, FR-055). -->
  <dialog
    ref="dialogEl"
    class="modal items-start"
    data-test="command-palette"
    aria-label="Command palette"
  >
    <div class="modal-box p-0 w-full max-w-xl mt-[10vh] overflow-hidden">
      <div class="flex items-center gap-2 px-4 py-3 border-b border-base-300">
        <svg class="w-5 h-5 shrink-0 text-base-content/50" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
        </svg>
        <input
          ref="inputEl"
          v-model="query"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-autocomplete="list"
          aria-label="Search servers, tools, settings"
          :aria-controls="listboxId"
          :aria-activedescendant="activeId"
          autocomplete="off"
          spellcheck="false"
          class="grow min-w-0 bg-transparent outline-none text-base"
          placeholder="Search servers, tools, settings…"
          data-test="palette-input"
          @keydown="onInputKeydown"
        />
        <kbd class="kbd kbd-sm shrink-0">Esc</kbd>
      </div>
      <ul :id="listboxId" role="listbox" aria-label="Results" class="max-h-[60vh] overflow-y-auto py-1">
        <template v-for="section in sections" :key="section.id">
          <li
            role="presentation"
            class="px-4 pt-3 pb-1 text-[10px] font-semibold uppercase tracking-[0.12em] text-base-content/40"
            :data-test="`palette-section-${section.id}`"
          >
            {{ section.label }}
          </li>
          <li
            v-for="(row, n) in section.rows"
            :id="rowId(row.index)"
            :key="row.key"
            role="option"
            :aria-selected="row.index === active ? 'true' : 'false'"
            class="flex items-center justify-between gap-3 px-4 py-2 cursor-pointer text-sm"
            :class="row.index === active ? 'bg-primary/10' : 'hover:bg-base-200'"
            :data-test="`palette-row-${section.id}-${n}`"
            @mousemove="active = row.index"
            @click="activate(row)"
          >
            <span class="truncate">{{ row.label }}</span>
            <span v-if="row.hint" class="shrink-0 text-xs text-base-content/50 truncate max-w-[45%]">{{ row.hint }}</span>
          </li>
        </template>
        <li v-if="!sections.length" role="presentation" class="px-4 py-6 text-sm text-center text-base-content/60">
          No matches
        </li>
      </ul>
    </div>
    <form method="dialog" class="modal-backdrop"><button aria-label="Close" tabindex="-1">close</button></form>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter, type RouteLocationRaw } from 'vue-router'
import api from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useServersStore } from '@/stores/servers'
import { useDialogOpen } from '@/composables/useDialogOpen'
import { allCatalogFields, SERVER_EDITION_FIELDS } from '@/views/settings/fields'
import {
  CONNECT_CLIENT_EVENT,
  PALETTE_ACTIONS,
  SIDEBAR_FOOTER,
  SIDEBAR_GROUPS,
  SIDEBAR_HOME,
  TEAMS_ADMIN_MENU,
  TEAMS_USER_MENU,
  type NavItem,
} from '@/navigation/navModel'

type SectionId = 'search' | 'pages' | 'actions' | 'servers' | 'tools' | 'settings'

interface Row {
  key: string
  label: string
  hint?: string
  /** Position across all sections; drives arrow keys and aria-activedescendant. */
  index: number
  run: () => void
}
interface Section {
  id: SectionId
  label: string
  rows: Row[]
}
interface ToolRow {
  name: string
  server: string
}

const SEARCH_DEBOUNCE_MS = 150
const RESULT_LIMIT = 8

const open = defineModel<boolean>('open', { default: false })
const router = useRouter()
const authStore = useAuthStore()
const serversStore = useServersStore()

const query = ref('')
const active = ref(0)
const toolRows = ref<ToolRow[]>([])
const inputEl = ref<HTMLInputElement | null>(null)
const listboxId = `palette-listbox-${Math.random().toString(36).slice(2, 8)}`

const { dialogEl } = useDialogOpen(() => open.value, () => { open.value = false })

// A tenant has no admin doors (Spec 107 FR-041): only their own pages, and
// free text still goes to /tools, which is in the tenant menu.
const isTenant = computed(() => authStore.principalKind === 'tenant')
const text = computed(() => query.value.trim())
const needle = computed(() => text.value.toLowerCase())

function hasRoutePath(path: string | undefined): boolean {
  return !path || router.getRoutes().some((r) => r.path === path)
}

function matches(label: string): boolean {
  return !needle.value || label.toLowerCase().includes(needle.value)
}

function go(to: RouteLocationRaw) {
  void router.push(to)
}

interface RowSpec { key: string; label: string; hint?: string; run: () => void }

const pageSpecs = computed<RowSpec[]>(() => {
  const external = (item: NavItem) => () => window.open(item.path, '_blank', 'noopener,noreferrer')
  let specs: RowSpec[]
  if (isTenant.value) {
    specs = TEAMS_USER_MENU.map((p) => ({ key: `page:${p.path}`, label: p.name, run: () => go(p.path) }))
  } else if (authStore.isTeamsEdition) {
    specs = [...TEAMS_USER_MENU, ...TEAMS_ADMIN_MENU].map((p) => ({ key: `page:${p.path}`, label: p.name, run: () => go(p.path) }))
  } else {
    const items = [SIDEBAR_HOME, ...SIDEBAR_GROUPS.flatMap((g) => g.items), ...SIDEBAR_FOOTER]
    specs = items
      .filter((item) => hasRoutePath(item.requiresRoutePath))
      .map((item) => ({ key: `page:${item.id}`, label: item.label, run: item.external ? external(item) : () => go(item.path) }))
  }
  return specs.filter((s) => matches(s.label))
})

const actionSpecs = computed<RowSpec[]>(() => {
  if (isTenant.value) return []
  return PALETTE_ACTIONS
    // The server edition has no Clients hub: it can only add a personal server.
    .filter((a) => !authStore.isTeamsEdition || a.id === 'add-server')
    .filter((a) => hasRoutePath(a.requiresRoutePath) && matches(a.label))
    .map((a) => ({
      key: `action:${a.id}`,
      label: a.label,
      run: a.opens === 'connect-client'
        ? () => window.dispatchEvent(new CustomEvent(CONNECT_CLIENT_EVENT))
        : () => go(a.to!),
    }))
})

const serverSpecs = computed<RowSpec[]>(() => {
  if (isTenant.value) return []
  return serversStore.servers
    .filter((s) => matches(s.name))
    .slice(0, RESULT_LIMIT)
    .map((s) => ({ key: `server:${s.name}`, label: s.name, hint: 'Server', run: () => go(`/servers/${encodeURIComponent(s.name)}`) }))
})

const serverEditionKeys = new Set(SERVER_EDITION_FIELDS.map((f) => f.key))
const settingSpecs = computed<RowSpec[]>(() => {
  if (isTenant.value || !needle.value) return []
  return allCatalogFields()
    .filter((f) => authStore.isTeamsEdition || !serverEditionKeys.has(f.key))
    .filter((f) => f.label.toLowerCase().includes(needle.value) || f.key.toLowerCase().includes(needle.value))
    .slice(0, RESULT_LIMIT)
    .map((f) => ({ key: `setting:${f.key}`, label: f.label, hint: f.key, run: () => go({ path: '/settings', query: { focus: f.key } }) }))
})

const toolSpecs = computed<RowSpec[]>(() =>
  isTenant.value
    ? []
    : toolRows.value.map((t) => ({
        key: `tool:${t.server}:${t.name}`,
        label: t.name,
        hint: t.server,
        run: () => go({ path: '/tools', query: { server: t.server, q: t.name } }),
      })),
)

const sections = computed<Section[]>(() => {
  const defs: Array<[SectionId, string, RowSpec[]]> = [
    ['search', 'Search', text.value
      ? [{ key: 'search', label: `Search tools for “${text.value}”`, run: () => go({ path: '/tools', query: { q: text.value } }) }]
      : []],
    ['pages', 'Pages', pageSpecs.value],
    ['actions', 'Actions', actionSpecs.value],
    ['servers', 'Servers', serverSpecs.value],
    ['tools', 'Tools', toolSpecs.value],
    ['settings', 'Settings', settingSpecs.value],
  ]
  let index = 0
  return defs
    .filter(([, , specs]) => specs.length > 0)
    .map(([id, label, specs]) => ({ id, label, rows: specs.map((spec) => ({ ...spec, index: index++ })) }))
})

const flatRows = computed(() => sections.value.flatMap((s) => s.rows))
const rowId = (index: number) => `${listboxId}-opt-${index}`
const activeId = computed(() => (flatRows.value[active.value] ? rowId(active.value) : undefined))

// Keep the active row in range as results come and go, without moving it
// under the user when tool results land after typing.
watch(flatRows, (rows) => {
  if (active.value >= rows.length) active.value = Math.max(0, rows.length - 1)
})

// --- tool search: debounced, sequenced, never on empty text ---------------

let timer: ReturnType<typeof setTimeout> | null = null
let sequence = 0

function cancelSearch() {
  if (timer) clearTimeout(timer)
  timer = null
  sequence++ // drops any response still in flight
}

async function runSearch(value: string) {
  const mine = ++sequence
  try {
    const response = await api.searchTools(value, RESULT_LIMIT)
    if (mine !== sequence) return
    // A 400 or any failure just shows no tool rows; the palette never retries.
    toolRows.value = response.success && response.data
      ? (response.data.results ?? []).map((r) => ({ name: r.tool.name, server: r.tool.server_name }))
      : []
  } catch {
    if (mine === sequence) toolRows.value = []
  }
}

watch(query, (raw) => {
  active.value = 0
  const value = raw.trim()
  cancelSearch()
  if (!value || isTenant.value) {
    toolRows.value = []
    return
  }
  timer = setTimeout(() => {
    timer = null
    void runSearch(value)
  }, SEARCH_DEBOUNCE_MS)
})

// --- open / close -----------------------------------------------------------

let opener: HTMLElement | null = null

watch(open, async (isOpen) => {
  if (isOpen) {
    await nextTick()
    inputEl.value?.focus()
    return
  }
  cancelSearch()
  query.value = ''
  toolRows.value = []
  active.value = 0
  // Hand focus back to whatever opened the palette; with no opener, just let go
  // of the (now hidden) input so a later "/" is not mistaken for typing.
  if (opener && opener !== document.body && opener.isConnected) opener.focus()
  else inputEl.value?.blur()
  opener = null
})

/** Open the palette, optionally seeded with text the user already typed. */
function show(seed = '') {
  if (!open.value) opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  query.value = seed
  open.value = true
}

function close() {
  open.value = false
}

function activate(row: Row) {
  close()
  row.run()
}

function onInputKeydown(event: KeyboardEvent) {
  const count = flatRows.value.length
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      if (count) active.value = (active.value + 1) % count
      break
    case 'ArrowUp':
      event.preventDefault()
      if (count) active.value = (active.value - 1 + count) % count
      break
    case 'Home':
    case 'End':
      // With text in the field these keys move the caret, as in any combobox.
      if (query.value) break
      event.preventDefault()
      if (count) active.value = event.key === 'Home' ? 0 : count - 1
      break
    case 'Enter': {
      event.preventDefault()
      const row = flatRows.value[active.value]
      if (row) activate(row)
      break
    }
    case 'Escape':
      event.preventDefault()
      close()
      break
  }
}

// --- global hotkeys -----------------------------------------------------------

function isEditable(el: Element | EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  return el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName)
}

function onWindowKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k') {
    // preventDefault so the browser's own Ctrl/Cmd+K (focus address bar) stays out of it.
    event.preventDefault()
    if (open.value) close()
    else show()
    return
  }
  if (
    event.key === '/' &&
    !event.metaKey && !event.ctrlKey && !event.altKey &&
    !open.value &&
    !isEditable(document.activeElement) &&
    !isEditable(event.target) &&
    !document.querySelector('dialog[open]')
  ) {
    event.preventDefault()
    show()
  }
}

onMounted(() => window.addEventListener('keydown', onWindowKeydown))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onWindowKeydown)
  cancelSearch()
})

defineExpose({ show, close })
</script>
