<template>
  <section class="space-y-3" aria-labelledby="profile-tools-heading" data-test="profile-tool-table">
    <h2 id="profile-tools-heading" class="font-semibold">Tools under this profile</h2>
    <p v-if="counts" class="text-sm opacity-70" data-test="profile-tool-counts"><template v-if="unsaved">Saved profile: </template>{{ counts.visible }} allowed by profile &middot; {{ counts.hidden }} hidden<template v-if="isAdmin"> &middot; {{ callableCount }} callable now &middot; {{ heldCount }} held</template></p>
    <p v-if="counts" class="text-xs opacity-70" data-test="profile-tool-semantics">
      <strong>Allowed by profile</strong> is this profile's policy only (servers, rules, tier cap).
      <template v-if="isAdmin"><strong>Callable now</strong> also passes the later gates; a <strong>Held</strong> tool is allowed here but waiting on one (for example tool approval or server state), so an agent cannot call it yet.</template>
      <template v-else>Later gates, such as tool approval, can still hold an allowed tool.</template>
    </p>
    <p v-if="unsaved" class="text-xs text-warning" data-test="profile-tool-unsaved-note">Counts and access show the saved profile. Save to update them, or use Try it below to test your unsaved edits.</p>

    <div class="flex flex-wrap gap-2" data-test="profile-tool-filters">
      <label class="sr-only" for="tool-filter-server">Filter by server</label>
      <select id="tool-filter-server" v-model="serverFilter" class="select select-bordered select-xs" data-test="tool-filter-server">
        <option value="">All servers</option>
        <option v-for="name in serverNames" :key="name" :value="name">{{ name }}</option>
      </select>
      <label class="sr-only" for="tool-filter-reason">Filter by access</label>
      <select id="tool-filter-reason" v-model="reasonFilter" class="select select-bordered select-xs" data-test="tool-filter-reason">
        <option value="">Any access</option>
        <option value="visible">Allowed by profile</option>
        <template v-if="isAdmin || reasonFilter === 'callable' || reasonFilter === 'held'">
          <option value="callable">Callable now</option>
          <option value="held">Held</option>
        </template>
        <option v-for="reason in REASONS" :key="reason" :value="reason">{{ reasonText(reason) }}</option>
      </select>
      <label class="sr-only" for="tool-filter-search">Search tools</label>
      <input id="tool-filter-search" v-model.trim="search" class="input input-bordered input-xs" placeholder="Search tools" data-test="tool-filter-search" />
    </div>

    <p v-if="loading" class="flex items-center gap-2 text-sm" aria-busy="true" data-test="profile-tool-loading"><span class="loading loading-spinner loading-sm" /> Loading tools&hellip;</p>
    <p v-else-if="!serversChosen" class="text-sm opacity-70" data-test="profile-tool-no-servers">Choose servers to see their tools</p>
    <p v-else-if="!rows.length" class="text-sm opacity-70" data-test="profile-tool-none">No tools indexed for these servers yet</p>

    <ul v-if="orphanStale.length" class="text-sm space-y-1" data-test="profile-stale-orphans">
      <li v-for="key in orphanStale" :key="key" class="flex flex-wrap items-center gap-2">
        <code class="text-xs">{{ key }}</code> <span :data-test="`profile-stale-orphan-note-${key}`">{{ orphanNote(key) }}</span>
        <button v-if="editable" type="button" class="btn btn-xs btn-outline" @click="emit('remove-classification', key)">Remove classification</button>
      </li>
    </ul>

    <!-- The table scrolls inside its container so the page never scrolls sideways. -->
    <div v-if="visibleRows.length" class="overflow-x-auto rounded-box border border-base-300">
      <table class="table table-sm">
        <thead>
          <!-- daisyUI mutes table headers; full text colour keeps them AA on base-200. -->
          <tr>
            <th scope="col" class="text-base-content">Tool</th>
            <th scope="col" class="text-base-content">Tier</th>
            <th scope="col" class="text-base-content">Access</th>
            <th scope="col" class="text-base-content">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in visibleRows"
            :key="key(row)"
            :class="focusedKey === key(row) && 'ring-2 ring-primary ring-inset bg-primary/10'"
            :aria-current="focusedKey === key(row) ? 'true' : undefined"
            :data-test="`profile-tool-row-${toolRowId(row)}`"
            :ref="el => setRow(key(row), el as HTMLElement | null)"
          >
            <td class="font-mono text-xs break-all">{{ row.server }}:{{ row.tool }}</td>
            <td class="text-xs whitespace-nowrap">{{ row.intrinsic_tier }}<template v-if="row.profile_tier && row.profile_tier !== row.intrinsic_tier"> &rarr; {{ row.profile_tier }}</template></td>
            <td class="text-xs">
              <span class="badge badge-sm whitespace-nowrap" :class="stateClass(row)" :data-test="`profile-tool-state-${toolRowId(row)}`">{{ stateLabel(row) }}</span>
              <span v-if="!row.access.visible || isHeld(row)" class="ml-1" :data-test="`profile-tool-reason-${toolRowId(row)}`">{{ reasonText(row.access.reason) }}</span>
              <button
                v-if="isHeld(row) && profileName"
                type="button"
                class="btn btn-ghost btn-xs ml-1 min-h-6"
                :aria-label="`Explain access to ${key(row)}`"
                :data-test="`profile-tool-explain-${toolRowId(row)}`"
                @click="explainTool = key(row)"
              >Explain access</button>
              <p v-if="row.classification_stale" class="mt-1 text-warning" :data-test="`profile-stale-${toolRowId(row)}`">classification ignored &mdash; tool is now annotated</p>
            </td>
            <td>
              <div v-if="editable" class="flex flex-wrap items-center gap-2">
                <label class="flex items-center gap-1 text-xs cursor-pointer">
                  <input
                    type="checkbox"
                    class="toggle toggle-xs"
                    :checked="isListed('allow', row)"
                    :aria-label="`Allow ${key(row)} in ${profileLabel}`"
                    :data-test="`tool-allow-${toolRowId(row)}`"
                    :ref="el => setToggle(key(row), el as HTMLInputElement | null)"
                    @change="emit('toggle', { list: 'allow', key: key(row) })"
                  />Allow
                </label>
                <label class="flex items-center gap-1 text-xs cursor-pointer">
                  <input
                    type="checkbox"
                    class="toggle toggle-xs"
                    :checked="isListed('deny', row)"
                    :aria-label="`Deny ${key(row)} in ${profileLabel}`"
                    :data-test="`tool-deny-${toolRowId(row)}`"
                    @change="emit('toggle', { list: 'deny', key: key(row) })"
                  />Deny
                </label>
                <template v-if="row.intrinsic_tier === 'unannotated'">
                  <label class="sr-only" :for="`classify-${toolRowId(row)}`">Classify {{ key(row) }}</label>
                  <select
                    :id="`classify-${toolRowId(row)}`"
                    class="select select-bordered select-xs"
                    :value="classified(row)"
                    :data-test="`tool-classify-${toolRowId(row)}`"
                    @change="emit('classify', { key: key(row), tier: ($event.target as HTMLSelectElement).value })"
                  >
                    <option value="">Classify&hellip;</option>
                    <option value="read">Mark as read</option>
                    <option value="write">Mark as write</option>
                    <option value="destructive">Mark as destructive</option>
                  </select>
                </template>
                <button v-if="row.classification_stale" type="button" class="btn btn-xs btn-outline" :data-test="`tool-remove-classification-${toolRowId(row)}`" @click="emit('remove-classification', key(row))">Remove classification</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="hiddenOnly" class="text-sm opacity-70" data-test="profile-tool-hidden-count">{{ hiddenOnly }} hidden</p>
    <AccessExplainer v-if="profileName" :open="explainTool !== ''" :subject="{ kind: 'profile', name: profileName }" :tool="explainTool || undefined" @close="explainTool = ''" />
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { reasonText, toolKey, toolRowId } from '@/utils/profiles'
import AccessExplainer from '@/components/AccessExplainer.vue'
import type { EffectiveTool, EffectiveToolsResult, ProfileToolRules } from '@/types/api'

