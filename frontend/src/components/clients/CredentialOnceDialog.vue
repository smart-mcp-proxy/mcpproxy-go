<template>
  <BaseDialog :open="open" :title="title || 'Client credential'" test-id="credential-once-dialog" @close="emit('close')">
    <p class="alert alert-warning text-sm" role="note">Shown once. MCPProxy stores only a hash.</p>
    <div class="form-control">
      <label class="label" for="credential-once-secret"><span class="label-text font-medium">Client credential</span></label>
      <div class="flex gap-2">
        <input
          id="credential-once-secret"
          class="input input-bordered input-sm w-full font-mono text-xs"
          readonly
          :value="credential"
          data-test="credential-once-secret"
          @focus="($event.target as HTMLInputElement).select()"
        />
        <button type="button" class="btn btn-sm" data-test="credential-once-copy" @click="copy('secret', credential)">{{ copied === 'secret' ? 'Copied' : 'Copy' }}</button>
      </div>
    </div>
    <div v-if="snippet" class="form-control">
      <label class="label" for="credential-once-snippet"><span class="label-text font-medium">Generic HTTP client config</span></label>
      <textarea
        id="credential-once-snippet"
        class="textarea textarea-bordered font-mono text-xs w-full"
        rows="8"
        readonly
        :value="snippet.generic_http"
        data-test="credential-once-snippet"
      />
      <button type="button" class="btn btn-xs mt-1 self-start" data-test="credential-once-copy-snippet" @click="copy('snippet', snippet.generic_http)">{{ copied === 'snippet' ? 'Copied' : 'Copy config' }}</button>
    </div>
    <template #actions>
      <button type="button" class="btn btn-primary btn-sm" data-test="credential-once-close" @click="emit('close')">I have saved it</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'

// Spec 108-i I12. The secret lives only in the props the parent passes for the
// life of this dialog. Nothing here writes it to a store, to localStorage or to
// the URL; the parent clears its copy when the dialog closes.
const props = defineProps<{ open: boolean; credential: string; snippet?: { generic_http: string; header_name: string } | null; title?: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const copied = ref<'secret' | 'snippet' | ''>('')

watch(() => props.open, open => { if (!open) copied.value = '' })

async function copy(which: 'secret' | 'snippet', text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = which
    window.setTimeout(() => { if (copied.value === which) copied.value = '' }, 2000)
  } catch {
    // The value is selectable in its field; a missing clipboard is not an error.
    copied.value = ''
  }
}
</script>
