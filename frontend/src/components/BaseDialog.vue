<template>
  <dialog ref="dialogEl" class="modal" :aria-labelledby="titleId" :data-test="testId">
    <div v-if="open" class="modal-box max-h-[90vh] overflow-y-auto" :class="wide ? 'max-w-3xl' : 'max-w-lg'">
      <h3 :id="titleId" class="font-bold text-lg mb-3">{{ title }}</h3>
      <div class="space-y-3">
        <slot />
      </div>
      <div v-if="$slots.actions" class="modal-action">
        <slot name="actions" />
      </div>
    </div>
    <form method="dialog" class="modal-backdrop" @submit.prevent="emit('close')"><button tabindex="-1" aria-label="Close dialog">close</button></form>
  </dialog>
</template>

<script setup lang="ts">
import { useDialogOpen } from '@/composables/useDialogOpen'

// A native <dialog> (Spec 109 FR-055: showModal puts it in the top layer) with
// an aria-labelledby title. Focus lands on the first field when it opens and
// returns to the trigger when it closes - both are the browser's own modal
// behaviour - and Escape closes it through useDialogOpen.
const props = defineProps<{ open: boolean; title: string; testId?: string; wide?: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { dialogEl } = useDialogOpen(() => props.open, () => emit('close'))
const titleId = `dialog-title-${Math.random().toString(36).slice(2, 9)}`
</script>
