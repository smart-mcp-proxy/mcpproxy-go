<template>
  <div v-if="summary" class="space-y-4" data-test="signature-bundle-status">
    <!-- Spec 101 FR-020: only a degraded bundle needs action, so only that gets
         an alert. Identity (and the last refusal) live in the quiet panel below. -->
    <div v-if="summary.degraded" class="alert alert-warning" role="alert" data-test="bundle-degraded-alert">
      <svg class="w-5 h-5 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
      </svg>
      <div>
        <div class="font-semibold">Signature coverage degraded<span v-if="summary.degraded.reason" class="font-mono text-xs font-normal"> ({{ summary.degraded.reason }})</span></div>
        <div>{{ summary.degraded.explanation }}</div>
      </div>
    </div>

    <!-- Compact form (Review page): the identity rows stay folded until asked
         for, so a healthy install shows one quiet line. -->
    <details v-if="collapsible" class="text-sm bg-base-100 rounded-box shadow" data-test="bundle-details">
      <summary class="cursor-pointer px-4 py-3 flex flex-wrap items-center gap-x-3 gap-y-1">
        <span class="font-semibold">Signature database</span>
        <span class="text-base-content/70" data-test="bundle-summary-line">
          {{ summary.value }} runnable<template v-if="summary.shortFingerprint"> &middot; <code class="font-mono">{{ summary.shortFingerprint }}</code></template>
        </span>
      </summary>
      <div class="px-4 pb-3" data-test="bundle-status-panel">
        <SignatureBundlePanelBody :summary="summary" />
      </div>
    </details>

    <div v-else class="text-sm bg-base-100 rounded-box shadow px-4 py-3" data-test="bundle-status-panel">
      <SignatureBundlePanelBody :summary="summary" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatSignatureBundle, type SignatureBundle } from '@/utils/signatureBundle'
import SignatureBundlePanelBody from './SignatureBundlePanelBody.vue'

// Spec 101 FR-020 bundle status: fingerprint (full digest on demand), sequence,
// authority, epoch, signature state, staleness, last rejection, and the
// degraded-coverage banner. Driven by the `signature_bundle` object of
// GET /api/v1/security/overview; renders nothing when the daemon (or the
// session's scope) does not provide it.
const props = defineProps<{
  bundle: SignatureBundle | null | undefined
  /** Fold the identity panel behind a one-line summary. */
  collapsible?: boolean
}>()

const summary = computed(() => formatSignatureBundle(props.bundle))
</script>
