<template>
  <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs font-normal" :data-test="`credential-lifecycle-${id}`">
    <span v-if="binding" class="badge badge-ghost badge-sm" :data-test="`credential-binding-${id}`">{{ binding }}</span>
    <span v-if="issuer" class="opacity-70" :data-test="`credential-issuer-${id}`">{{ issuer }}</span>
    <span v-if="lease" class="opacity-80" :data-test="`credential-lease-${id}`">{{ lease }}</span>
    <span
      v-if="showState"
      class="badge badge-sm"
      :class="state.tone === 'error' ? 'badge-error' : state.tone === 'success' ? 'badge-success' : 'badge-ghost'"
      :title="state.detail"
      :data-test="`credential-state-${id}`"
    >{{ state.label === 'Revoked' ? state.detail : state.label }}</span>
    <span v-if="dangling" class="badge badge-error badge-sm badge-outline" :data-test="`credential-dangling-${id}`">Profile missing — deny-all</span>
    <details v-if="purpose" class="w-full" :data-test="`credential-purpose-${id}`" @click.stop>
      <summary class="cursor-pointer opacity-70">{{ PURPOSE_LABEL }}</summary>
      <!-- Text interpolation only: the purpose is caller prose, never markup. -->
      <p class="whitespace-pre-wrap break-words mt-1 opacity-80" data-test="credential-purpose-text">{{ purpose }}</p>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { PURPOSE_LABEL, bindingText, issuerText, leaseText, stateBadge } from '@/utils/credentials'
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
  now?: number
}>()

const nowMs = computed(() => props.now ?? Date.now())
const fields = computed(() => ({
  revoked: props.revoked, revoked_at: props.revokedAt, expires_at: props.expiresAt,
  created_at: props.createdAt, lease: props.lease, credential_state: props.credentialState,
}))
const binding = computed(() => bindingText(props.kind, props.profile, props.mode))
const issuer = computed(() => issuerText(props.issuer))
const state = computed(() => stateBadge(fields.value, nowMs.value))
const lease = computed(() => (state.value.label === 'Revoked' || state.value.label === 'Lease ended' ? '' : leaseText(fields.value, nowMs.value)))
const dangling = computed(() => props.profileState === 'dangling')
</script>
