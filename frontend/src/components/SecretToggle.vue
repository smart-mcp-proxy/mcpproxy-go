<template>
  <div class="flex items-start gap-2" :data-test="`secret-toggle-${kind}-${name}`">
    <div class="flex-1 min-w-0">
      <label class="label py-0">
        <span class="label-text text-xs font-mono">{{ name }}</span>
      </label>
      <input
        :type="mode === 'secret' ? 'password' : 'text'"
        class="input input-bordered input-sm w-full font-mono"
        :value="modelValue"
        :placeholder="mode === 'secret' ? 'Value stored in the OS keyring on Add' : ''"
        data-test="secret-toggle-value-input"
        @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
      />
    </div>
    <div class="join mt-6 shrink-0" role="group" :aria-label="`${name} storage`">
      <button
        type="button"
        class="btn btn-xs join-item"
        :class="mode === 'value' ? 'btn-active' : ''"
        data-test="secret-toggle-mode-value"
        @click="setMode('value')"
      >
        Value
      </button>
      <button
        type="button"
        class="btn btn-xs join-item"
        :class="mode === 'secret' ? 'btn-active' : ''"
        :disabled="!keyringAvailable"
        :title="!keyringAvailable ? keyringReason || 'OS keyring unavailable' : 'Store in the OS keyring'"
        data-test="secret-toggle-mode-secret"
        @click="setMode('secret')"
      >
        Secret
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { looksSecret } from '@/utils/secretLike'

// One Value/Secret toggle for a single env var or header field (Spec 109
// FR-065). The parent owns the actual value; this component only owns the
// mode (value vs secret) and defaults it to Secret for a secret-like name.
// When the keyring is unavailable the Secret button is disabled with the
// reason (FR-065) and setMode refuses to ENTER secret mode, but a parent that
// seeds `mode` from secret_like can still hand this component 'secret' while
// the keyring is unavailable. Parents therefore must fail closed themselves:
// CatalogSearch and PasteServer disable Add while any field is in that state
// (never silently downgrading to plaintext) until the user picks Value.
interface Props {
  name: string
  kind: 'env' | 'header'
  modelValue: string
  mode: 'value' | 'secret'
  keyringAvailable: boolean
  keyringReason?: string
}
const props = defineProps<Props>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:mode': [mode: 'value' | 'secret']
}>()

function setMode(mode: 'value' | 'secret') {
  if (mode === 'secret' && !props.keyringAvailable) return
  emit('update:mode', mode)
}

defineExpose({ defaultMode: () => (looksSecret(props.name) ? 'secret' : 'value') })
</script>
