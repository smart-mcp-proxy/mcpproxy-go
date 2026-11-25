<template>
  <header class="bg-base-100 border-b border-base-300 sticky top-0 z-[var(--z-header)]">
    <!-- Spec 134 FR-011/FR-014: one row that never overflows —
         [menu] [logo] [search] ......... [online] [N need attention] [Add server] [+ Add].
         "View as" lives in the filter row of the pages it applies to (FR-016).
         The right cluster is always rendered; text collapses below 1100px. -->
    <div class="flex items-center gap-2 px-3 sm:px-4 min-[1280px]:px-6 py-3 min-w-0" data-test="header-row">
      <!-- Mobile menu toggle: a real button (Spec 134 FR-014). -->
      <button
        type="button"
        class="btn btn-ghost btn-square lg:hidden shrink-0"
        aria-label="Open navigation menu"
        aria-controls="sidebar-panel"
        :aria-expanded="drawerOpen ? 'true' : 'false'"
        data-test="header-drawer-toggle"
        @click="drawerOpen = !drawerOpen"
      >
        <UiIcon name="menu" class="w-6 h-6" />
      </button>

      <router-link
        to="/"
        class="lg:hidden shrink-0"
        aria-label="MCPProxy home"
        data-test="header-logo"
      >
        <img src="/src/assets/logo.svg" alt="" class="w-8 h-8" />
      </router-link>

      <!-- Search. A launcher: a button that opens the command palette (FR-013).
           It never opens on focus or Tab. Below 1100px it is an icon button. -->
      <div class="min-w-0 min-[1100px]:flex-1 min-[1100px]:max-w-xl">
        <button
          type="button"
          class="hidden min-[1100px]:flex w-full items-center gap-2 input input-bordered text-left text-base-content/60 cursor-pointer"
          aria-label="Search servers, tools, settings"
          aria-keyshortcuts="Meta+K Control+K"
          data-test="header-search-input"
          @click="openPalette"
        >
          <UiIcon name="search" class="w-4 h-4 shrink-0" />
          <span class="grow truncate">Search servers, tools, settings…</span>
          <kbd class="kbd kbd-sm" data-test="header-search-hint">{{ shortcutHint }}</kbd>
        </button>
        <button
          type="button"
          class="btn btn-ghost btn-square min-[1100px]:hidden"
          aria-label="Search"
          aria-keyshortcuts="Meta+K Control+K"
          data-test="header-search-icon"
          @click="openPalette"
        >
          <UiIcon name="search" class="w-5 h-5" />
        </button>
      </div>

      <div class="ml-auto flex items-center gap-2 shrink-0">
        <!-- Spec 107 FR-041: a tenant never loads the fleet-wide servers store
             and cannot add through admin doors, so neither control renders. -->
        <HeaderStatus v-if="!isTenant" />

        <!-- "N need attention" (Spec 109 FR-001/FR-003, Spec 134 FR-011): hidden
             at 0; the popover lists every item with its fix, then "See all". -->
        <div v-if="attentionStore.count > 0" class="relative" data-test="header-attention-pill" @keydown.esc.stop="closeAttention(true)" @focusout="onAttentionFocusOut">
          <button
            ref="attentionButton"
            type="button"
            @click="showAttentionPopover = !showAttentionPopover"
            class="flex items-center gap-2 px-3 py-2 min-h-10 bg-warning/10 text-warning rounded cursor-pointer hover:bg-warning/20 transition-colors"
            data-test="header-attention-pill-button"
            aria-controls="header-attention-popover"
            :aria-expanded="showAttentionPopover ? 'true' : 'false'"
            :aria-label="`${attentionStore.count} need attention`"
          >
            <UiIcon name="alert" class="w-4 h-4 shrink-0" />
            <span class="font-bold">{{ attentionStore.count }}</span>
            <span class="text-xs hidden min-[1280px]:inline whitespace-nowrap">need attention</span>
          </button>
          <div
            v-if="showAttentionPopover"
            id="header-attention-popover"
            class="fixed left-3 right-3 top-[4.5rem] sm:absolute sm:left-auto sm:right-0 sm:top-full sm:mt-2 sm:w-80 p-3 shadow-lg bg-base-100 rounded-box border border-base-300 z-[var(--z-dropdown)]"
            data-test="header-attention-popover"
          >
            <div class="space-y-1 max-h-[60vh] overflow-y-auto">
              <router-link
                v-for="item in attentionStore.items"
                :key="item.id"
                :to="item.fix.target"
                class="block px-2 py-2 rounded hover:bg-base-200 text-sm"
                :data-test="`header-attention-item-${item.id}`"
                @click="closeAttention()"
              >
                <span class="block truncate">{{ item.summary }}</span>
                <span class="block text-xs text-base-content/60">{{ item.fix.label }}</span>
              </router-link>
            </div>
            <router-link
              to="/"
              class="btn btn-sm btn-ghost w-full mt-2"
              data-test="header-attention-see-all"
              @click="closeAttention()"
            >
              See all
            </router-link>
          </div>
          <!-- Click-outside overlay -->
          <div v-if="showAttentionPopover" class="fixed inset-0 z-[calc(var(--z-dropdown)-1)]" @click="closeAttention()" />
        </div>

        <!-- Spec 134 FR-011: the one primary header button. -->
        <router-link
          v-if="!isTenant"
          to="/add-server"
          class="btn btn-primary shrink-0"
          aria-label="Add server"
          data-test="header-add-server"
        >
          <UiIcon name="plus" class="w-4 h-4 min-[1280px]:hidden" />
          <span class="hidden min-[1280px]:inline">Add server</span>
        </router-link>
        <AddMenu v-if="!isTenant" />
      </div>
    </div>

    <CommandPalette ref="palette" v-model:open="paletteOpen" />
  </header>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, watch } from 'vue'
