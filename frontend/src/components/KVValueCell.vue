<template>
  <div v-if="isEditing" class="flex gap-2 items-center">
    <input
      v-model="draftValue"
      type="text"
      class="input input-bordered input-xs flex-1 font-mono"
      :placeholder="placeholder"
      ref="editInput"
      @keyup.enter="$emit('save', draftValue)"
      @keyup.esc="$emit('cancel-edit')"
    />
    <button class="btn btn-xs btn-primary" :disabled="busy || !draftValue" @click="$emit('save', draftValue)">Save</button>
    <button class="btn btn-xs btn-ghost" :disabled="busy" @click="$emit('cancel-edit')">Cancel</button>
  </div>
  <div v-else class="flex gap-2 items-center flex-wrap">
    <!-- Keyring reference: rendered as a chip; no convert affordance. -->
    <template v-if="isKeyringRef">
      <span class="badge badge-info badge-sm gap-1" :title="rawValue">
        <span aria-hidden="true">🔑</span>
        <span class="font-mono text-xs">stored in keyring: {{ keyringName }}</span>
      </span>
    </template>
    <!-- Env reference (${env:VAR}): also a chip. -->
    <template v-else-if="isEnvRef">
      <span class="badge badge-ghost badge-sm gap-1" :title="rawValue">
        <span aria-hidden="true">$</span>
        <span class="font-mono text-xs">env var: {{ envRefName }}</span>
      </span>
    </template>
    <!-- Literal value path. Two cases produce identical visuals:
         1. Header value the backend already masked (`••••<last2> (<N> chars)`
            format from oauth.RedactStringHeaders) — we render the masked
            string verbatim. Convert-to-secret on these rows hits the
            server-side `/config-to-secret` endpoint, which has the real
            value on disk.
         2. Plaintext literal (env vars, or headers when
            `reveal_secret_headers: true`) — we mask client-side using
            the same format so it looks the same to the user.
         The UI no longer has a reveal button; the two routes to peek
         at the value (edit + cancel, or open the config file) are
         documented in CLAUDE.md. -->
    <template v-else>
      <code class="bg-base-200 px-1.5 py-0.5 rounded text-xs text-base-content/70 font-mono break-all">{{ displayValue }}</code>
      <button
        class="btn btn-ghost btn-xs"
        title="Move value into the OS keyring and reference it as ${keyring:name}"
        @click="$emit('convert')"
        :disabled="busy"
      >
        <span aria-hidden="true">🔒</span>
        <span class="hidden md:inline ml-1">Convert to secret</span>
      </button>
    </template>
    <button class="btn btn-ghost btn-xs" title="Edit" @click="$emit('start-edit')" :disabled="busy">✎</button>
    <button class="btn btn-ghost btn-xs text-error" title="Delete" @click="$emit('delete')" :disabled="busy">✕</button>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

// KVValueCell encapsulates the per-row UI for the Headers and Environment
// Variables cards on ServerDetail. It owns the visual state (edit-in-
// progress draft) and emits intent events; the parent owns the
// persistence (PATCH the server, call config-to-secret, refresh).

const props = defineProps<{
  scope: 'header' | 'env'
  k: string
  rawValue: string
  isEditing: boolean
  busy?: boolean
}>()

const emit = defineEmits<{
  (e: 'start-edit'): void
  (e: 'cancel-edit'): void
  (e: 'save', value: string): void
  (e: 'delete'): void
  (e: 'convert'): void
}>()

void emit

const draftValue = ref(props.rawValue)
const editInput = ref<HTMLInputElement | null>(null)

// Keep the draft in sync with the parent's raw value when entering edit
// mode. We intentionally do NOT mirror raw → draft outside edit mode so
// the user's in-progress text never gets clobbered by a background SSE
// refresh.
watch(
  () => [props.isEditing, props.rawValue] as const,
  ([editing, raw]) => {
    if (editing) {
      draftValue.value = raw
      nextTick(() => editInput.value?.focus())
    }
  },
  { immediate: true }
)

const isKeyringRef = computed(() => /^\$\{keyring:[^}]+\}$/.test(props.rawValue ?? ''))
const isEnvRef = computed(() => /^\$\{env:[^}]+\}$/.test(props.rawValue ?? ''))
const keyringName = computed(() => {
  const m = (props.rawValue ?? '').match(/^\$\{keyring:([^}]+)\}$/)
  return m ? m[1] : ''
})
const envRefName = computed(() => {
  const m = (props.rawValue ?? '').match(/^\$\{env:([^}]+)\}$/)
  return m ? m[1] : ''
})

// Render the literal value with the masked-display format. If the
// backend already masked the value (sensitive header, default redaction
// policy), it arrives already in this exact format — we pass it
// through unchanged. If the backend sent plaintext (env var, or headers
// when `reveal_secret_headers: true`), we mask client-side here for
// consistency. The detection is "starts with a mask bullet", which is
// idempotent: re-masking a masked string is a no-op.
const MASK_PREFIX = '••••'
const displayValue = computed(() => {
  const v = props.rawValue ?? ''
  if (!v) return '(empty)'
  if (v.startsWith(MASK_PREFIX) || v === '(empty)') return v
  if (v.length <= 4) return MASK_PREFIX
  return MASK_PREFIX + v.slice(-2) + ` (${v.length} chars)`
})

const placeholder = computed(() =>
  props.scope === 'header' ? 'value (e.g. Bearer abc...)' : 'value (literal or ${keyring:name})'
)
</script>
