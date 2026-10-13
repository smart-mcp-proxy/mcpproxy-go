<template>
  <div v-if="warnings.length" role="status" aria-live="polite" class="space-y-2" data-test="clients-warnings-banner">
    <div
      v-for="(warning, index) in warnings"
      :key="`${warning.code}-${warning.client_id ?? ''}-${index}`"
      class="rounded-box border p-3 text-sm space-y-2"
      :class="warning.severity === 'warn' ? 'border-warning bg-warning/10' : 'border-info bg-info/10'"
      :data-test="`clients-warning-${warning.code}`"
    >
      <div class="flex flex-wrap items-start gap-2">
        <!-- The severity is text as well as colour and an icon. -->
        <span class="badge badge-sm" :class="warning.severity === 'warn' ? 'badge-warning' : 'badge-info'">
          <span aria-hidden="true">{{ warning.severity === 'warn' ? '⚠' : 'ℹ' }}</span>
          {{ warning.severity === 'warn' ? 'Warning' : 'Info' }}
        </span>
        <span class="flex-1 min-w-[12rem]">{{ warning.message }}</span>
        <button v-if="warning.action" type="button" class="btn btn-xs btn-outline" :data-test="`clients-warning-action-${warning.code}`" @click="act(warning)">{{ actionLabel(warning) }}</button>
      </div>
      <GuardRefusal
        v-if="warning.code === 'anonymous_denied_by_binding_guard' && (warning.bindings?.length || warning.fixes?.length)"
        plain
        :refusal="{ error: 'Bindings that would be reachable without authentication', bindings: warning.bindings, fixes: warning.fixes }"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import GuardRefusal from '@/components/GuardRefusal.vue'
import { useScopeQuery } from '@/composables/useScopeQuery'
import { CONNECT_CLIENT_EVENT } from '@/navigation/navModel'
import type { ClientWarning } from '@/types/api'

// Spec 108-i I10: the Clients warnings banner, one row per warning from GET
// /clients. The action button is mapped from action.kind; nothing here mutates
// anything - each action navigates, or opens the dialog that previews first.
defineProps<{ warnings: ClientWarning[] }>()
const emit = defineEmits<{ (e: 'upgrade'): void; (e: 'move', clientId: string): void }>()
const router = useRouter()
const scope = useScopeQuery('clients')

function actionLabel(warning: ClientWarning): string {
  switch (warning.action?.kind) {
    case 'change_setting': return 'Open setting'
    case 'upgrade_admin_key_holders': return 'Upgrade admin-key clients…'
    case 'reconnect_client': return 'Reconnect…'
    case 'move_client': return 'Move…'
    case 'edit_token': return 'Edit token'
    default: return 'Fix'
  }
}

function act(warning: ClientWarning) {
  const action = warning.action
  if (!action) return
  const target = action.target ?? ''
  switch (action.kind) {
    case 'change_setting':
      void router.push({ path: '/settings', query: { tab: 'security', focus: target } })
      break
    case 'upgrade_admin_key_holders':
      emit('upgrade')
      break
    case 'reconnect_client':
      window.dispatchEvent(new CustomEvent(CONNECT_CLIENT_EVENT, { detail: { client: target } }))
      break
    case 'move_client':
      emit('move', target)
      break
    case 'edit_token':
      void router.push(scope.linkTo('tokens', { token: target }))
      break
  }
}
</script>
