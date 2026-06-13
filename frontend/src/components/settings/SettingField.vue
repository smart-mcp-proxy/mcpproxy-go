<template>
  <div
    class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 py-3 px-2 -mx-2 border-b border-base-200 last:border-0 rounded transition-colors"
    :class="dirty ? 'bg-warning/5 border-l-2 border-l-warning' : 'border-l-2 border-l-transparent'"
    :data-test="`setting-row-${field.key}`"
  >
    <!-- Label + help + badges -->
    <div class="sm:max-w-md">
      <div class="flex items-center gap-2 flex-wrap">
        <span class="font-medium">{{ field.label }}</span>
        <span v-if="dirty" class="badge badge-warning badge-xs" title="Unsaved change">●</span>
        <a
          v-if="docsHref"
          :href="docsHref"
          target="_blank"
          rel="noopener noreferrer"
          class="link link-primary text-xs font-normal"
          :data-test="`setting-docs-${field.key}`"
          title="Open documentation"
          @click.stop
        >docs ↗</a>
        <span v-if="field.restart" class="badge badge-warning badge-xs gap-1" title="Requires restart">
          restart
        </span>
        <span v-if="field.danger && field.danger.tone !== 'info'" class="badge badge-error badge-xs" title="Sensitive change">
          sensitive
        </span>
      </div>
      <p v-if="field.help" class="text-xs text-base-content/60 mt-0.5">{{ field.help }}</p>
    </div>

    <!-- Control -->
    <div class="shrink-0">
      <!-- toggle -->
      <input
        v-if="field.control === 'toggle'"
        type="checkbox"
        class="toggle toggle-primary"
        :checked="!!modelValue"
        :data-test="`setting-toggle-${field.key}`"
        @change="emitVal(($event.target as HTMLInputElement).checked)"
      />

      <!-- select -->
      <select
        v-else-if="field.control === 'select'"
        class="select select-bordered select-sm min-w-[12rem]"
        :value="modelValue ?? ''"
        :data-test="`setting-select-${field.key}`"
        @change="emitVal(($event.target as HTMLSelectElement).value)"
      >
        <option v-for="opt in field.options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
      </select>

      <!-- number -->
      <div v-else-if="field.control === 'number'" class="flex flex-col items-end">
        <input
          type="number"
          class="input input-bordered input-sm w-32"
          :class="{ 'input-error': validationError }"
          :value="modelValue"
          :min="field.min"
          :max="field.max"
          :step="field.step"
          :data-test="`setting-number-${field.key}`"
          @input="onNumber(($event.target as HTMLInputElement).value)"
        />
        <span v-if="validationError" class="text-error text-xs mt-1" :data-test="`setting-error-${field.key}`">{{ validationError }}</span>
      </div>

      <!-- secret -->
      <template v-else-if="field.control === 'secret'">
        <div class="join">
          <input
            :type="showSecret ? 'text' : 'password'"
            class="input input-bordered input-sm join-item w-56 font-mono"
            :value="modelValue ?? ''"
            :placeholder="field.placeholder"
            :data-test="`setting-secret-${field.key}`"
            @input="emitVal(($event.target as HTMLInputElement).value)"
          />
          <button type="button" class="btn btn-sm join-item" :title="showSecret ? 'Hide' : 'Show'" @click="showSecret = !showSecret">
            {{ showSecret ? '🙈' : '👁' }}
          </button>
          <button
            type="button"
            class="btn btn-sm join-item"
            :class="{ 'btn-success': copied }"
            :data-test="`setting-copy-${field.key}`"
            :title="copied ? 'Copied!' : 'Copy to clipboard'"
            :disabled="!modelValue"
            @click="copy"
          >
            {{ copied ? '✓' : '📋' }}
          </button>
          <button
            type="button"
            class="btn btn-sm join-item"
            :data-test="`setting-regenerate-${field.key}`"
            title="Generate a new random key"
            @click="regenEl?.showModal()"
          >
            ↻
          </button>
        </div>
        <dialog ref="regenEl" class="modal" :data-test="`setting-regenerate-confirm-${field.key}`">
          <div class="modal-box">
            <h3 class="font-bold text-lg">Generate a new {{ field.label }}?</h3>
            <p class="text-sm mt-2">
              A new random value will replace the current one in the field. Nothing changes until you click
              <b>Save changes</b> — after saving you'll need to update connected clients and restart.
            </p>
            <div class="modal-action">
              <button class="btn btn-sm" @click="regenEl?.close()" :data-test="`setting-regenerate-cancel-${field.key}`">Cancel</button>
              <button class="btn btn-sm btn-primary" @click="confirmRegenerate" :data-test="`setting-regenerate-proceed-${field.key}`">
                Generate
              </button>
            </div>
          </div>
          <form method="dialog" class="modal-backdrop"><button>close</button></form>
        </dialog>
      </template>

      <!-- text / duration -->
      <div v-else-if="field.control === 'text' || field.control === 'duration'" class="flex flex-col items-end">
        <input
          type="text"
          class="input input-bordered input-sm w-56 font-mono"
          :class="{ 'input-error': validationError }"
          :value="modelValue ?? ''"
          :placeholder="field.placeholder"
          :data-test="`setting-text-${field.key}`"
          @input="emitText(($event.target as HTMLInputElement).value)"
        />
        <span v-if="validationError" class="text-error text-xs mt-1" :data-test="`setting-error-${field.key}`">{{ validationError }}</span>
      </div>

      <!-- textarea (multi-line text, e.g. MCP server instructions) -->
      <div v-else-if="field.control === 'textarea'" class="flex flex-col items-stretch w-full sm:w-96">
        <textarea
          class="textarea textarea-bordered textarea-sm w-full font-mono leading-snug"
          :class="{ 'textarea-error': validationError }"
          rows="6"
          :value="modelValue ?? ''"
          :placeholder="field.placeholder"
          :data-test="`setting-textarea-${field.key}`"
          @input="emitText(($event.target as HTMLTextAreaElement).value)"
        ></textarea>
        <span v-if="validationError" class="text-error text-xs mt-1" :data-test="`setting-error-${field.key}`">{{ validationError }}</span>
      </div>

      <!-- multiselect (checkbox group) -->
      <div v-else-if="field.control === 'multiselect'" class="flex flex-wrap gap-3 justify-end max-w-xs">
        <label v-for="opt in field.options" :key="opt.value" class="label cursor-pointer gap-1 p-0">
          <input
            type="checkbox"
            class="checkbox checkbox-xs"
            :checked="Array.isArray(modelValue) && modelValue.includes(opt.value)"
            :data-test="`setting-multi-${field.key}-${opt.value}`"
            @change="toggleMulti(opt.value, ($event.target as HTMLInputElement).checked)"
          />
          <span class="text-xs">{{ opt.label }}</span>
        </label>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { docsUrl, validateField, type SettingField } from '@/views/settings/fields'

