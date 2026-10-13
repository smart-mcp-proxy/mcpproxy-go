<template>
  <div v-if="items.length > 0" class="flex flex-wrap items-center gap-2" data-test="scope-chips">
    <span
      v-for="item in items"
      :key="item.name"
      class="badge badge-sm gap-1 h-auto min-h-6 py-1 whitespace-normal text-left"
      :class="item.note ? 'badge-ghost' : item.conflicting ? 'badge-warning' : 'badge-outline'"
      :aria-disabled="item.note ? 'true' : undefined"
      :data-conflicting="item.conflicting ? 'true' : undefined"
      :data-test="item.na ? `scope-chip-na-${item.name}` : `scope-chip-${item.name}`"
    >
      <span>{{ item.label }}</span>
      <span v-if="item.note" class="text-xs" data-test="scope-chip-note">&mdash; {{ item.note }}</span>
      <button
        type="button"
        class="btn btn-ghost btn-xs btn-circle min-h-6 h-6 w-6"
        :aria-label="`Remove ${item.label} filter`"
        :data-test="`scope-chip-remove-${item.name}`"
        @click="item.remove()"
      >
        <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useScopeLabels, type ScopeName } from '@/composables/useScopeLabels'
import {
  isScopeParamAvailable,
  scopeParamAppliesToPage,
  type PageId,
  type UseScopeQueryResult,
} from '@/composables/useScopeQuery'

// Spec 108-j J4 (url-filter-contract.md rules 3, 5, 7): the removable chips for
// the profile, client and token filters of a page. The values come from
// useScopeQuery's chips (which already hide a parameter the build does not
// advertise); this adds the human labels, the disabled chips and the remove
// buttons.
//
//  - `disabled` maps a param name to the sentence explaining why it is NOT
//    applied on this page right now (Tools: "Unattributed applies to Activity
//    and Usage only"); the chip renders disabled with that text and can still
//    be removed.
//  - A sticky param that is in the URL and available but that this page does
//    not register (today `token` on Tools) renders as a "not applicable here"
//    chip (rule 5), never silently ignored.
//  - `conflicting` names the params whose chips are marked as the pair that
//    can never both be satisfied (rule 8).
const props = defineProps<{
  page: PageId
  scopeQuery: UseScopeQueryResult
  disabled?: Record<string, string>
  conflicting?: string[]
  /** Names present in the URL that the build does not advertise AND whose
   * features never arrived in time (J3): a disabled "Filter unavailable on this
   * server" chip, so the operator sees the URL's filter was not applied. */
  unavailable?: string[]
}>()

const SCOPE_NAMES = ['profile', 'client', 'token'] as const

const route = useRoute()
const { chipLabel } = useScopeLabels()

interface Item {
  name: ScopeName
  label: string
  note?: string
  na?: boolean
  conflicting?: boolean
  remove: () => void
}

const items = computed<Item[]>(() => {
  const out: Item[] = []
  for (const chip of props.scopeQuery.chips.value) {
    if (!(SCOPE_NAMES as readonly string[]).includes(chip.name)) continue
    const name = chip.name as ScopeName
    out.push({
      name,
      label: chipLabel(name, chip.value),
      note: props.disabled?.[name],
      conflicting: props.conflicting?.includes(name),
      remove: chip.remove,
    })
  }
  for (const name of SCOPE_NAMES) {
    if (out.some(item => item.name === name)) continue
    const raw = route.query[name]
    const value = Array.isArray(raw) ? raw[0] : raw
    if (typeof value !== 'string' || value === '') continue
    if (!isScopeParamAvailable(name) || scopeParamAppliesToPage(name, props.page)) continue
    out.push({
      name,
      label: chipLabel(name, value),
      note: 'not applicable here',
      na: true,
      remove: () => props.scopeQuery.clear([name]),
    })
  }
  for (const name of props.unavailable ?? []) {
    if (!(SCOPE_NAMES as readonly string[]).includes(name) || out.some(item => item.name === name)) continue
    const raw = route.query[name]
    const value = Array.isArray(raw) ? raw[0] : raw
    if (typeof value !== 'string' || value === '') continue
    out.push({
      name: name as ScopeName,
      label: chipLabel(name as ScopeName, value),
      note: 'Filter unavailable on this server',
      na: true,
      remove: () => props.scopeQuery.clear([name]),
    })
  }
  return out
})
</script>