// Spec 108-i I18 / FR-041: the per-tool table of a profile, from
// GET /profiles/{name}/effective-tools (administrators see hidden rows with
// their reason; anyone else gets visible rows and a hidden count). Allow, Deny
// and Classify edit the editor's DRAFT through events; nothing here writes.
const props = defineProps<{
  rows: EffectiveTool[]
  counts?: EffectiveToolsResult['counts']
  stale?: string[]
  staleReasons?: Record<string, string>
  draftRules: ProfileToolRules | undefined
  profileLabel: string
  // The saved profile's name; enables "Explain access" on held tools.
  profileName?: string
  serversChosen: boolean
  loading?: boolean
  editable?: boolean
  focusKey?: string
  // The draft differs from the saved profile; the counts and access column
  // are the SAVED profile's evaluation (FR-041), so say so.
  unsaved?: boolean
  // Spec 115 UI-007: the deep link `?reason=callable` opens the table filtered
  // to the callable (visible) tools.
  initialReason?: string
}>()
const emit = defineEmits<{
  (e: 'toggle', payload: { list: 'allow' | 'deny'; key: string }): void
  (e: 'classify', payload: { key: string; tier: string }): void
  (e: 'remove-classification', key: string): void
}>()

const REASONS = ['server_not_in_profile', 'denied_by_rule', 'unannotated_hidden', 'above_tier_cap']
const serverFilter = ref('')
// `callable` is its own filter: a visible tool can still be uncallable
// (pending approval, disabled server), and the deep link promises the
// callable set (Spec 115 UI-007).
const reasonFor = (r: string | undefined) => r ?? ''
const reasonFilter = ref(reasonFor(props.initialReason))
// A query-only navigation reuses the mounted table: follow the deep link.
watch(() => props.initialReason, r => { reasonFilter.value = reasonFor(r) })
const search = ref('')
const focusedKey = ref('')
const toggles = new Map<string, HTMLInputElement>()
const rowEls = new Map<string, HTMLElement>()

