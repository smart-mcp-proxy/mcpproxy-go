<template>
  <div v-if="items.length > 0" class="flex flex-wrap items-center gap-2 mb-4" data-test="carried-scope-chips">
    <span
      v-for="item in items"
      :key="item.name"
      class="badge badge-sm badge-ghost gap-1 h-auto min-h-6 py-1 whitespace-normal text-left"
      data-not-applied="true"
      :data-test="`carried-scope-chip-${item.name}`"
    >
      <span>{{ item.label }}</span>
      <span class="text-xs">&mdash; not applied on this page</span>
      <button
        type="button"
        class="btn btn-ghost btn-xs btn-circle min-h-6 h-6 w-6"
        :aria-label="`Remove ${item.label} filter (${item.name})`"
        :data-test="`carried-scope-remove-${item.name}`"
        @click="remove(item.name)"
      >
        <UiIcon name="x" class="w-3 h-3" />
      </button>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UiIcon from '@/components/UiIcon.vue'
import { useScopeLabels, type ScopeName } from '@/composables/useScopeLabels'
import { pageIdForRouteName, scopeParamAppliesToPage } from '@/composables/useScopeQuery'

// Spec 134 FR-016: the view-as values (profile, client, token) travel between
// pages in the URL. On a page that applies none of them (Home, Settings,
// Review, a server, the Clients Endpoint tab) there is no filter row to show
// them in, so the shell shows each as a removable chip marked "not applied on
// this page". Pages that apply any of them show their own chips (ScopeChips),
// which mark the ones they do not apply the same way.
const NAMES: ScopeName[] = ['profile', 'client', 'token']

const route = useRoute()
const router = useRouter()
const { valueLabel } = useScopeLabels()

const page = computed(() => pageIdForRouteName(route.name, route.query.tab))
const pageHasScope = computed(() => NAMES.some((name) => scopeParamAppliesToPage(name, page.value)))

function queryValue(name: string): string {
  const raw = route.query[name]
  const value = Array.isArray(raw) ? raw[0] : raw
  return typeof value === 'string' ? value : ''
}

const items = computed(() => {
  if (pageHasScope.value) return []
  // Shown even when the build does not list the filter: such a value is not
  // applied anywhere, and it must still be visible and removable.
  return NAMES.filter((name) => queryValue(name) !== '').map((name) => {
    const value = queryValue(name)
    const label = name === 'token' || value === '-'
      ? `${name === 'profile' ? 'Profile' : name === 'client' ? 'Client' : 'Token'}: ${valueLabel(name, value)}`
      : `Viewing as ${valueLabel(name, value)}`
    return { name, label }
  })
})

function remove(name: ScopeName) {
  const query = { ...route.query }
  delete query[name]
  void router.replace({ query, hash: route.hash })
}
</script>