import { useSystemStore } from '@/stores/system'
import { useAuthStore } from '@/stores/auth'
import { useAttentionStore } from '@/stores/attention'
import HeaderStatus from './HeaderStatus.vue'
import UiIcon from './UiIcon.vue'
import { useSidebarDrawer } from '@/composables/useSidebarDrawer'
import AddMenu from './AddMenu.vue'
import CommandPalette from './CommandPalette.vue'

const systemStore = useSystemStore()
const authStore = useAuthStore()
const attentionStore = useAttentionStore()

const { drawerOpen } = useSidebarDrawer()
const showAttentionPopover = ref(false)
const attentionButton = ref<HTMLButtonElement | null>(null)

// Escape closes the popover and hands focus back to its button.
function closeAttention(returnFocus = false) {
  if (!showAttentionPopover.value) return
  showAttentionPopover.value = false
  if (returnFocus) void nextTick(() => attentionButton.value?.focus())
}
// Focus leaving the attention wrapper (Tab past "See all") closes the popover
// so its backdrop never stays up behind focus elsewhere.
function onAttentionFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null
  if (next && !(event.currentTarget as HTMLElement).contains(next)) closeAttention()
}
const paletteOpen = ref(false)
const palette = ref<InstanceType<typeof CommandPalette> | null>(null)

const isTenant = computed(() => authStore.principalKind === 'tenant')
const shortcutHint = computed(() =>
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K',
)

// The header search is only a launcher: the palette's own field takes typing.
function openPalette() {
  palette.value?.show('')
}

onMounted(() => {
  // Spec 109 FR-001/FR-003: the header is global, so it fetches its own copy
  // rather than depending on Home having mounted first.
  attentionStore.fetchAttention()
})

// #1401: the header now stays mounted through an auth recovery (it used to
// remount, which re-ran the load above), so reload on the recovery epoch.
watch(() => systemStore.authEpoch, () => {
  attentionStore.fetchAttention()
})
</script>