const key = toolKey
const explainTool = ref('')
// counts.callable is administrator-only: its presence is the signal that the
// held/callable split (and a held tool's reason) may be shown to this caller.
const isAdmin = computed(() => props.counts?.callable !== undefined && props.counts?.callable !== null)
const callableCount = computed(() => props.counts?.callable ?? 0)
const heldCount = computed(() => Math.max(0, (props.counts?.visible ?? 0) - callableCount.value))
const isHeld = (row: EffectiveTool): boolean => isAdmin.value && row.access.visible && !row.access.callable
function stateLabel(row: EffectiveTool): string {
  if (!row.access.visible) return 'Hidden'
  if (!isAdmin.value) return 'Allowed by profile'
  return row.access.callable ? 'Callable now' : 'Held'
}
function stateClass(row: EffectiveTool): string {
  if (!row.access.visible) return 'badge-ghost'
  return isHeld(row) ? 'badge-warning' : 'badge-success'
}
const serverNames = computed(() => [...new Set(props.rows.map(row => row.server))].sort())
const visibleRows = computed(() => props.rows.filter(row => {
  if (serverFilter.value && row.server !== serverFilter.value) return false
  if (reasonFilter.value === 'visible' && !row.access.visible) return false
  if (reasonFilter.value === 'callable' && !(row.access.visible && row.access.callable)) return false
  if (reasonFilter.value === 'held' && !isHeld(row)) return false
  if (reasonFilter.value && !['visible', 'callable', 'held'].includes(reasonFilter.value) && row.access.reason !== reasonFilter.value) return false
  if (search.value && !`${row.server}:${row.tool}`.toLowerCase().includes(search.value.toLowerCase())) return false
  return true
}))
// A non-administrator gets visible rows only, plus a count of the rest.
const hiddenOnly = computed(() => (props.rows.some(row => !row.access.visible) ? 0 : props.counts?.hidden ?? 0))
const orphanStale = computed(() => (props.stale ?? []).filter(entry => !props.rows.some(row => key(row) === entry)))

// Same wording as `mcpproxy profile show --effective`. The server's reason map
// is authoritative; an older daemon sends none, so the note stays neutral.
function orphanNote(entry: string): string {
  const reason = props.staleReasons?.[entry]
  if (reason === 'annotated') return 'classification ignored \u2014 tool is now annotated'
  if (reason === 'missing') return 'classification ignored \u2014 tool not found'
  return 'classification ignored'
}

function isListed(list: 'allow' | 'deny', row: EffectiveTool): boolean {
  return (props.draftRules?.[list] ?? []).includes(key(row))
}
function classified(row: EffectiveTool): string {
  return props.draftRules?.classify?.[key(row)] ?? ''
}
function setRow(k: string, el: HTMLElement | null) {
  if (el) rowEls.set(k, el)
  else rowEls.delete(k)
}
function setToggle(k: string, el: HTMLInputElement | null) {
  if (el) toggles.set(k, el)
  else toggles.delete(k)
}

// `?focus=server:tool`: scroll to the row, ring it, focus its Allow toggle.
watch(() => [props.focusKey, props.rows.length] as const, async ([focus]) => {
  if (!focus || !props.rows.some(row => key(row) === focus)) return
  focusedKey.value = focus
  serverFilter.value = ''
  reasonFilter.value = ''
  search.value = ''
  await Promise.resolve()
  rowEls.get(focus)?.scrollIntoView?.({ block: 'center' })
  toggles.get(focus)?.focus()
}, { immediate: true, flush: 'post' })
</script>
