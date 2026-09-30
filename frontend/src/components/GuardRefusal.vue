<template>
  <div :role="plain ? undefined : 'alert'" class="alert alert-warning flex-col items-start gap-2 text-sm" data-test="guard-refusal">
    <p class="font-medium">{{ refusal.error || defaultMessage }}</p>
    <ul v-if="refusal.bindings?.length" class="list-disc list-inside text-xs" data-test="guard-bindings">
      <li v-for="binding in refusal.bindings" :key="binding.token_name || binding.client_id">
        {{ binding.client_id || binding.token_name }} &rarr; {{ binding.profile }} ({{ binding.mode }})
      </li>
    </ul>
    <div v-if="refusal.fixes?.length" class="flex flex-wrap gap-2">
      <button
        v-for="fix in refusal.fixes"
        :key="fix.kind + (fix.target ?? '')"
        type="button"
        class="btn btn-xs btn-outline"
        :data-test="`guard-fix-${fix.kind}`"
        @click="follow(fix)"
      >{{ label(fix) }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import type { BindingRef, GuardFix } from '@/types/api'

// Spec 108-i I9 / FR-008a: a refused binding change. The two fixes only NAVIGATE
// to the Settings screen where the operator sees and confirms the change; this
// component never writes config itself.
// `plain` drops role=alert for a guard shown inside a status region (the
// Clients warnings banner); a refusal that blocked an action stays an alert.
defineProps<{ refusal: { error?: string; bindings?: BindingRef[]; fixes?: GuardFix[] }; plain?: boolean }>()
const emit = defineEmits<{ (e: 'navigate'): void }>()
const router = useRouter()
const defaultMessage = 'This change would leave a client bound to a profile reachable without authentication.'

function label(fix: GuardFix): string {
  if (fix.kind === 'require_mcp_auth') return 'Require authentication…'
  if (fix.kind === 'set_anonymous_profile') return `Set anonymous callers to ${fix.target ?? 'a profile'}…`
  return fix.kind
}

function follow(fix: GuardFix) {
  if (fix.kind === 'require_mcp_auth') {
    void router.push({ path: '/settings', query: { tab: 'security', focus: 'require_mcp_auth' } })
  } else if (fix.kind === 'set_anonymous_profile') {
    const query: Record<string, string> = { tab: 'security', focus: 'anonymous_profile' }
    if (fix.target) query.value = fix.target
    void router.push({ path: '/settings', query })
  }
  emit('navigate')
}
</script>
