<template>
  <div class="space-y-2">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <span class="font-semibold">Signature bundle</span>
      <span v-for="row in summary.identity" :key="row.label" class="text-base-content/70">
        {{ row.label }}: <span class="text-base-content">{{ row.value }}</span>
      </span>
      <span v-if="summary.pinned" class="badge badge-warning badge-sm">pinned</span>
      <span v-if="summary.rollback" class="badge badge-warning badge-sm">rolled back</span>
    </div>
    <div v-if="summary.shortFingerprint" class="flex flex-wrap items-center gap-2">
      <span class="text-base-content/70">Fingerprint</span>
      <code class="font-mono">{{ summary.shortFingerprint }}</code>
      <button
        v-if="summary.fullDigest"
        type="button"
        class="btn btn-xs btn-ghost"
        :aria-expanded="showFullDigest"
        data-test="bundle-digest-toggle"
        @click="showFullDigest = !showFullDigest"
      >{{ showFullDigest ? 'Hide full digest' : 'Show full digest' }}</button>
    </div>
    <div v-if="showFullDigest && summary.fullDigest" class="flex flex-wrap items-center gap-2">
      <code class="font-mono text-xs break-all" data-test="bundle-full-digest">{{ summary.fullDigest }}</code>
      <button type="button" class="btn btn-xs btn-ghost" data-test="bundle-digest-copy" @click="copyFullDigest">
        {{ digestCopied ? 'Copied' : 'Copy' }}
      </button>
    </div>
    <div
      v-for="problem in summary.refreshProblems"
      :key="problem.kind"
      class="text-warning"
      role="status"
      data-test="bundle-refresh-problem"
      :data-kind="problem.kind"
    >{{ problem.explanation }}</div>
    <div v-if="summary.lastRejection" class="text-base-content/70" data-test="bundle-last-rejection">
      Last refused candidate: <span class="text-base-content">{{ summary.lastRejection.label }}</span>
      <span v-if="summary.lastRejection.detail"> &mdash; {{ summary.lastRejection.detail }}</span>
      <span v-if="summary.lastRejection.at"> ({{ summary.lastRejection.at }})</span>.
      The previous bundle is still serving.
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { SignatureBundleSummary } from '@/utils/signatureBundle'

const props = defineProps<{ summary: SignatureBundleSummary }>()

// Full digest is the rollback input (Spec 101 FR-010), so it is available in
// full on demand rather than cluttering the default view.
const showFullDigest = ref(false)
const digestCopied = ref(false)
async function copyFullDigest() {
  const text = props.summary.fullDigest || ''
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    digestCopied.value = true
    setTimeout(() => { digestCopied.value = false }, 1500)
  } catch {
    // Clipboard blocked: the digest is already visible and selectable.
  }
}
</script>
