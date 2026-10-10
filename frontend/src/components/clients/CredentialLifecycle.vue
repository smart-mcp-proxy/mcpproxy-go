<template>
  <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs font-normal" :data-test="`credential-lifecycle-${id}`">
    <span v-if="binding" class="font-medium whitespace-nowrap" :data-test="`credential-binding-${id}`">{{ binding }}</span>
    <span v-if="issuer" class="opacity-70 whitespace-nowrap" :data-test="`credential-issuer-${id}`">{{ issuer }}</span>
    <span v-if="lease && !hideLease" class="opacity-80 whitespace-nowrap" :data-test="`credential-lease-${id}`">{{ lease }}</span>
    <span v-else-if="expiryText && !hideLease" class="opacity-80 whitespace-nowrap" :data-test="`credential-expiry-${id}`">{{ expiryText }}</span>
    <span
      v-if="showState"
      class="whitespace-nowrap"
      :class="state.tone === 'error' ? 'text-error font-medium' : state.tone === 'success' ? 'text-success' : 'opacity-70'"
      :title="state.detail"
      :data-test="`credential-state-${id}`"
    >{{ state.label === 'Revoked' ? state.detail : state.label }}</span>
    <span v-if="dangling" class="text-error whitespace-nowrap" :data-test="`credential-dangling-${id}`">Profile missing — deny-all</span>
    <details v-if="purpose" class="w-full" :data-test="`credential-purpose-${id}`" @click.stop>
      <summary class="cursor-pointer opacity-70">{{ PURPOSE_LABEL }}</summary>
      <!-- Text interpolation only: the purpose is caller prose, never markup. -->
      <p class="whitespace-pre-wrap break-words mt-1 opacity-80" data-test="credential-purpose-text">{{ purpose }}</p>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { PURPOSE_LABEL, bindingText, isLease, issuerText, leaseText, stateBadge } from '@/utils/credentials'
import { formatDateTimeShort } from '@/utils/datetime'
import { useNow } from '@/composables/useNow'
import type { CredentialIssuer } from '@/types/api'

// Spec 115 UI-001/UI-005: a worker credential's lifecycle on one line: how it
// is bound (Locked to / Pinned to), who issued it, its lease, its state
// (Revoked <time> wins over expired) and the stated purpose, labelled as not
// enforced. Shared by the Clients rows and the Tokens rows.
const props = defineProps<{
  id: string
  kind: 'client' | 'token'
  profile?: string
  mode?: string
  issuer?: CredentialIssuer | null
  lease?: boolean
  expiresAt?: string | null
  createdAt?: string | null
  revoked?: boolean
  revokedAt?: string | null
  credentialState?: string
  purpose?: string
  profileState?: string
  showState?: boolean
  // Tokens show the lease in their own Expires column.
  hideLease?: boolean
  now?: number
}>()

const clock = useNow()
const nowMs = computed(() => props.now ?? clock.value)
const fields = computed(() => ({
  revoked: props.revoked, revoked_at: props.revokedAt, expires_at: props.expiresAt,
  created_at: props.createdAt, lease: props.lease, credential_state: props.credentialState,
}))
const binding = computed(() => bindingText(props.kind, props.profile, props.mode))
const issuer = computed(() => issuerText(props.issuer))
const state = computed(() => stateBadge(fields.value, nowMs.value))
const lease = computed(() => (state.value.label === 'Revoked' || state.value.label === 'Lease ended' ? '' : leaseText(fields.value, nowMs.value)))
const dangling = computed(() => props.profileState === 'dangling')
// A non-lease credential still shows when it expires (UI-001).
const expiryText = computed(() => {
  if (lease.value || isLease(fields.value) || !props.expiresAt) return ''
  const past = new Date(props.expiresAt).getTime() <= nowMs.value
  return `${past ? 'Expired' : 'Expires'} ${formatDateTimeShort(props.expiresAt)}`
})
</script>
