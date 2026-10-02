<template>
  <div class="flex items-start gap-2" :data-test="`secret-toggle-${kind}-${name}`">
    <div class="flex-1 min-w-0">
      <label class="label py-0">
        <span class="label-text text-xs font-mono">{{ name }}</span>
      </label>
      <div class="flex items-center gap-1">
        <input
          :type="masked ? 'password' : 'text'"
          class="input input-bordered input-sm w-full font-mono"
          :value="modelValue"
          :placeholder="mode === 'secret' ? 'Value stored in the OS keyring on Add' : ''"
          autocomplete="off"
          spellcheck="false"
          data-1p-ignore
          data-lpignore="true"
          data-test="secret-toggle-value-input"
          @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
        />
        <button
          v-if="sensitive"
          type="button"
          class="btn btn-ghost btn-xs shrink-0"
          :aria-pressed="revealed"
          :aria-label="`${revealed ? 'Hide' : 'Show'} ${name} value`"
          data-test="secret-toggle-reveal"
          @click="revealed = !revealed"
        >
          {{ revealed ? 'Hide' : 'Show' }}
        </button>
      </div>
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
import { computed, ref } from 'vue'
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

// Masking is presentation only (FR-065, fix-usertest-web T201): a field in
// Secret mode, or whose name looks secret-like (the same D13 rule that picks
// the Secret default), is a password input with a Show/Hide toggle. Value vs
// Secret still decides storage, so Value mode keeps storing plain config.
const revealed = ref(false)
const sensitive = computed(() => props.mode === 'secret' || looksSecret(props.name))
const masked = computed(() => sensitive.value && !revealed.value)

function setMode(mode: 'value' | 'secret') {
  if (mode === 'secret' && !props.keyringAvailable) return
  emit('update:mode', mode)
}

defineExpose({ defaultMode: () => (looksSecret(props.name) ? 'secret' : 'value') })
</script>
