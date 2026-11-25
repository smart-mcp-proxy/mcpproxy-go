<template>
  <div class="space-y-4" data-test="tool-details">
    <div v-if="tool.description">
      <h4 class="text-sm font-semibold mb-1 text-base-content/70">Description</h4>
      <p class="text-sm whitespace-pre-line break-words" data-test="tool-details-description">{{ tool.description }}</p>
    </div>

    <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
      <div>
        <dt class="text-base-content/60">Tier</dt>
        <dd data-test="tool-details-tier">
          <StateTag :tone="tierTone">{{ tierText }}</StateTag>
          <span v-if="viewAsAdmin && tool.profile_tier && tool.profile_tier !== tool.tier" class="ml-2 text-xs" data-test="tools-profile-tier">as {{ tool.profile_tier }}</span>
        </dd>
      </div>
      <div>
        <dt class="text-base-content/60">State</dt>
        <dd data-test="tool-details-state">
          <StateTag v-if="tool.config_denied" tone="error">config-denied</StateTag>
          <StateTag v-else-if="tool.disabled" tone="off">disabled</StateTag>
          <StateTag v-else tone="ok">enabled</StateTag>
        </dd>
      </div>
      <div>
        <dt class="text-base-content/60">Usage (30d)</dt>
        <dd data-test="tool-details-usage">
          <router-link v-if="tool.usage && callsLink" :to="callsLink" class="link" data-test="tool-calls-link">{{ tool.usage }} call{{ tool.usage === 1 ? '' : 's' }}</router-link>
          <span v-else-if="tool.usage">{{ tool.usage }} call{{ tool.usage === 1 ? '' : 's' }}</span>
          <span v-else>0 calls</span>
        </dd>
      </div>
      <div>
        <dt class="text-base-content/60">Last used</dt>
        <dd data-test="tool-details-last-used">
          <span v-if="tool.last_used">{{ formatRelativeTime(tool.last_used) }}</span>
          <span v-else>Never used</span>
        </dd>
      </div>
      <div v-if="tool.approval_status" class="sm:col-span-2">
        <dt class="text-base-content/60">Approval</dt>
        <dd class="flex flex-wrap items-center gap-2" data-test="tool-details-approval">
          <StateTag :tone="approvalTone" class="whitespace-nowrap">{{ toolApprovalLabel(tool.approval_status) }}</StateTag>
          <template v-if="approvable">
            <span
              class="badge badge-warning badge-outline badge-xs whitespace-nowrap"
              title="Enabled in configuration, but held: agents cannot call this tool until it is approved"
              data-test="tool-held-cue"
            >Held</span>
            <router-link
              :to="reviewPath(tool.server_name, tool.approval_status as 'pending' | 'changed')"
              class="link link-primary text-xs"
              data-test="tool-review-link"
            >Review</router-link>
          </template>
        </dd>
        <ToolHoldEvidence :tool="tool" />
      </div>
      <div v-if="viewAsAdmin" class="sm:col-span-2" data-test="tool-details-access">
        <dt class="text-base-content/60">Access</dt>
        <dd class="flex flex-wrap items-center gap-2">
          <StateTag :tone="accessTone" class="whitespace-nowrap">{{ accessText }}</StateTag>
          <span v-if="!callable" class="text-xs" data-test="tools-access-reason">{{ accessReasonLabel(tool.access?.reason, tool) }}</span>
          <button
            v-if="!callable"
            type="button"
            class="btn btn-outline btn-sm"
            :aria-label="`Why is ${tool.name} not callable?`"
            data-test="tool-details-why"
            @click="emit('why')"
          >Why?</button>
        </dd>
      </div>
    </dl>

    <div v-if="tool.annotations && Object.keys(tool.annotations).length > 0">
      <h4 class="text-sm font-semibold mb-1 text-base-content/70">Annotations</h4>
      <div class="flex flex-wrap gap-2" data-test="tool-details-annotations">
        <StateTag v-if="tool.annotations.readOnlyHint" tone="busy">readOnly</StateTag>
        <StateTag v-if="tool.annotations.destructiveHint" tone="error">destructive</StateTag>
        <StateTag v-if="tool.annotations.idempotentHint">idempotent</StateTag>
        <StateTag v-if="tool.annotations.openWorldHint">openWorld</StateTag>
      </div>
    </div>

    <div v-if="schema">
      <h4 class="text-sm font-semibold mb-1 flex items-center gap-2 text-base-content/70">
        Input Schema
        <span class="badge badge-sm badge-ghost">JSON</span>
      </h4>
      <div class="mockup-code max-h-64 overflow-auto">
        <pre class="text-xs"><code>{{ JSON.stringify(schema, null, 2) }}</code></pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import StateTag from '@/components/StateTag.vue'
import type { StatusTone } from '@/utils/healthTone'
import ToolHoldEvidence from '@/components/tools/ToolHoldEvidence.vue'
import type { GlobalTool } from '@/types/api'
import { accessReasonLabel, accessStateLabel } from '@/utils/accessReason'
import { reviewPath } from '@/utils/serverRoute'
import { tierLabel, toolApprovalLabel } from '@/utils/toolQuarantine'
import { formatRelativeTime, toolTier } from '@/utils/toolsGrouping'

// Spec 134 FR-056 / Spec 134 inventory P6-27: everything a tool row and its
// detail dialog show, in one place, for the grouped and the flat layout.
const props = defineProps<{
  tool: GlobalTool
  viewAsAdmin: boolean
  callsLink: RouteLocationRaw | null
}>()
const emit = defineEmits<{ (e: 'why'): void }>()

const tierText = computed(() => tierLabel(toolTier(props.tool)))
const tierTone = computed<StatusTone>(() => {
  const t = toolTier(props.tool)
  return t === 'destructive' ? 'error' : t === 'write' ? 'review' : t === 'read' ? 'ok' : 'neutral'
})
const approvalTone = computed<StatusTone>(() => {
  const s = props.tool.approval_status
  return s === 'approved' ? 'ok' : s === 'pending' ? 'review' : s === 'changed' ? 'error' : 'neutral'
})
const approvable = computed(() => props.tool.approval_status === 'pending' || props.tool.approval_status === 'changed')
const callable = computed(() => props.tool.access?.callable !== false)
const accessText = computed(() => accessStateLabel(props.tool.access ?? { visible: true, callable: true }))
const accessTone = computed<StatusTone>(() => (callable.value ? 'ok' : props.tool.access?.visible ? 'review' : 'error'))
const schema = computed(() => {
  const t = props.tool as unknown as { input_schema?: unknown; schema?: unknown }
  return t.input_schema || t.schema || null
})
</script>
