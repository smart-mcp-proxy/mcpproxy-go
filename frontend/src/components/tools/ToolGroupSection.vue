<template>
  <section class="border-t border-[var(--ds-border)] first:border-t-0" :data-test="`tools-section-${group.server}`" data-test-section>
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1 bg-base-200/40" data-test="tools-section-header">
      <button
        type="button"
        class="btn btn-ghost btn-sm min-h-11 min-w-11 px-2"
        :aria-expanded="expanded"
        :aria-controls="bodyId"
        :aria-label="`${expanded ? 'Collapse' : 'Expand'} ${group.server}`"
        data-test="tools-section-toggle"
        @click="emit('toggle')"
      >
        <UiIcon name="chevronDown" class="w-4 h-4 motion-safe:transition-transform" :class="expanded ? '' : '-rotate-90'" />
      </button>
      <h3 class="font-semibold text-sm min-w-0 break-all">
        <router-link :to="serverDetailPath(group.server)" class="link link-primary" data-test="tools-section-server-link">{{ group.server }}</router-link>
      </h3>
      <StatusPill v-if="info?.status" :status="info.status" :level="info.level" />
      <span class="text-xs text-base-content/70" data-test="tools-section-count">
        {{ group.total }} tool{{ group.total === 1 ? '' : 's' }}<template v-if="group.tools.length < group.total"> · {{ group.tools.length }} on this page</template>
      </span>
    </div>
    <ul v-show="expanded" :id="bodyId" class="divide-y divide-[var(--ds-border)]" data-test="tools-section-body">
      <li
        v-for="tool in group.tools"
        :key="toolKey(tool)"
        class="flex items-start gap-2 px-3 py-2"
        :class="{ 'bg-primary/5': selectedKeys.has(toolKey(tool)), 'bg-base-200/70': viewAsAdmin && tool.access?.callable === false }"
        :data-not-callable="viewAsAdmin && tool.access?.callable === false ? 'true' : undefined"
        data-test="tool-row"
      >
        <input
          type="checkbox"
          class="checkbox checkbox-sm mt-2 shrink-0"
          :aria-label="`Select tool ${tool.name}`"
          :checked="selectedKeys.has(toolKey(tool))"
          :disabled="viewAsActive"
          :title="viewAsActive ? leaveViewAs : undefined"
          data-test="tool-select"
          @change="emit('select', tool)"
        />
        <!-- Phone: name, labels, description, then last used and Details stacked.
             Desktop: one line, the description takes the free width. -->
        <div class="min-w-0 flex-1 flex flex-col gap-1 md:flex-row md:items-center md:gap-3">
          <span class="min-w-0 md:w-56 md:shrink-0 self-start md:self-center"><code class="text-xs bg-base-200 px-1.5 py-0.5 rounded break-all md:break-normal md:inline-block md:max-w-full md:truncate md:align-middle" :title="tool.name" data-test="tool-name">{{ tool.name }}</code></span>
          <p class="text-sm text-base-content/70 truncate min-w-0 md:flex-1 order-last md:order-none" :title="tool.description || undefined" data-test="tool-description">{{ tool.description || '—' }}</p>
          <div class="flex flex-wrap items-center gap-1 md:justify-end md:shrink-0">
            <ToolExceptionLabels :tool="tool" />
            <span v-if="viewAsAdmin && tool.access?.callable === false" class="text-xs text-base-content/70 whitespace-nowrap" data-test="tool-not-callable">Not callable by this subject</span>
          </div>
          <div class="flex items-center gap-3 text-xs text-base-content/60 md:shrink-0">
            <span class="whitespace-nowrap md:w-28" data-test="tool-last-used">Last used: <template v-if="tool.last_used">{{ formatRelativeTime(tool.last_used) }}</template><template v-else>never</template></span>
            <button
              type="button"
              class="btn btn-outline btn-sm min-h-11 sm:min-h-8"
              :aria-label="`Details for ${tool.name}`"
              data-test="tool-details-button"
              @click="emit('details', tool, $event.currentTarget as HTMLElement)"
            >Details</button>
          </div>
        </div>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { useId } from 'vue'
import StatusPill from '@/components/StatusPill.vue'
import UiIcon from '@/components/UiIcon.vue'
import ToolExceptionLabels from '@/components/tools/ToolExceptionLabels.vue'
import type { GlobalTool } from '@/types/api'
import { serverDetailPath } from '@/utils/serverRoute'
import { formatRelativeTime, type SectionServerInfo } from '@/utils/toolsGrouping'

// Spec 134 FR-055/FR-056: one collapsible section of the grouped list. Built
// only from rows this caller was sent; `info` is the server's health when the
// caller can read it, else absent (no pill).
defineProps<{
  group: { server: string; tools: GlobalTool[]; total: number }
  info?: SectionServerInfo & { status?: string | null }
  expanded: boolean
  selectedKeys: Set<string>
  toolKey: (tool: GlobalTool) => string
  viewAsActive: boolean
  viewAsAdmin: boolean
  leaveViewAs: string
}>()
const emit = defineEmits<{
  (e: 'toggle'): void
  (e: 'select', tool: GlobalTool): void
  (e: 'details', tool: GlobalTool, opener: HTMLElement): void
}>()

// A server name may differ from another only by a character an id cannot hold.
const bodyId = `tools-section-body-${useId()}`
</script>
