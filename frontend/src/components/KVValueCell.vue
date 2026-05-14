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
    <!-- Keyring reference: rendered as a chip; no reveal toggle. -->
    <template v-if="isKeyringRef">
      <span class="badge badge-info badge-sm gap-1" :title="rawValue">
        <span aria-hidden="true">🔑</span>
        <span class="font-mono text-xs">stored in keyring: {{ keyringName }}</span>
      </span>
    </template>
    <!-- Env reference (${env:VAR}): also a chip with no reveal toggle. -->
    <template v-else-if="isEnvRef">
      <span class="badge badge-ghost badge-sm gap-1" :title="rawValue">
        <span aria-hidden="true">$</span>
        <span class="font-mono text-xs">env var: {{ envRefName }}</span>
      </span>
    </template>
    <!-- Literal value path. Two sub-cases:
         1. Backend redacted the value (`***REDACTED***` sentinel). The
            real string isn't on the client; reveal would just expose
            the sentinel and Convert-to-secret has nothing to upload to
            the keyring. We surface a small "backend-redacted" hint
            instead so the user understands why those actions are
            disabled. Editing the value still works through the inline
            edit button — the PATCH endpoint deep-merges, so typing a
            new value replaces just that one header server-side.
         2. Plaintext literal: mask by default, eye to reveal, lock to
            convert. -->
    <template v-else>
      <code v-if="isBackendRedacted" class="bg-base-200 px-1.5 py-0.5 rounded text-xs text-base-content/50" title="Backend redacted this value. Edit to overwrite — the PATCH endpoint deep-merges, so the unchanged real value is preserved.">{{ rawValue }}</code>
      <template v-else>
        <code v-if="revealed" class="bg-base-200 px-1.5 py-0.5 rounded text-xs break-all">{{ rawValue }}</code>
        <code v-else class="bg-base-200 px-1.5 py-0.5 rounded text-xs text-base-content/50">{{ redactedPreview }}</code>
        <button
          class="btn btn-ghost btn-xs"
          :title="revealed ? 'Hide value' : 'Reveal value'"
          @click="revealed ? $emit('hide') : $emit('reveal')"
        >
          <span aria-hidden="true">{{ revealed ? '🙈' : '👁' }}</span>
        </button>
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
    </template>
    <button class="btn btn-ghost btn-xs" title="Edit" @click="$emit('start-edit')" :disabled="busy">✎</button>
    <button class="btn btn-ghost btn-xs text-error" title="Delete" @click="$emit('delete')" :disabled="busy">✕</button>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

// KVValueCell encapsulates the per-row UI for the Headers and Environment
// Variables cards on ServerDetail. It owns the visual state (revealed vs
// hidden, edit-in-progress draft) and emits intent events; the parent owns
// the persistence (PATCH the server, refresh the store).

const props = defineProps<{
  scope: 'header' | 'env'
  k: string
  rawValue: string
  isEditing: boolean
  revealed: boolean
  busy?: boolean
}>()

const emit = defineEmits<{
  (e: 'reveal'): void
  (e: 'hide'): void
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
// The Go backend redacts secret header values via
// `redactServerHeaders` so an MCP agent calling `upstream_servers list`
// cannot exfiltrate Bearer tokens (PR #425). The REST API and SSE
// channel inherit the same redaction. When that sentinel surfaces, the
// real string is not in our hands — reveal/convert are disabled, but
// editing still works because the PATCH endpoint deep-merges (the
// untouched redacted key simply stays out of the patch body).
const isBackendRedacted = computed(() => props.rawValue === '***REDACTED***')

const redactedPreview = computed(() => {
  const v = props.rawValue ?? ''
  if (!v) return '(empty)'
  if (v.length <= 4) return '••••'
  return '••••' + v.slice(-2) + ` (${v.length} chars)`
})

const placeholder = computed(() =>
  props.scope === 'header' ? 'value (e.g. Bearer abc...)' : 'value (literal or ${keyring:name})'
)
</script>