const props = defineProps<{ field: SettingField; modelValue: any; dirty?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: any): void }>()

const showSecret = ref(false)
const copied = ref(false)
const regenEl = ref<HTMLDialogElement | null>(null)
const docsHref = computed(() => docsUrl(props.field.docs))

async function copy() {
  const val = props.modelValue
  if (!val) return
  try {
    await navigator.clipboard.writeText(String(val))
  } catch {
    // clipboard API unavailable (e.g. non-secure context) — fall back
    const ta = document.createElement('textarea')
    ta.value = String(val)
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
    } catch {
      /* ignore */
    }
    document.body.removeChild(ta)
  }
  copied.value = true
  setTimeout(() => {
    copied.value = false
  }, 1500)
}

function confirmRegenerate() {
  regenEl.value?.close()
  regenerate()
}

// Only surface validation errors for fields the user has actually edited, so a
// pre-existing value (e.g. a short legacy API key) never alarms or blocks
// saving unrelated fields.
const validationError = computed(() => (props.dirty ? validateField(props.field, props.modelValue) : null))

function emitVal(v: any) {
  emit('update:modelValue', v)
}

// Text/duration input. A blank optional duration is "unset" — emit null
// (reset to the default) rather than "" which the backend can't parse as a
// duration. This mirrors the tri-state contract (nil = default).
function emitText(raw: string) {
  if (props.field.control === 'duration' && props.field.optional && raw.trim() === '') {
    emitVal(null)
    return
  }
  // Empty-means-default: a textarea cleared to only whitespace persists as ""
  // (the backend maps "" → built-in default), never a whitespace-only string.
  if (props.field.control === 'textarea' && raw.trim() === '') {
    emitVal('')
    return
  }
  emitVal(raw)
}

function onNumber(raw: string) {
  if (raw === '') {
    emitVal('')
    return
  }
  emitVal(Number(raw))
}

function toggleMulti(value: string, checked: boolean) {
  const cur = Array.isArray(props.modelValue) ? [...props.modelValue] : []
  const idx = cur.indexOf(value)
  if (checked && idx === -1) cur.push(value)
  if (!checked && idx !== -1) cur.splice(idx, 1)
  emitVal(cur)
}

function regenerate() {
  // 32-byte random hex key — generated client-side; user must save + restart.
  const bytes = new Uint8Array(32)
  crypto.getRandomValues(bytes)
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  showSecret.value = true
  emitVal(hex)
}

defineExpose({ validationError })
</script>
