<template>
  <header class="bg-base-100 border-b border-base-300 sticky top-0 z-[var(--z-header)]">
    <!-- Spec 109 FR-053: one row that never overflows —
         [☰] [search] [viewing] ......... [status pill] [attention] [+ Add].
         The right cluster is always rendered (it used to vanish below md, so a
         phone had no status at all); text collapses below 1100px instead. -->
    <div class="flex items-center gap-2 px-3 sm:px-6 py-3 min-w-0" data-test="header-row">
      <!-- Mobile menu toggle -->
      <label
        for="sidebar-drawer"
        class="btn btn-ghost btn-square lg:hidden shrink-0"
        aria-label="Open navigation menu"
        data-test="header-drawer-toggle"
      >
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
        </svg>
      </label>

      <!-- Search. A launcher, not a form: focusing it opens the command
           palette (Spec 109 FR-054), seeded with anything already typed.
           Below 1100px it collapses to an icon button. -->
      <div class="min-w-0 min-[1100px]:flex-1 min-[1100px]:max-w-xl">
        <div class="relative hidden min-[1100px]:block">
          <input
            v-model="searchQuery"
            type="text"
            placeholder="Search servers, tools, settings…"
            class="input input-bordered w-full pr-16"
            aria-label="Search servers, tools, settings"
            aria-keyshortcuts="Meta+K Control+K"
            data-test="header-search-input"
            @focus="openPalette"
            @click="openPalette"
          />
          <kbd
            class="kbd kbd-sm absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none"
            data-test="header-search-hint"
          >{{ shortcutHint }}</kbd>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-square min-[1100px]:hidden"
          aria-label="Search"
          aria-keyshortcuts="Meta+K Control+K"
          data-test="header-search-icon"
          @click="openPalette"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
        </button>
      </div>

      <!-- "Viewing" slot (Spec 109 owns the slot, Spec 108 fills it). App.vue
           passes the ViewingFilter chip (Spec 108-i FR-044); the chip hides
           itself for a tenant and while there is nothing to filter by, and the
           wrapper collapses to nothing when it does (no empty gap). -->
      <div v-if="$slots.viewing" class="shrink-0 empty:hidden" data-test="header-viewing-slot">
        <slot name="viewing" />
      </div>

      <div class="ml-auto flex items-center gap-2 shrink-0">
        <!-- Spec 107 FR-041: a tenant never loads the fleet-wide servers store
             and cannot add through admin doors, so neither control renders. -->
        <StatusPill v-if="!isTenant" />

        <!-- Needs-attention pill (Spec 109 FR-001/FR-003): hidden at 0, the
             same FR-001 list/count every surface reads. Popover shows the
             first 5 items; "See all" opens Home, where the full list lives. -->
        <div v-if="attentionStore.count > 0" class="relative" data-test="header-attention-pill">
          <button
            @click="showAttentionPopover = !showAttentionPopover"
            class="flex items-center gap-2 px-3 py-2 bg-warning/10 text-warning rounded-lg cursor-pointer hover:bg-warning/20 transition-colors"
            data-test="header-attention-pill-button"
            :aria-label="`${attentionStore.count} items need attention`"
          >
            <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
            </svg>
            <span class="font-bold">{{ attentionStore.count }}</span>
            <span class="text-xs hidden min-[1100px]:inline whitespace-nowrap">needs attention</span>
          </button>
          <div
            v-if="showAttentionPopover"
            class="absolute right-0 top-full mt-2 p-3 shadow-lg bg-base-100 rounded-box w-80 max-w-[calc(100vw-1.5rem)] border border-base-300 z-[var(--z-dropdown)]"
            data-test="header-attention-popover"
          >
            <div class="space-y-1">
              <router-link
                v-for="item in attentionStore.items.slice(0, 5)"
                :key="item.id"
                :to="item.fix.target"
                class="block px-2 py-1.5 rounded hover:bg-base-200 text-sm truncate"
                :data-test="`header-attention-item-${item.id}`"
                @click="showAttentionPopover = false"
              >
                {{ item.summary }}
              </router-link>
            </div>
            <router-link
              to="/"
              class="btn btn-xs btn-ghost w-full mt-2"
              data-test="header-attention-see-all"
              @click="showAttentionPopover = false"
            >
              See all
            </router-link>
          </div>
          <!-- Click-outside overlay -->
          <div v-if="showAttentionPopover" class="fixed inset-0 z-[calc(var(--z-dropdown)-1)]" @click="showAttentionPopover = false" />
        </div>

        <AddMenu v-if="!isTenant" />
      </div>
    </div>

    <CommandPalette ref="palette" v-model:open="paletteOpen" />
  </header>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useSystemStore } from '@/stores/system'
import { useAuthStore } from '@/stores/auth'
import { useAttentionStore } from '@/stores/attention'
import StatusPill from './StatusPill.vue'
import AddMenu from './AddMenu.vue'
import CommandPalette from './CommandPalette.vue'

const systemStore = useSystemStore()
const authStore = useAuthStore()
const attentionStore = useAttentionStore()

const showAttentionPopover = ref(false)
const searchQuery = ref('')
const paletteOpen = ref(false)
const palette = ref<InstanceType<typeof CommandPalette> | null>(null)

const isTenant = computed(() => authStore.principalKind === 'tenant')
const shortcutHint = computed(() =>
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K',
)

// The header field is only a launcher. Hand any typed text to the palette,
// clear the field and blur it first so closing the palette does not restore
// focus here and reopen it.
function openPalette(event?: Event) {
  const seed = searchQuery.value
  searchQuery.value = ''
  ;(event?.target as HTMLElement | null)?.blur?.()
  palette.value?.show(seed)
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
